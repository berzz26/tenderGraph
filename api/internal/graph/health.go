package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Prober is the Bolt-side probe used by the health checker (implemented by
// *BoltClient; interface here keeps health testable without a live driver).
type Prober interface {
	VerifyConnectivity(ctx context.Context) error
	ReadQuery(ctx context.Context, cypher string, params map[string]any) (*Result, error)
}

// HealthChecker distinguishes "reachable" (TCP/HTTP answers) from "ready"
// (HydraDB reports readiness AND Bolt actually handshakes AND a real query
// executes). A reachable port alone is never sufficient.
type HealthChecker struct {
	adminURL    string // e.g. http://127.0.0.1:19091
	httpBase    string // e.g. http://127.0.0.1:8443
	token       string
	namespace   string
	timeout     time.Duration
	client      *http.Client
	bolt        Prober
	boltHealthy bool // bolt probe failed earlier but bolt is configured
}

// NewHealthChecker builds the checker. bolt may be nil (Bolt component then
// reports as skipped/unavailable rather than healthy).
func NewHealthChecker(cfg Config, bolt Prober) *HealthChecker {
	timeout := time.Duration(cfg.HealthTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HealthChecker{
		adminURL:  strings.TrimSuffix(cfg.AdminAddr, "/"),
		httpBase:  strings.TrimSuffix(cfg.HTTPAddr, "/"),
		token:     cfg.AuthToken,
		namespace: cfg.Namespace,
		timeout:   timeout,
		client:    &http.Client{Timeout: timeout},
		bolt:      bolt,
	}
}

// Component is one dependency probe result.
type Component struct {
	Name      string        `json:"name"`
	OK        bool          `json:"ok"`
	Reachable bool          `json:"reachable"` // transport-level: something answered
	Detail    string        `json:"detail,omitempty"`
	Elapsed   time.Duration `json:"-"`
}

// Report is the aggregate health result.
type Report struct {
	Status     string      `json:"status"` // "healthy" | "degraded" | "unavailable"
	Ready      bool        `json:"ready"`
	Components []Component `json:"components"`
}

// Check runs all probes. It never panics on unreachable HydraDB and never
// makes callers block longer than the configured timeout per probe.
func (h *HealthChecker) Check(ctx context.Context) Report {
	rep := Report{}

	// 1. Admin readiness (/readyz): 200 = the node reports itself ready.
	adminReady := h.probe(ctx, "admin_ready", func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.adminURL+"/readyz", nil)
		if err != nil {
			return 0, err
		}
		resp, err := h.client.Do(req)
		if err != nil {
			return 0, err
		}
		defer drainClose(resp.Body)
		return resp.StatusCode, nil
	})
	adminReady.OK = adminReady.OK && adminReady.Detail == ""
	rep.Components = append(rep.Components, adminReady)

	// 2. Admin metrics (/metrics): reachable + scrapeable.
	metrics := h.probe(ctx, "admin_metrics", func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.adminURL+"/metrics", nil)
		if err != nil {
			return 0, err
		}
		resp, err := h.client.Do(req)
		if err != nil {
			return 0, err
		}
		defer drainClose(resp.Body)
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return 0, err
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, fmt.Errorf("metrics http %d", resp.StatusCode)
		}
		if !strings.Contains(string(b), "graph_") {
			return resp.StatusCode, fmt.Errorf("metrics body does not look like HydraDB metrics")
		}
		return resp.StatusCode, nil
	})
	metrics.OK = metrics.OK && metrics.Detail == ""
	rep.Components = append(rep.Components, metrics)

	// 3. Query API healthz (GET only, verified).
	healthz := h.probe(ctx, "http_healthz", func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.httpBase+"/healthz", nil)
		if err != nil {
			return 0, err
		}
		resp, err := h.client.Do(req)
		if err != nil {
			return 0, err
		}
		defer drainClose(resp.Body)
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil && resp.StatusCode == http.StatusOK {
			return resp.StatusCode, fmt.Errorf("healthz body not ok-json: %w", err)
		}
		if resp.StatusCode == http.StatusOK && body.Status != "ok" {
			return resp.StatusCode, fmt.Errorf("healthz status %q", body.Status)
		}
		return resp.StatusCode, nil
	})
	healthz.OK = healthz.OK && healthz.Detail == ""
	rep.Components = append(rep.Components, healthz)

	// 4. Bolt: handshake + a real parameterized read.
	boltComp := Component{Name: "bolt_query"}
	if h.bolt == nil {
		boltComp.Detail = "bolt client not configured"
	} else {
		start := time.Now()
		if err := h.bolt.VerifyConnectivity(ctx); err != nil {
			boltComp.Detail = truncate(err.Error(), 200)
		} else {
			res, err := h.bolt.ReadQuery(ctx, "MATCH (n:GraphHealthProbe) RETURN count(*) AS c",
				map[string]any{})
			if err != nil {
				boltComp.Detail = truncate(err.Error(), 200)
			} else if _, err := res.Int64(0, 0); err != nil {
				boltComp.Detail = truncate("count result undecodable: "+err.Error(), 200)
			} else {
				boltComp.OK = true
			}
		}
		boltComp.Elapsed = time.Since(start)
		// Transport answered (auth/protocol/query errors) vs connection-level
		// failure (refused, timeout, DNS): only the former counts as reachable.
		boltComp.Reachable = boltComp.OK || boltWasReachable(boltComp.Detail)
	}
	rep.Components = append(rep.Components, boltComp)

	// Aggregate: healthy = all four ok; degraded = reachable but not fully
	// ready; unavailable = nothing answered.
	allOK := true
	anyReachable := false
	for _, c := range rep.Components {
		if !c.OK {
			allOK = false
		}
		if c.Reachable {
			anyReachable = true
		}
	}
	switch {
	case allOK && len(rep.Components) > 0:
		rep.Status = "healthy"
		rep.Ready = true
	case anyReachable:
		rep.Status = "degraded"
		rep.Ready = adminReady.OK && healthz.OK && boltComp.OK
	default:
		rep.Status = "unavailable"
		rep.Ready = false
	}
	return rep
}

// probe runs fn and produces a Component: Reachable=true when the transport
// answered (even with a non-OK status), OK=true only for status 200 with no
// error detail.
func (h *HealthChecker) probe(ctx context.Context, name string, fn func(ctx context.Context) (int, error)) Component {
	c := Component{Name: name}
	if ctx == nil {
		ctx = context.Background()
	}
	pctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	start := time.Now()
	status, err := fn(pctx)
	c.Elapsed = time.Since(start)
	if err != nil {
		c.Reachable = status != 0 // transport answered but body/semantics failed
		c.Detail = truncate(err.Error(), 200)
		return c
	}
	c.Reachable = true
	if status != http.StatusOK {
		c.Detail = fmt.Sprintf("http %d", status)
		return c
	}
	c.OK = true
	return c
}

// boltWasReachable: the bolt probe detail text is a wrapped error; a
// connection-level refusal means unreachable, anything else (auth, protocol,
// query errors) still means the port answered.
func boltWasReachable(detail string) bool {
	if detail == "" {
		return true
	}
	low := strings.ToLower(detail)
	for _, marker := range []string{
		"connection refused", "no such host", "i/o timeout",
		"network is unreachable", "connection reset by peer", "tls:",
	} {
		if strings.Contains(low, marker) {
			return false
		}
	}
	return true
}

func isConnectivityError(detail string) bool { return !boltWasReachable(detail) }
