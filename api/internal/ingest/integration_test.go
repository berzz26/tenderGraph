//go:build integration

package ingest

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/projection"
)

func TestIntegrationSourceScannersAndControlPlane(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	r := ApprovedRegistry()
	control := ControlStore{Pool: pool}
	if err := control.Seed(ctx, r); err != nil {
		t.Fatal(err)
	}
	scanner := SourceScanner{Pool: pool}
	for _, spec := range r.All() {
		high, err := scanner.HighWatermark(ctx, spec)
		if err != nil {
			t.Fatalf("%s high watermark: %v", spec.Name, err)
		}
		if high.Empty() {
			t.Fatalf("%s unexpectedly empty", spec.Name)
		}
		b, err := scanner.Scan(ctx, spec, Cursor{}, high, 3, 1770000000000)
		if err != nil {
			t.Fatalf("%s scan: %v", spec.Name, err)
		}
		if b.Items == 0 || b.Cursor.Empty() {
			t.Fatalf("%s returned no bounded rows", spec.Name)
		}
	}
	status, err := control.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 7 {
		t.Fatalf("control status rows=%d", len(status))
	}
}

func TestIntegrationFailureAfterGraphWriteRecoversByReplay(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	token := os.Getenv("HYDRADB_AUTH_TOKEN_FILE")
	if dsn == "" || token == "" {
		t.Skip("DATABASE_URL and HYDRADB_AUTH_TOKEN_FILE are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	clients, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())

	registry := ApprovedRegistry()
	control := ControlStore{Pool: pool}
	if err := control.Seed(ctx, registry); err != nil {
		t.Fatal(err)
	}
	writer := projection.HydraWriter{Client: clients.HTTP, BatchSize: 500}
	failing := &Engine{
		Scanner: SourceScanner{Pool: pool}, Writer: writer, Control: control, Registry: registry,
		BatchSize: 500, MaxAttempts: 1,
		AfterWrite: func(context.Context, RunSummary, projection.Batch) error {
			return errors.New("injected failure after graph write")
		},
	}
	if _, err := failing.RunDataset(ctx, "contractor_v2", "selected"); err == nil {
		t.Fatal("expected injected failure")
	}
	failed, err := control.FailedBatches(ctx, "contractor_v2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) == 0 {
		t.Fatal("expected failed batch checkpoint")
	}

	recovery := &Engine{Scanner: SourceScanner{Pool: pool}, Writer: writer, Control: control, Registry: registry, BatchSize: 500, MaxAttempts: 1}
	summary, err := recovery.RunDataset(ctx, "contractor_v2", "selected")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "SUCCEEDED" {
		t.Fatalf("recovery status=%s", summary.Status)
	}
}
