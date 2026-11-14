package srv

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// httpLog logs the raw server error without echoing it to the client.
func httpLog(err error) {
	log.Printf("sql error: %v", err)
}

// Server carries the pool and shared helpers.
type Server struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Server { return &Server{Pool: pool} }

// JSON writes a JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode: %v", err)
	}
}

// clean strips pgx error decoration.
func clean(err error) error {
	return err
}
func fail(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]any{"error": msg})
}

// Query runs the given SQL and maps every row to a lowercase-keyed object.
// numeric/xid types are reduced to float64, timestamps to RFC3339 strings.
func (s *Server) Query(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		log.Printf("query failed (sql head): %.400s", sql)
		return nil, clean(err)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	for i := range fds {
		fds[i].Name = lower(fds[i].Name)
	}
	out := make([]map[string]any, 0, 64)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, v := range vals {
			m[fds[i].Name] = normalizeVal(v)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

func normalizeVal(v any) any {
	switch x := v.(type) {
	case pgtype.Numeric:
		f, err := x.Float64Value()
		if err != nil || !f.Valid {
			return nil
		}
		return f.Float64
	case time.Time:
		return x.Format(time.RFC3339)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case pgtype.UUID:
		return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			x.Bytes[0], x.Bytes[1], x.Bytes[2], x.Bytes[3], x.Bytes[4], x.Bytes[5], x.Bytes[6], x.Bytes[7],
			x.Bytes[8], x.Bytes[9], x.Bytes[10], x.Bytes[11], x.Bytes[12], x.Bytes[13], x.Bytes[14], x.Bytes[15])
	case [16]byte:
		b := x
		return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
	default:
		return v
	}
}

// QueryOne returns the first row or nil.
func (s *Server) QueryOne(ctx context.Context, sql string, args ...any) (map[string]any, error) {
	r, err := s.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(r) == 0 {
		return nil, nil
	}
	return r[0], nil
}

// --- request helpers ---

func qp(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}

func qpInt(r *http.Request, key string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v < 1 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func qpOff(r *http.Request) int {
	v, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// paging applies page/page_size (or explicit limit/offset) and returns limit, offset.
func paging(r *http.Request) (int, int) {
	if l := r.URL.Query().Get("limit"); l != "" {
		lim, err := strconv.Atoi(l)
		if err == nil && lim > 0 && lim <= 500 {
			return lim, qpOff(r)
		}
	}
	page := qpInt(r, "page", 1, 100000)
	ps := qpInt(r, "page_size", 50, 200)
	return ps, (page - 1) * ps
}
