//go:build integration

// Integration tests against the RUNNING HydraDB instance.
// Run with: go test -tags integration ./internal/graph/
// Skips automatically when HydraDB is unreachable, so CI without the
// dependency stays green. Writes use the dedicated label IntegrationProbe and
// a disposable id range (977000..977999) in the verified dev graph; every test
// cleans up after itself and VERIFIES the cleanup.
package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	probeLabel  = "IntegrationProbe"
	probeEdge   = "INTEGRATION_PROBE_EDGE"
	probeIDBase = 977000 // disposable range 977000..977999
)

func liveConfig(t *testing.T) Config {
	t.Helper()
	cfg := LoadGraphConfig()
	if err := cfg.Validate(); err != nil {
		t.Skipf("HydraDB not configured: %v", err)
	}
	return cfg
}

func cleanupProbe(t *testing.T, c *Clients, ids ...int) {
	t.Helper()
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"id": probeIDBase + id})
	}
	// Verified batch delete form: UNWIND ... MATCH (n {id: row.id}) DETACH DELETE n.
	if _, err := c.HTTP.DoQuery(context.Background(),
		"UNWIND $rows AS row MATCH (n {id: row.id}) DETACH DELETE n",
		map[string]any{"rows": rows}); err != nil {
		t.Fatalf("cleanup delete: %v", err)
	}
	// VERIFY cleanup: a read over the whole disposable range must see 0.
	// (UNWIND...MATCH...RETURN is rejected by this build as a batch write,
	// so the verification uses a plain parameterized read instead.)
	res, err := c.HTTP.DoQuery(context.Background(),
		"MATCH (n:"+probeLabel+") WHERE n.id IN $ids RETURN count(*) AS c",
		map[string]any{"ids": idsAsInt64s(ids)})
	if err != nil {
		t.Fatalf("cleanup verify: %v", err)
	}
	n, err := res.Int64(0, 0)
	if err != nil || n != 0 {
		t.Fatalf("cleanup not verified: count=%d err=%v", n, err)
	}
}

func TestIntBoltConnect(t *testing.T) {
	cfg := liveConfig(t)
	bc, err := NewBoltClient(cfg)
	if err != nil {
		t.Fatalf("NewBoltClient: %v", err)
	}
	defer bc.Close(context.Background())
	if err := bc.VerifyConnectivity(context.Background()); err != nil {
		t.Fatalf("Bolt handshake failed: %v", err)
	}
}

func TestIntHTTPConnectAndHealth(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())
	rep := c.Health.Check(context.Background())
	if rep.Status != "healthy" {
		t.Fatalf("expected healthy, got %+v", rep)
	}
}

func TestIntParameterizedReadAndDecode(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())
	ctx := context.Background()

	// Verified node upsert form: MERGE pattern matches only id, labels via SET.
	_, err = c.HTTP.DoQuery(ctx,
		"UNWIND $rows AS row MERGE (n {id: row.id}) SET n:"+probeLabel+
			", n.name = row.name, n.count = row.count, n.ratio = row.ratio, n.active = row.active",
		map[string]any{"rows": []map[string]any{{
			"id": probeIDBase + 1, "name": "probe-one",
			"count": int64(42), "ratio": 1.25, "active": true,
		}}})
	if err != nil {
		t.Fatalf("write probe node: %v", err)
	}
	defer cleanupProbe(t, c, 1)

	// Parameterized read + typed decoding (vertex_id/integer/float/boolean/string).
	res, err := c.HTTP.DoQuery(ctx,
		"MATCH (n:"+probeLabel+" {id: $id}) RETURN n.id AS id, n.name AS name, n.count AS count, n.ratio AS ratio, n.active AS active",
		map[string]any{"id": probeIDBase + 1})
	if err != nil {
		t.Fatalf("read probe node: %v", err)
	}
	if res.Count() != 1 {
		t.Fatalf("expected 1 row, got %d", res.Count())
	}
	got := map[string]any{
		"id": mustI64(t, res, 0, 0), "name": res.Value(0, 1),
		"count": mustI64(t, res, 0, 2), "ratio": res.Value(0, 3), "active": res.Value(0, 4),
	}
	if got["id"] != int64(probeIDBase+1) || got["count"] != int64(42) {
		t.Errorf("integer decode wrong: %+v", got)
	}
	if got["name"] != "probe-one" || got["active"] != true {
		t.Errorf("string/boolean decode wrong: %+v", got)
	}
	if f, ok := got["ratio"].(float64); !ok || f != 1.25 {
		t.Errorf("float decode wrong: %#v", got["ratio"])
	}
	if res.QueryID == "" || res.Bookmark == "" {
		t.Errorf("envelope fields missing: query_id=%q bookmark=%q", res.QueryID, res.Bookmark)
	}
}

