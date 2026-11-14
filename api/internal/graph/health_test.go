package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeBolt is a configurable Prober for health tests.
type fakeBolt struct {
	verifyErr error
	queryErr  error
	queries   int
}

func (f *fakeBolt) VerifyConnectivity(ctx context.Context) error { return f.verifyErr }
func (f *fakeBolt) ReadQuery(ctx context.Context, cypher string, params map[string]any) (*Result, error) {
	f.queries++
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &Result{Columns: []string{"c"}, Rows: [][]any{{int64(0)}}}, nil
}

func newTestChecker(admin, httpBase string, bolt Prober) *HealthChecker {
	return &HealthChecker{
		adminURL:  admin,
		httpBase:  httpBase,
		token:     "test-token",
		namespace: "default",
		timeout:   2 * time.Second,
		client:    &http.Client{Timeout: 2 * time.Second},
		bolt:      bolt,
	}
}

func TestHealthAllComponentsHealthy(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/metrics":
			_, _ = w.Write([]byte("# TYPE graph_build_info gauge\ngraph_build_info 1\n"))
		default:
			t.Errorf("unexpected admin path %q", r.URL.Path)
		}
	}))
	defer admin.Close()
	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" || r.Method != http.MethodGet {
			t.Errorf("unexpected healthz request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer q.Close()

	h := newTestChecker(admin.URL, q.URL, &fakeBolt{})
	rep := h.Check(context.Background())
	if rep.Status != "healthy" || !rep.Ready {
		t.Fatalf("report = %+v", rep)
	}
	for _, c := range rep.Components {
		if !c.OK {
			t.Errorf("component %s not ok: %+v", c.Name, c)
		}
		if !c.Reachable {
			t.Errorf("component %s not reachable: %+v", c.Name, c)
		}
	}
}

func TestHealthReachableButNotReady(t *testing.T) {
	// Admin up but /readyz 404; query API down; bolt auth fails (port answers).
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte("graph_build_info 1\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer admin.Close()

	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer q.Close()

	h := newTestChecker(admin.URL, q.URL, &fakeBolt{
		verifyErr: &APIError{Status: 401, Code: "unauthenticated", Message: "auth required"},
	})
	rep := h.Check(context.Background())
	if rep.Status != "degraded" {
		t.Fatalf("expected degraded, got %+v", rep)
	}
	if rep.Ready {
		t.Error("ready must be false when bolt probe fails")
	}
	for _, c := range rep.Components {
		if c.Name == "admin_ready" && c.OK {
			t.Error("admin_ready should not be OK on 404")
		}
		if c.Name == "http_healthz" && c.OK {
			t.Error("http_healthz should not be OK on 503")
		}
		if !c.Reachable && c.Name != "bolt_query" {
			t.Errorf("component %s should be reachable", c.Name)
		}
	}
}

func TestHealthUnreachable(t *testing.T) {
	// Closed servers = connection refused on all HTTP probes.
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	admin.Close()
	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	q.Close()

	h := newTestChecker(admin.URL, q.URL, &fakeBolt{
		verifyErr: fmt.Errorf("bolt: verify connectivity: connection refused"),
	})
	rep := h.Check(context.Background())
	if rep.Status != "unavailable" {
		t.Fatalf("expected unavailable, got %+v", rep)
	}
	for _, c := range rep.Components {
		if c.Reachable {
			t.Errorf("component %s must not be reachable: %+v", c.Name, c)
		}
		if c.OK {
			t.Errorf("component %s must not be ok: %+v", c.Name, c)
		}
	}
}

func TestHealthBoltNil(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte("graph_build_info 1\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer admin.Close()
	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer q.Close()

	h := newTestChecker(admin.URL, q.URL, nil)
	rep := h.Check(context.Background())
	if rep.Status != "degraded" {
		t.Fatalf("expected degraded (bolt missing), got %+v", rep)
	}
	var bolt Component
	for _, c := range rep.Components {
		if c.Name == "bolt_query" {
			bolt = c
		}
	}
	if bolt.OK || !strings.Contains(bolt.Detail, "not configured") {
		t.Errorf("bolt component = %+v", bolt)
	}
}

func TestHealthMetricsBodyMismatch(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Everything 200 but metrics body has no graph_ lines.
		_, _ = w.Write([]byte("hello\n"))
	}))
	defer admin.Close()
	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer q.Close()
	h := newTestChecker(admin.URL, q.URL, &fakeBolt{})
	rep := h.Check(context.Background())
	for _, c := range rep.Components {
		if c.Name == "admin_metrics" && c.OK {
			t.Error("metrics with wrong body must not be OK")
		}
	}
}
