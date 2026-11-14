//go:build integration

package intel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/projection"
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

func TestLiveAnalystDecisionLifecycleUsesControlPlaneOnly(t *testing.T) {
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

	candidateGID := "TEST:CANDIDATE:PHASE7:" + time.Now().UTC().Format("20060102150405.000000000")
	candidateID := projection.NumericID(candidateGID)
	_, err = pool.Exec(ctx, `INSERT INTO tg_relationship_candidates
 (candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version)
 VALUES ($1,$2,'VENDOR_CORPORATE_ENTITY','Vendor','test-vendor','VENDOR:cp:test-vendor','CorporateEntity','test-entity','ENTITY:v:test-entity','TEST_FIXTURE','[{"source_table":"test_fixture","source_pk":"1"}]','[{"source_table":"test_fixture","source_pk":"1"}]','test')`, candidateID, candidateGID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		conn, acquireErr := pool.Acquire(context.Background())
		if acquireErr == nil {
			defer conn.Release()
			_, _ = conn.Exec(context.Background(), `SELECT set_config('tg_history_cleanup','on',false)`)
			_, _ = conn.Exec(context.Background(), `DELETE FROM tg_graph_projection_outbox WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_decision_history WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_decisions WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_candidates WHERE candidate_id=$1`, candidateID)
		}
	}()

	store := ReviewStore{Pool: pool}
	version0 := 0
	first, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "CONFIRMED", Rationale: "test fixture only; not an analyst finding", ActorID: "operator:integration-test", ActorKind: "OPERATOR_SUPPLIED", EvidenceRefs: json.RawMessage(`[{"source_table":"test_fixture","source_pk":"1"}]`), ExpectedVersion: &version0, RequestID: "phase7-test-1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.DecisionVersion != 1 || first.DesiredOperation != "UPSERT_CONFIRMED_RELATIONSHIP" {
		t.Fatalf("first decision=%+v", first)
	}
	idempotent, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "CONFIRMED", Rationale: "retry", ActorID: "operator:integration-test", ActorKind: "OPERATOR_SUPPLIED", RequestID: "phase7-test-1"})
	if err != nil || !idempotent.Idempotent {
		t.Fatalf("idempotent retry=%+v err=%v", idempotent, err)
	}

	version1 := 1
	if _, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "NEEDS_REVIEW", Rationale: "reopen for test", ActorID: "operator:integration-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version1, RequestID: "phase7-test-2"}); err != nil {
		t.Fatal(err)
	}
	version2 := 2
	last, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "REJECTED", Rationale: "test fixture only; not an analyst finding", ActorID: "operator:integration-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version2, RequestID: "phase7-test-3"})
	if err != nil {
		t.Fatal(err)
	}
	if last.DecisionVersion != 3 || last.DesiredOperation != "NO_CONFIRMED_RELATIONSHIP" {
		t.Fatalf("last decision=%+v", last)
	}
	if _, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "CONFIRMED", Rationale: "stale version", ActorID: "operator:integration-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version0, RequestID: "phase7-test-stale"}); err == nil {
		t.Fatal("expected optimistic concurrency conflict")
	}
	history, err := store.History(ctx, candidateID)
	if err != nil || len(history) != 3 {
		t.Fatalf("history len=%d err=%v", len(history), err)
	}
	var projectionCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM tg_graph_projection_outbox WHERE candidate_id=$1`, candidateID).Scan(&projectionCount); err != nil {
		t.Fatal(err)
	}
	if projectionCount != 3 {
		t.Fatalf("projection count=%d", projectionCount)
	}
}

func TestLiveAnalystProjectionOutboxAgainstDisposableGraph(t *testing.T) {
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

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	vendorGID := "VENDOR:cp:phase75-" + suffix
	entityGID := "ENTITY:v:phase75-" + suffix
	vendor, err := projection.NewNode("Vendor", vendorGID, "phase75_test", "vendor-"+suffix, time.Now().UnixMilli(), map[string]any{"business_name": "Phase 7.5 test vendor"})
	if err != nil {
		t.Fatal(err)
	}
	entity, err := projection.NewNode("CorporateEntity", entityGID, "phase75_test", "entity-"+suffix, time.Now().UnixMilli(), map[string]any{"name": "Phase 7.5 test entity"})
	if err != nil {
		t.Fatal(err)
	}
	writer := projection.HydraWriter{Client: clients.HTTP, BatchSize: 10}
	if err := writer.EnsureAbsent(ctx, []projection.Node{vendor, entity}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, projection.Batch{Nodes: []projection.Node{vendor, entity}}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.DeleteAndVerify(context.Background(), []projection.Node{vendor, entity}); err != nil {
			t.Errorf("graph cleanup: %v", err)
		}
	}()

	candidateGID := CandidateGID("VENDOR_CORPORATE_ENTITY", vendorGID, entityGID)
	candidateID := CandidateID(candidateGID)
	_, err = pool.Exec(ctx, `INSERT INTO tg_relationship_candidates
 (candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version)
 VALUES ($1,$2,'VENDOR_CORPORATE_ENTITY','Vendor',$3,$4,'CorporateEntity',$5,$6,'PHASE75_TEST','[{"source_table":"phase75_test","source_pk":"1"}]','[{"source_table":"phase75_test","source_pk":"1"}]','phase75-test')`, candidateID, candidateGID, "vendor-"+suffix, vendorGID, "entity-"+suffix, entityGID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		conn, acquireErr := pool.Acquire(context.Background())
		if acquireErr != nil {
			return
		}
		defer conn.Release()
		_, _ = conn.Exec(context.Background(), `SELECT set_config('tg_history_cleanup','on',false)`)
		_, _ = conn.Exec(context.Background(), `DELETE FROM tg_graph_projection_outbox WHERE candidate_id=$1`, candidateID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_decision_history WHERE candidate_id=$1`, candidateID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_decisions WHERE candidate_id=$1`, candidateID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM tg_relationship_candidates WHERE candidate_id=$1`, candidateID)
	}()

	version0 := 0
	store := ReviewStore{Pool: pool}
	if _, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "CONFIRMED", Rationale: "disposable end-to-end fixture", ActorID: "operator:phase75-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version0, RequestID: "phase75-e2e-1"}); err != nil {
		t.Fatal(err)
	}
	consumer := ProjectionConsumer{Pool: pool, Graph: clients.HTTP}
	result, err := consumer.Apply(ctx, 10, false)
	if err != nil || result.Applied != 1 || result.Failed != 0 {
		t.Fatalf("confirmed projection result=%+v err=%v", result, err)
	}
	assertAnalystEdge(t, ctx, clients.HTTP, vendor, entity, 1)

	version1 := 1
	if _, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "NEEDS_REVIEW", Rationale: "reopened for disposable test", ActorID: "operator:phase75-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version1, RequestID: "phase75-e2e-2"}); err != nil {
		t.Fatal(err)
	}
	result, err = consumer.Apply(ctx, 10, false)
	if err != nil || result.Applied != 1 {
		t.Fatalf("needs-review projection result=%+v err=%v", result, err)
	}
	assertAnalystEdge(t, ctx, clients.HTTP, vendor, entity, 0)

	version2 := 2
	if _, err := store.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "REJECTED", Rationale: "disposable test rejection", ActorID: "operator:phase75-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version2, RequestID: "phase75-e2e-3"}); err != nil {
		t.Fatal(err)
	}
	result, err = consumer.Apply(ctx, 10, false)
	if err != nil || result.Applied != 1 {
		t.Fatalf("rejection projection result=%+v err=%v", result, err)
	}
	assertAnalystEdge(t, ctx, clients.HTTP, vendor, entity, 0)
}