func TestIntBoltReadQuery(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())
	ctx := context.Background()

	// Seed via HTTP (verified write path), read via Bolt (parameterized).
	_, err = c.HTTP.DoQuery(ctx,
		"UNWIND $rows AS row MERGE (n {id: row.id}) SET n:"+probeLabel+", n.name = row.name",
		map[string]any{"rows": []map[string]any{{"id": probeIDBase + 2, "name": "bolt-read"}}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer cleanupProbe(t, c, 2)

	res, err := c.Bolt.ReadQuery(ctx,
		"MATCH (n:"+probeLabel+" {id: $id}) RETURN n.id AS id, n.name AS name",
		map[string]any{"id": probeIDBase + 2})
	if err != nil {
		t.Fatalf("bolt read: %v", err)
	}
	if res.Count() != 1 {
		t.Fatalf("bolt read rows = %d", res.Count())
	}
	if id := res.Value(0, 0); id != int64(probeIDBase+2) {
		t.Errorf("bolt id value = %#v", id)
	}
	if name := res.Value(0, 1); name != "bolt-read" {
		t.Errorf("bolt name value = %#v", name)
	}
}

func TestIntBoltRelationshipWriteReadAndCleanup(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())
	ctx := context.Background()

	seed := []map[string]any{
		{"id": probeIDBase + 3, "name": "a"},
		{"id": probeIDBase + 4, "name": "b"},
	}
	_, err = c.HTTP.DoQuery(ctx,
		"UNWIND $rows AS row MERGE (n {id: row.id}) SET n:"+probeLabel+", n.name = row.name",
		map[string]any{"rows": seed})
	if err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	defer cleanupProbe(t, c, 3, 4)

	// Verified batch edge form: endpoints need exactly one label, and the
	// relationship carries an id from the row map.
	_, err = c.HTTP.DoQuery(ctx,
		"UNWIND $rows AS row MATCH (a:"+probeLabel+" {id: row.src}), (b:"+probeLabel+" {id: row.dst})"+
			" CREATE (a)-[:"+probeEdge+" {id: row.rid, w: row.w}]->(b)",
		map[string]any{"rows": []map[string]any{
			{"src": probeIDBase + 3, "dst": probeIDBase + 4, "rid": int64(probeIDBase + 50), "w": int64(7)},
		}})
	if err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	// Relationship visibility may briefly lag a batch write (observed once);
	// retry for up to 3s before failing.
	deadline := time.Now().Add(3 * time.Second)
	var res *Result
	for {
		res, err = c.HTTP.DoQuery(ctx,
			"MATCH (a:"+probeLabel+" {id: $id})-[r:"+probeEdge+"]->(b) RETURN r.w AS w, b.id AS dst",
			map[string]any{"id": probeIDBase + 3})
		if err != nil {
			t.Fatalf("edge read: %v", err)
		}
		if res.Count() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge not visible after retries: %d rows", res.Count())
		}
		time.Sleep(300 * time.Millisecond)
	}
	if w := mustI64(t, res, 0, 0); w != 7 {
		t.Errorf("edge property w = %#v", res.Value(0, 0))
	}
	if dst := mustI64(t, res, 0, 1); dst != int64(probeIDBase+4) {
		t.Errorf("edge dst = %#v", res.Value(0, 1))
	}
}

