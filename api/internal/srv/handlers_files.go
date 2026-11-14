package srv

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// TenderFileDownload verifies that the requested file belongs to the tender,
// then returns a short-lived MinIO presigned URL. MinIO credentials never
// leave the Go API and the browser never talks to MinIO with credentials.
func (s *Server) TenderFileDownload(w http.ResponseWriter, r *http.Request) {
	tenderID := strings.TrimSpace(r.PathValue("tenderId"))
	fileID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("fileId")), 10, 64)
	if tenderID == "" || err != nil {
		fail(w, http.StatusBadRequest, "invalid tender or file id")
		return
	}

	row, err := s.QueryOne(r.Context(), `
		SELECT tf.cloud_file_name, tf.file_name
		FROM tender_files tf
		JOIN tenders t ON t.id = tf.tender_id
		WHERE t.tender_id = $1 AND tf.id = $2 AND tf.is_deleted = FALSE`, tenderID, fileID)
	if err != nil {
		sendErr(w, err)
		return
	}
	if row == nil || strings.TrimSpace(toString(row["cloud_file_name"])) == "" {
		fail(w, http.StatusNotFound, "file not found")
		return
	}

	endpoint := strings.TrimSpace(os.Getenv("MINIO_PUBLIC_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("MINIO_ENDPOINT"))
	}
	accessKey := strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY"))
	bucket := strings.TrimSpace(os.Getenv("MINIO_BUCKET"))
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		fail(w, http.StatusServiceUnavailable, "file storage is not configured")
		return
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		fail(w, http.StatusServiceUnavailable, "invalid file storage endpoint")
		return
	}
	client, err := minio.New(parsed.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.EqualFold(parsed.Scheme, "https"),
		Region: strings.TrimSpace(os.Getenv("MINIO_REGION")),
	})
	if err != nil {
		sendErr(w, err)
		return
	}

	presigned, err := client.PresignedGetObject(r.Context(), bucket, strings.TrimSpace(toString(row["cloud_file_name"])), 15*time.Minute, nil)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"url":       presigned.String(),
		"file_name": toString(row["file_name"]),
		"expires":   time.Now().Add(15 * time.Minute).UTC(),
	})
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
