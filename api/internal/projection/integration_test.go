//go:build integration

package projection

import (
	"context"
	"os"
	"testing"
	"time"

	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
)

func TestIntegrationRealVerticalSlice(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	graphCfg := graph.LoadGraphConfig()
	if err := graphCfg.Validate(); err != nil {
		t.Skipf("HydraDB not configured: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tender, bids, vendor, award, err := (SourceAdapter{Pool: pool}).LoadVerticalSlice(ctx, 16234)
	if err != nil {
		t.Fatal(err)
	}
	if len(bids) < 2 || vendor == nil || award == nil {
		t.Fatalf("vertical slice incomplete: bids=%d vendor=%v award=%v", len(bids), vendor != nil, award != nil)
	}
	batch, err := BuildVerticalSlice(tender, bids, vendor, award, 1770000000000)
	if err != nil {
		t.Fatal(err)
	}

	clients, err := graph.Open(graphCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())
	writer := HydraWriter{Client: clients.HTTP}
	if err := writer.EnsureAbsent(ctx, batch.Nodes); err != nil {
		t.Skipf("safe vertical-slice isolation unavailable: %v", err)
	}
	written := false
	defer func() {
		if written {
			if err := writer.DeleteAndVerify(context.Background(), batch.Nodes); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}()

	if _, err := writer.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	written = true
	verifyVerticalSlice(t, ctx, clients.HTTP, batch, bids, award)

	// Replay uses a second set of HTTP mutation requests, not one Cypher
	// statement, so deterministic relationship MERGE behavior is exercised.
	if _, err := writer.Write(ctx, batch); err != nil {
		t.Fatalf("replay: %v", err)
	}
	verifyVerticalSlice(t, ctx, clients.HTTP, batch, bids, award)

	// Actual collision behavior: create one disposable node, then ask the
	// writer to validate a different GID at its id.
	existing, err := NewNode("Phase2CollisionProbe", "COLLISION:phase2:existing", "phase2_test", "existing", 1770000000000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.EnsureAbsent(ctx, []Node{existing}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, Batch{Nodes: []Node{existing}}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.DeleteAndVerify(context.Background(), []Node{existing}); err != nil {
			t.Errorf("collision probe cleanup: %v", err)
		}
	}()
	incoming := existing
	incoming.GID = "COLLISION:phase2:incoming"
	incoming.Props = cloneMap(existing.Props)
	incoming.Props["gid"] = incoming.GID
	if err := writer.CheckCollisions(ctx, []Node{incoming}); err == nil {
		t.Fatal("expected live collision check to fail")
	}
}

func verifyVerticalSlice(t *testing.T, ctx context.Context, client *graph.HTTPClient, batch Batch, bids []BidRecord, award *AwardRecord) {
	t.Helper()
	for _, n := range batch.Nodes {
		res, err := client.DoQuery(ctx, "MATCH (n {id: $id}) RETURN n.gid AS gid, n.src_table AS src_table, n.src_pk AS src_pk", map[string]any{"id": n.ID})
		if err != nil {
			t.Fatalf("read node %s: %v", n.GID, err)
		}
		if res.Count() != 1 {
			t.Fatalf("node %s rows=%d", n.GID, res.Count())
		}
		gid, _ := res.String(0, 0)
		if gid != n.GID {
			t.Fatalf("node gid=%q want %q", gid, n.GID)
		}
	}
	obs := 0
	for _, n := range batch.Nodes {
		if n.Label == "CoBidObservation" {
			obs++
			res, err := client.DoQuery(ctx, "MATCH (o:CoBidObservation {id: $id})<-[:CO_BID_IN]-(b:Bid) RETURN count(*) AS c", map[string]any{"id": n.ID})
			if err != nil {
				t.Fatal(err)
			}
			count, err := res.Int64(0, 0)
			if err != nil || count != 2 {
				t.Fatalf("observation edges=%d err=%v", count, err)
			}
		}
	}
	if obs == 0 {
		t.Fatal("no co-bid observation")
	}
	if award != nil {
		for _, b := range bids {
			if e, ok := WinnerEvidenceFor(b, *award); ok {
				res, err := client.DoQuery(ctx, "MATCH (b:Bid {id: $bid})-[r:WON]->(a:Award {id: $award}) RETURN r.evidence_kind AS evidence", map[string]any{"bid": NumericID(GID("BID", "v", FormatID(b.ID))), "award": NumericID(GID("AWARD", "v", FormatID(award.ID)))})
				if err != nil {
					t.Fatal(err)
				}
				if res.Count() != 1 {
					t.Fatalf("winner relationship rows=%d", res.Count())
				}
				got, _ := res.String(0, 0)
				if got != e.Kind {
					t.Fatalf("winner evidence=%q want %q", got, e.Kind)
				}
			}
		}
	}
}
