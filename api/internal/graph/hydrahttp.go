package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient talks to the verified HydraDB HTTP query API.
//
// Verified contract (live instance 0.1.0+916637477f3f and
// /home/berzz/hydradb/src/client/http.rs):
//
//	POST /v1/graphs/{graph}/query
//	body: {"cell_id" (required), "query", "parameters"?, "bookmark"?,
//	       "timeout_ms"?, "page_size"?, "cursor"?, "consistency"?}
//	  - page_size/cursor are BODY fields, not URL query parameters.
//	  - continuation: same body + "query_id" + "cursor" (= next_cursor).
//	response 200: {"query_id","columns","rows","read_epoch","next_cursor",
//	               "bookmark","statuses"?} — cells are typed envelopes.
//	errors: 400/401/403/500 carry {"error":{"code","message"}};
//	        422 (body deserialization) is a PLAIN TEXT body.
//
// Writes in this build are only accepted in the UNWIND batch mutation form;
// single-shot write patterns are rejected by the server (verified).
type HTTPClient struct {
	base      string // e.g. http://127.0.0.1:8443
	graph     string // graph id (database)
	namespace string
	cellID    string
	token     string
	timeout   time.Duration
	client    *http.Client
}

// NewHTTPClient builds an HTTP query client. httpAddr is the base URL.
func NewHTTPClient(cfg Config) *HTTPClient {
	timeout := time.Duration(cfg.QueryTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPClient{
		base:      strings.TrimSuffix(cfg.HTTPAddr, "/"),
		graph:     cfg.Database,
		namespace: cfg.Namespace,
		cellID:    cfg.CellID,
		token:     cfg.AuthToken,
		timeout:   timeout,
		client:    &http.Client{Timeout: timeout},
	}
}

// namespaceHeader is the verified scope header (crates/protocol/src/http.rs:
// GRAPH_NAMESPACE_HEADER = "x-graph-namespace").
const namespaceHeader = "X-Graph-Namespace"

// queryRequest is the request body. omitempty keeps optional fields off the
// wire (the server treats them as serde defaults).
type queryRequest struct {
	QueryID     string         `json:"query_id,omitempty"`
	CellID      string         `json:"cell_id"`
	Query       string         `json:"query"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Bookmark    string         `json:"bookmark,omitempty"`
	TimeoutMS   int            `json:"timeout_ms,omitempty"`
	PageSize    int            `json:"page_size,omitempty"`
	Cursor      *uint64        `json:"cursor,omitempty"`
	Consistency string         `json:"consistency,omitempty"`
}

// rawResponse is the wire envelope before cell decoding.
type rawResponse struct {
	QueryID    string              `json:"query_id"`
	Columns    []string            `json:"columns"`
	Rows       [][]json.RawMessage `json:"rows"`
	ReadEpoch  *uint64             `json:"read_epoch"`
	NextCursor *uint64             `json:"next_cursor"`
	Bookmark   string              `json:"bookmark"`
}

// DoQuery executes one query and decodes the verified envelope.
// ctx controls cancellation/deadline; it is honored on top of the client
// timeout (whichever fires first).
func (c *HTTPClient) DoQuery(ctx context.Context, query string, params map[string]any) (*Result, error) {
	return c.doQuery(ctx, queryRequest{CellID: c.cellID, Query: query, Parameters: params, PageSize: 4096})
}

// QueryPage fetches the next page of a previous read. Verified continuation
// contract: the SAME body is re-sent with query_id and cursor (= next_cursor);
// the query text is still required by the prepare path (verified in
// src/client/service.rs — cursor continuation still runs query preparation).
func (c *HTTPClient) QueryPage(ctx context.Context, query string, params map[string]any, queryID string, cursor uint64) (*Result, error) {
	return c.doQuery(ctx, queryRequest{
		QueryID:    queryID,
		CellID:     c.cellID,
		Query:      query,
		Parameters: params,
		PageSize:   4096,
		Cursor:     &cursor,
	})
}

func (c *HTTPClient) doQuery(ctx context.Context, req queryRequest) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Per-request deadline on top of ctx.
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode query request: %w", err)
	}
	url := fmt.Sprintf("%s/v1/graphs/%s/query", c.base, c.graph)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build query request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.namespace != "" {
		httpReq.Header.Set(namespaceHeader, c.namespace)
	}
	// Bearer token never logged; it lives only in the header value.
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, wrapNet(ctx, err, "POST %s", redactURL(url))
	}
	defer drainClose(resp.Body)

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read query response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, decodeHTTPError(resp.StatusCode, raw)
	}

	var env rawResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode query response (http %d): %w", resp.StatusCode, err)
	}
	out := &Result{
		QueryID:    env.QueryID,
		Columns:    env.Columns,
		Rows:       make([][]any, 0, len(env.Rows)),
		ReadEpoch:  env.ReadEpoch,
		NextCursor: env.NextCursor,
		Bookmark:   env.Bookmark,
	}
	for _, row := range env.Rows {
		if len(row) != len(env.Columns) {
			return nil, fmt.Errorf("decode query response: row has %d cells, expected %d", len(row), len(env.Columns))
		}
		vals := make([]any, 0, len(row))
		for _, cell := range row {
			v, err := decodeCell(cell)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		out.Rows = append(out.Rows, vals)
	}
	return out, nil
}

const maxBodyBytes = 64 << 20 // 64 MiB guard

// APIError is a structured HydraDB error (verified envelope).
type APIError struct {
	Status  int    // HTTP status
	Code    string // error code, e.g. invalid_request, unauthenticated, permission_denied
	Message string
	Raw     string // raw body (may be plain text on 422); token-free
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hydradb http %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("hydradb http %d: %s", e.Status, truncate(e.Raw, 300))
}

// IsAPIError reports whether err is a structured HydraDB API error.
func IsAPIError(err error) (*APIError, bool) {
	ae, ok := err.(*APIError)
	return ae, ok
}

// decodeHTTPError converts a non-200 response into an APIError. The verified
// 422 path returns a plain-text body, so both shapes are handled.
func decodeHTTPError(status int, body []byte) error {
	trimmed := bytes.TrimSpace(body)
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &env); err == nil && env.Error.Message != "" {
			return &APIError{Status: status, Code: env.Error.Code, Message: env.Error.Message, Raw: string(trimmed)}
		}
	}
	return &APIError{Status: status, Raw: string(trimmed)}
}

// wrapNet classifies network-level failures with useful context.
func wrapNet(ctx context.Context, err error, format string, args ...any) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ctxErr)
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	_ = body.Close()
}
