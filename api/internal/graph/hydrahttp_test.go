package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestHTTPClient points an HTTPClient at a test server with short timeouts.
func newTestHTTPClient(srv *httptest.Server) *HTTPClient {
	c := &HTTPClient{
		base:      strings.TrimSuffix(srv.URL, "/"),
		graph:     "default",
		namespace: "default",
		cellID:    "cell-0",
		token:     "test-token-not-real",
		timeout:   5 * time.Second,
		client:    srv.Client(),
	}
	return c
}

// okEnvelope is the verified 200 shape with typed cells.
func okEnvelope(t *testing.T, w http.ResponseWriter, columns []string, rows [][]map[string]any, cursor uint64) {
	t.Helper()
	resp := map[string]any{
		"query_id":    "q-test-1",
		"columns":     columns,
		"rows":        rows,
		"read_epoch":  11,
		"next_cursor": nil,
		"bookmark":    fmt.Sprintf("sgk:1:%d", cursor),
	}
	if cursor > 0 {
		resp["next_cursor"] = cursor
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestHTTPClientDoQueryDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graphs/default/query" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-not-real" {
			t.Errorf("auth header = %q", got)
		}
		if got := r.Header.Get(namespaceHeader); got != "default" {
			t.Errorf("namespace header = %q", got)
		}
		var body struct {
			CellID     string         `json:"cell_id"`
			Query      string         `json:"query"`
			Parameters map[string]any `json:"parameters"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.CellID != "cell-0" {
			t.Errorf("cell_id = %q", body.CellID)
		}
		okEnvelope(t, w, []string{"id", "name", "flag"}, [][]map[string]any{
			{
				{"type": "vertex_id", "value": 777001},
				{"type": "string", "value": "alpha"},
				{"type": "boolean", "value": true},
			},
		}, 0)
	}))
	defer srv.Close()

	c := newTestHTTPClient(srv)
	res, err := c.DoQuery(ctxBG(), "MATCH (n:Test {id: $id}) RETURN n.id AS id, n.name AS name, n.flag AS flag",
		map[string]any{"id": 777001})
	if err != nil {
		t.Fatalf("DoQuery: %v", err)
	}
	if res.QueryID != "q-test-1" || len(res.Columns) != 3 {
		t.Fatalf("unexpected result %+v", res)
	}
	id, err := res.Int64(0, 0)
	if err != nil || id != 777001 {
		t.Errorf("Int64: %v %v", id, err)
	}
	name, err := res.String(0, 1)
	if err != nil || name != "alpha" {
		t.Errorf("String: %v %v", name, err)
	}
	if res.Value(0, 2) != true {
		t.Errorf("bool cell = %v", res.Value(0, 2))
	}
	if res.Bookmark == "" {
		t.Error("bookmark missing")
	}
	if res.NextCursor != nil {
		t.Error("next_cursor should be nil")
	}
}

func TestHTTPClientDecodeSignedAndFloat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okEnvelope(t, w, []string{"a", "b", "nul"}, [][]map[string]any{
			{
				{"type": "signed_integer", "value": -42},
				{"type": "float", "value": 3.5},
				{"type": "null"},
			},
		}, 0)
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	res, err := c.DoQuery(ctxBG(), "MATCH (n:Test) RETURN 1 AS a, 1 AS b, 1 AS nul", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := res.Int64(0, 0)
	if a != -42 {
		t.Errorf("signed int = %d", a)
	}
	if v, ok := res.Value(0, 1).(float64); !ok || v != 3.5 {
		t.Errorf("float = %v", res.Value(0, 1))
	}
	if res.Value(0, 2) != nil {
		t.Errorf("null = %v", res.Value(0, 2))
	}
}

func TestHTTPClientAPIErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "invalid_request", "message": "OpenCypher parse error"},
		})
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	_, err := c.DoQuery(ctxBG(), "NOT CYPHER", nil)
	ae, ok := IsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if ae.Status != 400 || ae.Code != "invalid_request" {
		t.Errorf("APIError = %+v", ae)
	}
}

func TestHTTPClientUnauthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "unauthenticated", "message": "valid bearer authentication is required"},
		})
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	_, err := c.DoQuery(ctxBG(), "MATCH (n) RETURN 1", nil)
	if ae, ok := IsAPIError(err); !ok || ae.Status != 401 {
		t.Fatalf("expected 401 APIError, got %v", err)
	}
}

func TestHTTPClientPlain422Body(t *testing.T) {
	// Verified: body-deserialization failures are plain text with 422.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte("Failed to deserialize the JSON body into the target type: missing field `cell_id`"))
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	_, err := c.DoQuery(ctxBG(), "MATCH (n) RETURN 1", nil)
	ae, ok := IsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if ae.Status != 422 || ae.Code != "" || !strings.Contains(ae.Raw, "cell_id") {
		t.Errorf("422 APIError = %+v", ae)
	}
	if !strings.Contains(ae.Error(), "422") {
		t.Errorf("Error() should include status: %s", ae.Error())
	}
}

func TestHTTPClientContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	c.timeout = 200 * time.Millisecond
	ctx, cancel := contextWithTimeout(t, 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.DoQuery(ctx, "MATCH (n) RETURN 1", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("timeout took too long: %v", d)
	}
}

func TestHTTPClientUnreachable(t *testing.T) {
	// A server that is immediately closed = connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	c := newTestHTTPClient(srv)
	_, err := c.DoQuery(ctxBG(), "MATCH (n) RETURN 1", nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	if _, ok := IsAPIError(err); ok {
		t.Errorf("network errors are not API errors: %v", err)
	}
}

func TestHTTPClientMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := newTestHTTPClient(srv)
	_, err := c.DoQuery(ctxBG(), "MATCH (n) RETURN 1", nil)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestDecodeCellDirect(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{`{"type":"vertex_id","value":4611686018427387903}`, int64(4611686018427387903)},
		{`{"type":"integer","value":123}`, int64(123)},
		{`{"type":"signed_integer","value":-5}`, int64(-5)},
		{`{"type":"float","value":1.5}`, 1.5},
		{`{"type":"boolean","value":false}`, false},
		{`{"type":"string","value":"x"}`, "x"},
		{`{"type":"null"}`, nil},
		{`{"type":"list","value":[{"type":"integer","value":1},{"type":"string","value":"s"}]}`, []any{int64(1), "s"}},
		{`{"type":"path","value":{"nodes":[{"id":4611686018427387903}],"relationships":[]}}`, map[string]any{"nodes": []any{map[string]any{"id": int64(4611686018427387903)}}, "relationships": []any{}}},
	}
	for _, tc := range cases {
		got, err := decodeCell([]byte(tc.in))
		if err != nil {
			t.Errorf("decodeCell(%s): %v", tc.in, err)
			continue
		}
		if fmt.Sprintf("%T %#v", got, got) != fmt.Sprintf("%T %#v", tc.want, tc.want) {
			t.Errorf("decodeCell(%s) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// ctxBG is a background context (Go 1.22 has no t.Context()).
func ctxBG() context.Context { return context.Background() }

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), d)
}