func TestLiveDecisionHistoryIsDatabaseAppendOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var candidateID int64
	if err := tx.QueryRow(ctx, `SELECT candidate_id FROM tg_relationship_candidates ORDER BY candidate_id LIMIT 1`).Scan(&candidateID); err != nil {
		t.Fatal(err)
	}
	var historyID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tg_relationship_decision_history(candidate_id,new_status,actor_id,actor_kind,rationale,decision_version,request_id) VALUES ($1,'NEEDS_REVIEW','operator:append-only-test','OPERATOR_SUPPLIED','rollback-only trigger test',900000,'phase75-history-trigger-test') RETURNING history_id`, candidateID).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_relationship_decision_history SET rationale='must fail' WHERE history_id=$1`, historyID); err == nil {
		t.Fatal("expected database trigger to reject history update")
	}
}

func assertAnalystEdge(t *testing.T, ctx context.Context, client *graph.HTTPClient, vendor, entity projection.Node, expected int64) {
	t.Helper()
	res, err := client.DoQuery(ctx, `MATCH (a:Vendor {id: $src})-[:IDENTIFIED_AS]->(b:CorporateEntity {id: $dst}) RETURN count(*) AS c`, map[string]any{"src": vendor.ID, "dst": entity.ID})
	if err != nil {
		t.Fatal(err)
	}
	count, err := res.Int64(0, 0)
	if err != nil || count != expected {
		t.Fatalf("analyst edge count=%d want=%d err=%v", count, expected, err)
	}
}