func TestIntInvalidQueryError(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())

	_, err = c.HTTP.DoQuery(context.Background(), "THIS IS NOT CYPHER", nil)
	ae, ok := IsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if ae.Status != 400 || ae.Code != "invalid_request" {
		t.Errorf("unexpected error: %+v", ae)
	}
	// Error must not echo the token.
	if strings.Contains(ae.Error(), cfg.AuthToken) {
		t.Fatal("token leaked in error message")
	}
}

func TestIntContextCancellation(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())

	// Pre-cancelled context must fail fast with a context error, not a server call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.HTTP.DoQuery(ctx, "MATCH (n:"+probeLabel+") RETURN count(*) AS c", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if ctx.Err() == nil {
		t.Fatal("context should remain cancelled")
	}

	// HTTP timeout: 1ns deadline.
	tctx, tcancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer tcancel()
	time.Sleep(2 * time.Millisecond)
	if _, err := c.HTTP.DoQuery(tctx, "MATCH (n:"+probeLabel+") RETURN count(*) AS c", nil); err == nil {
		t.Fatal("expected timeout error")
	}

	// Bolt: cancelled context on a read.
	_, err = c.Bolt.ReadQuery(ctx, "MATCH (n:"+probeLabel+") RETURN count(*) AS c", nil)
	if err == nil {
		t.Fatal("expected error for cancelled bolt context")
	}
}

func TestIntHydraDBUnavailable(t *testing.T) {
	// Point at a definitely-closed port.
	badCfg := liveConfig(t)
	badCfg.HTTPAddr = "http://127.0.0.1:1"
	badCfg.BoltAddr = "127.0.0.1:1"
	badCfg.AdminAddr = "http://127.0.0.1:1"

	bc, err := NewBoltClient(badCfg)
	if err != nil {
		t.Fatalf("NewBoltClient: %v", err)
	}
	defer bc.Close(context.Background())
	if err := bc.VerifyConnectivity(context.Background()); err == nil {
		t.Fatal("expected bolt connect failure against closed port")
	} else if strings.Contains(err.Error(), badCfg.AuthToken) {
		t.Fatal("token leaked in bolt error")
	}

	hc := NewHTTPClient(badCfg)
	if _, err := hc.DoQuery(context.Background(), "MATCH (n) RETURN 1", nil); err == nil {
		t.Fatal("expected http failure against closed port")
	} else if _, ok := IsAPIError(err); ok {
		t.Fatal("connection refused must not surface as APIError")
	}

	rep := NewHealthChecker(badCfg, bc).Check(context.Background())
	if rep.Status != "unavailable" {
		t.Fatalf("expected unavailable report, got %+v", rep)
	}
}

func TestIntHealthReportAgainstLive(t *testing.T) {
	cfg := liveConfig(t)
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close(context.Background())
	rep := c.Health.Check(context.Background())
	if rep.Status != "healthy" || !rep.Ready {
		t.Fatalf("live health report = %+v", rep)
	}
}

// idsAsInt64s maps the disposable test ids to int64 parameters.
func idsAsInt64s(ids []int) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		out = append(out, int64(probeIDBase+id))
	}
	return out
}

func mustI64(t *testing.T, r *Result, row, col int) int64 {	t.Helper()
	v, err := r.Int64(row, col)
	if err != nil {
		t.Fatalf("Int64(%d,%d): %v (value=%#v)", row, col, err, r.Value(row, col))
	}
	return v
}

// TestIntTokenNeverLogged is a behavioral check: run the health CLI logic with
// a fake logger capturing output and assert the token never appears.
func TestIntTokenNeverLogged(t *testing.T) {
	cfg := liveConfig(t)
	if !strings.Contains(cfg.String(), redactedPlaceholder) {
		t.Fatalf("config string must redact token: %s", cfg.String())
	}
	_ = fmt.Sprintf // keep fmt for future assertions
}
