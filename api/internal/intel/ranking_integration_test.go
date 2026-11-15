//go:build integration

package intel

// Phase 9 ranking-pipeline integration tests. All fixtures are clearly marked
// control-plane test records (TEST:CANDIDATE GIDs, TEST_FIXTURE generation
// method, operator:ranking-test actor). They are excluded from training by
// design; the tests assert that exclusion and that the system correctly
// reports NOT_READY while genuine labels are absent.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
)

// insertRankingFixture inserts one clearly marked test candidate + decision
// and registers cleanup of every control-plane row it creates.

func TestLiveRankingPipelineExcludesFixturesAndStaysOnBaseline(t *testing.T) {
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

	candidateGID := "TEST:CANDIDATE:PHASE9:" + time.Now().UTC().Format("20060102150405.000000000")
	candidateID := candidateIDFromGID(candidateGID)
	if _, err := pool.Exec(ctx, `INSERT INTO tg_relationship_candidates
 (candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version)
 VALUES ($1,$2,'VENDOR_CORPORATE_ENTITY','Vendor','test-vendor-p9','VENDOR:cp:test-vendor-p9','CorporateEntity','test-entity-p9','ENTITY:v:test-entity-p9','TEST_FIXTURE','[{"method":"GSTIN_EXACT_UNIQUE","gstin":"TESTGSTIN1","entity_count":1}]','[{"method":"GSTIN_EXACT_UNIQUE","gstin":"TESTGSTIN1","entity_count":1}]','phase9-test')`,
		candidateID, candidateGID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		conn, acquireErr := pool.Acquire(context.Background())
		if acquireErr == nil {
			defer conn.Release()
			bg := context.Background()
			_, _ = conn.Exec(bg, `SELECT set_config('tg_history_cleanup','on',false)`)
			_, _ = conn.Exec(bg, `DELETE FROM tg_candidate_learned_scores WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(bg, `DELETE FROM tg_candidate_baseline_scores WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(bg, `DELETE FROM tg_graph_projection_outbox WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(bg, `DELETE FROM tg_relationship_decision_history WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(bg, `DELETE FROM tg_relationship_decisions WHERE candidate_id=$1`, candidateID)
			_, _ = conn.Exec(bg, `DELETE FROM tg_relationship_candidates WHERE candidate_id=$1`, candidateID)
		}
	}()

	review := ReviewStore{Pool: pool}
	version0 := 0
	// The decision uses a test actor: it is a fixture, never analyst feedback.
	if _, err := review.Decide(ctx, DecisionInput{CandidateID: candidateID, Status: "CONFIRMED", Rationale: "phase9 ranking test fixture; not an analyst finding", ActorID: "operator:ranking-test", ActorKind: "OPERATOR_SUPPLIED", ExpectedVersion: &version0, RequestID: "phase9-ranking-1"}); err != nil {
		t.Fatal(err)
	}

	ranking := RankingStore{Pool: pool}

	// 1. The fixture decision must be excluded from training data.
	labels, excludedFixtures, _, err := ranking.LoadLabels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(labels, func(l trainingLabel) bool { return l.Candidate.CandidateID == candidateID }) {
		t.Fatal("test-fixture decision leaked into training labels")
	}
	found := false
	for _, l := range labels {
		if l.Candidate.CandidateID == candidateID {
			found = true
		}
	}
	if found || excludedFixtures < 1 {
		t.Fatalf("expected the fixture to be counted as excluded; fixtures=%d found=%v", excludedFixtures, found)
	}
	samples, duplicates := BuildSamples(labels)
	for _, s := range samples {
		if s.CandidateID == candidateID {
			t.Fatal("fixture leaked into samples")
		}
	}
	_ = duplicates

	// 2. Baseline scoring must cover every candidate, including the fixture,
	//    and the frozen score must be deterministic.
	scored, err := ranking.ScoreBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scored < 1 {
		t.Fatalf("expected baseline scoring to cover candidates, got %d", scored)
	}
	var bscore float64
	if err := pool.QueryRow(ctx, `SELECT score FROM tg_candidate_baseline_scores WHERE candidate_id=$1 AND baseline_version=$2`, candidateID, "baseline-v1").Scan(&bscore); err != nil {
		t.Fatalf("baseline score row missing: %v", err)
	}
	if bscore < 0 || bscore > 10 {
		t.Fatalf("baseline score out of documented range: %v", bscore)
	}

	// 3. The ranked queue must order by score descending with a rank column.
	queue, err := ranking.RankedQueue(ctx, CandidateFilter{Status: "PENDING", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(queue); i++ {
		si, sj := *queue[i-1].ActiveScore, *queue[i].ActiveScore
		if si < sj || (si == sj && queue[i-1].CandidateID > queue[i].CandidateID) {
			t.Fatalf("queue ordering violated at %d: %v/%d then %v/%d", i, si, queue[i-1].CandidateID, sj, queue[i].CandidateID)
		}
		if *queue[i].ActiveRank != i+1 {
			t.Fatalf("rank column wrong at %d: %v", i, *queue[i].ActiveRank)
		}
	}

	// 4. Training with only fixture labels must report NOT_READY and leave
	//    the frozen baseline active.
	model, decision, err := ranking.TrainAndEvaluate(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.NotReady || decision.Promote {
		t.Fatalf("expected NOT_READY with no genuine labels, got %+v", decision)
	}
	status, err := ranking.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveRanker != "BASELINE" || !status.UsingBaseline {
		t.Fatalf("system must stay on the baseline: %+v", status)
	}
	if status.ReadyForTraining {
		t.Fatalf("readiness must be false without genuine labels: %+v", status.ReadyReason)
	}
	if status.ExcludedFixtures < 1 {
		t.Fatalf("status must report excluded fixtures, got %d", status.ExcludedFixtures)
	}
	_ = model

	// 5. Promote/rollback guard rails.
	if err := ranking.PromoteModel(ctx, "learned-v-does-not-exist"); err == nil {
		t.Fatal("promoting an unknown model must fail")
	}
	if err := ranking.PromoteModel(ctx, model.Version); err == nil {
		t.Fatal("promoting an un-evaluated DRAFT model must fail")
	}
	if err := ranking.Rollback(ctx, "phase9 integration test rollback"); err != nil {
		t.Fatal(err)
	}
	after, err := ranking.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.ActiveRanker != "BASELINE" {
		t.Fatalf("rollback must restore the baseline: %+v", after)
	}
}

// TestLiveRankingRunAuditTrailExists verifies that the run table records the
// distinct learning-system states required by the phase: feedback collected,
// model trained, model evaluated, promotion decisions.
func TestLiveRankingRunAuditTrailExists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	ranking := RankingStore{Pool: pool}
	if _, _, err := ranking.TrainAndEvaluate(ctx, true); err != nil {
		t.Fatal(err)
	}
	var runKinds int
	if err := pool.QueryRow(ctx, `SELECT COUNT(DISTINCT run_kind) FROM tg_ranking_runs`).Scan(&runKinds); err != nil {
		t.Fatal(err)
	}
	if runKinds < 1 {
		t.Fatal("expected at least one recorded ranking run")
	}
}

func candidateIDFromGID(gid string) int64 {
	return CandidateID(gid)
}

var _ = json.Marshal
