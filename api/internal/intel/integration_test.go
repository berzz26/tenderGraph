//go:build integration

package intel

import (
	"context"
	"testing"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
)

func TestLiveInvestigationsAgainstPostgresAndHydraDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	config := cfg.Load()
	if config.DatabaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	clients, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())
	svc := Service{Pool: pool, Graph: clients.HTTP}

	profile, err := svc.Profile(ctx, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DistinctTenders == 0 || len(profile.History) == 0 {
		t.Fatalf("profile=%+v", profile)
	}
	pair, err := svc.Pair(ctx, 1, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if pair.SharedTenderCount != 2 || pair.JaccardSimilarity != 0.4 {
		t.Fatalf("pair=%+v", pair)
	}
	tender, err := svc.Tender(ctx, 16219, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tender.Participants) != 5 || tender.CoBidObservations != 10 {
		t.Fatalf("tender=%+v", tender)
	}
	network, err := svc.Network(ctx, 3223, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.Nodes) == 0 {
		t.Fatalf("network=%+v", network)
	}
}

func TestLiveCorporateIntelligenceAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	config := cfg.Load()
	if config.DatabaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var entityID string
	err = pool.QueryRow(ctx, `SELECT entity_id::text FROM entity_financials ORDER BY entity_id LIMIT 1`).Scan(&entityID)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Pool: pool}
	company, err := svc.Company(ctx, entityID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if company.Entity.EntityID != entityID || len(company.Financials) == 0 {
		t.Fatalf("company report missing entity or financial evidence: %+v", company)
	}
	if company.LatestMarket == nil {
		t.Fatal("expected a current market snapshot for the selected live entity")
	}

	var awardID int64
	err = pool.QueryRow(ctx, `SELECT id::bigint FROM tender_extracted_meta WHERE NULLIF(BTRIM(awardee_name), '') IS NOT NULL ORDER BY id LIMIT 1`).Scan(&awardID)
	if err != nil {
		t.Fatal(err)
	}
	award, err := svc.Award(ctx, awardID)
	if err != nil {
		t.Fatal(err)
	}
	if award.AwardID != awardID || award.TenderID == 0 || award.AwardGID == "" || award.TenderGID == "" {
		t.Fatalf("award report missing provenance: %+v", award)
	}
}

func TestLiveNativeHydraAlgorithms(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	clients, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(context.Background())
	svc := Service{Graph: clients.HTTP}

	degree, err := svc.NativeDegree(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(degree.Rows) == 0 || degree.Rows[0].BidGID == "" {
		t.Fatalf("degree=%+v", degree)
	}

	paths, err := svc.NativeShortestPaths(ctx, 3223, 3222, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths.Paths) == 0 || paths.SourceBidID != 3223 || paths.TargetBidID == nil || *paths.TargetBidID != 3222 {
		t.Fatalf("paths=%+v", paths)
	}

	connections, err := svc.NativeConnections(ctx, 3223, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.Paths) == 0 || connections.ResultLimit != 3 {
		t.Fatalf("connections=%+v", connections)
	}

	// MSpaths uses indexed string selectors and is intentionally exercised
	// directly here; its result rows are raw bounded path evidence.
	res, err := clients.HTTP.DoQuery(ctx, `CALL algo.MSpaths({sourceLabel: 'Bid', sourceProperty: 'gid', sourceValues: ['BID:v:3223', 'BID:v:3222'], targetValues: ['BID:v:3223', 'BID:v:3222'], relTypes: ['CO_BID_IN'], relDirection: 'both', maxLen: 2, pathCount: 1, pairwise: false, resultLimit: 10}) YIELD path RETURN path`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Fatal("MSpaths returned no path for a known shared tender pair")
	}
}
