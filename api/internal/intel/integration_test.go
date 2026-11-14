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
