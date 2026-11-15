package intel

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Feature extraction
// ---------------------------------------------------------------------------

func candidateFixture() Candidate {
	return Candidate{
		CandidateID:      42,
		CandidateGID:     "CANDIDATE:VENDOR_CORPORATE_ENTITY:VENDOR:cp:1:ENTITY:v:abc",
		RelationshipType: "VENDOR_CORPORATE_ENTITY",
		SourceGID:        "VENDOR:cp:1",
		TargetGID:        "ENTITY:v:abc",
		GenerationMethod: "GSTIN_EXACT_UNIQUE",
		CurrentStatus:    "PENDING",
		LatestEvidence:   json.RawMessage(`[{"method":"GSTIN_EXACT_UNIQUE","gstin":"ABCDE1234F","entity_count":1}]`),
	}
}

func TestExtractFeaturesDetectMethodsAndAmbiguity(t *testing.T) {
	c := candidateFixture()
	f := ExtractRankingFeatures(c, nil)
	if f.GstinUnique != 1 || f.NameMatch != 0 || f.GstinAmbiguous != 0 {
		t.Fatalf("unexpected unique-GSTIN features: %+v", f)
	}
	if f.EvidenceCount != 1.0/5.0 || f.EndpointEvidence != 1 {
		t.Fatalf("unexpected completeness features: %+v", f)
	}
	// Ambiguous GSTIN, no name corroboration: contradiction flag must fire.
	c.LatestEvidence = json.RawMessage(`[{"method":"GSTIN_EXACT_AMBIGUOUS","gstin":"X","entity_count":3}]`)
	f = ExtractRankingFeatures(c, nil)
	if f.GstinAmbiguous != 1 || f.AmbiguousNoName != 1 || f.GstinAmbiguity <= 0 {
		t.Fatalf("unexpected ambiguous features: %+v", f)
	}
	// Multi-evidence candidate.
	c.LatestEvidence = json.RawMessage(`[{"method":"GSTIN_EXACT_UNIQUE"},{"method":"NAME_NORMALIZED_EXACT"}]`)
	f = ExtractRankingFeatures(c, nil)
	if f.MultiEvidence != 1 || f.NameMatch != 1 || f.GstinUnique != 1 {
		t.Fatalf("unexpected multi-evidence features: %+v", f)
	}
	if f.AmbiguousNoName != 0 {
		t.Fatalf("corroborated ambiguous candidate must not be flagged contradictory: %+v", f)
	}
}

func TestExtractFeaturesIsDeterministic(t *testing.T) {
	c := candidateFixture()
	a := ExtractRankingFeatures(c, nil)
	b := ExtractRankingFeatures(c, nil)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("feature extraction must be deterministic")
	}
}

// ---------------------------------------------------------------------------
// Frozen baseline: determinism and documented weight behaviour
// ---------------------------------------------------------------------------

func TestBaselineScoreIsDeterministicAndOrdered(t *testing.T) {
	unique := ExtractRankingFeatures(candidateFixture(), nil)
	ambiguous := candidateFixture()
	ambiguous.LatestEvidence = json.RawMessage(`[{"method":"GSTIN_EXACT_AMBIGUOUS","entity_count":5}]`)
	fAmbiguous := ExtractRankingFeatures(ambiguous, nil)
	nameOnly := candidateFixture()
	nameOnly.LatestEvidence = json.RawMessage(`[{"method":"NAME_NORMALIZED_EXACT"}]`)
	fName := ExtractRankingFeatures(nameOnly, nil)

	sUnique, sAmbiguous, sName := 0.0, 0.0, 0.0
	sUnique = BaselineScore(unique)
	sAmbiguous = BaselineScore(fAmbiguous)
	sName = BaselineScore(fName)

	if sUnique <= sAmbiguous {
		t.Fatalf("a unique GSTIN match must outrank an ambiguous one: %v vs %v", sUnique, sAmbiguous)
	}
	if sName <= sAmbiguous-1.0 {
		t.Fatalf("ambiguity must cost at least one point against a corroborated name match: ambiguous=%v name-only=%v", sAmbiguous, sName)
	}
	if BaselineScore(unique) != sUnique {
		t.Fatal("baseline scoring must be deterministic")
	}
}

func TestBaselineRejectedAlwaysBelowPending(t *testing.T) {
	c := candidateFixture()
	c.CurrentStatus = "CONFIRMED"
	confirmed := BaselineScore(ExtractRankingFeatures(c, nil))
	c.CurrentStatus = "REJECTED"
	rejected := BaselineScore(ExtractRankingFeatures(c, nil))
	c.CurrentStatus = "PENDING"
	pending := BaselineScore(ExtractRankingFeatures(c, nil))
	if rejected >= pending {
		t.Fatalf("rejected candidates must sink below pending: rejected=%v pending=%v", rejected, pending)
	}
	if confirmed <= pending {
		t.Fatalf("confirmed candidates must outrank pending: confirmed=%v pending=%v", confirmed, pending)
	}
}

// ---------------------------------------------------------------------------
// Label exclusions
// ---------------------------------------------------------------------------

func TestIsTestDecisionExcludesOperatorTestActors(t *testing.T) {
	cases := []struct {
		actor string
		want  bool
	}{
		{"operator:integration-test", true},
		{"operator:phase75-test", true},
		{"test:abc", true},
		{"analyst:me", false},
		{"operator-supplied", false},
		{"review-2026-11-14-001", false},
	}
	for _, c := range cases {
		if got := IsTestDecision(c.actor, "OPERATOR_SUPPLIED"); got != c.want {
			t.Fatalf("IsTestDecision(%q) = %v, want %v", c.actor, got, c.want)
		}
	}
}

func TestIsTestCandidate(t *testing.T) {
	if !IsTestCandidate("TEST:CANDIDATE:PHASE7:123", "TEST_FIXTURE") {
		t.Fatal("TEST:CANDIDATE fixture must be excluded")
	}
	if IsTestCandidate("CANDIDATE:VENDOR_CORPORATE_ENTITY:VENDOR:cp:1:ENTITY:v:abc", "GSTIN_EXACT_UNIQUE") {
		t.Fatal("a real candidate must not be excluded")
	}
}

// ---------------------------------------------------------------------------
// Split, leakage and duplicate handling
// ---------------------------------------------------------------------------

func sample(id int64, vendor string, at time.Time, label int) LabelledSample {
	return LabelledSample{CandidateID: id, VendorID: vendor, EntityID: "E" + vendor,
		Features: RankingFeatures{GstinUnique: 1}, Label: label, DecidedAt: at}
}

func TestTemporalGroupSplitKeepsVendorsWholeAndRespectsTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var samples []LabelledSample
	// 10 vendors, each with 2 decisions; vendor i decides at base + i days.
	for v := 0; v < 10; v++ {
		at := base.Add(time.Duration(v) * 24 * time.Hour)
		samples = append(samples, sample(int64(v*2), fmtVendor(v), at, 1), sample(int64(v*2+1), fmtVendor(v), at, 0))
	}
	train, eval := TemporalGroupSplit(samples, 0.7)
	if len(train)+len(eval) != len(samples) {
		t.Fatal("split must be a partition")
	}
	trainVendors := map[string]bool{}
	for _, s := range train {
		trainVendors[s.VendorID] = true
	}
	for _, s := range eval {
		if trainVendors[s.VendorID] {
			t.Fatalf("vendor %s appears on both sides of the split", s.VendorID)
		}
	}
	// Temporal ordering: every training group's earliest decision precedes
	// every evaluation group's earliest decision.
	maxTrain := time.Time{}
	for _, s := range train {
		if s.DecidedAt.After(maxTrain) {
			maxTrain = s.DecidedAt
		}
	}
	minEval := maxTrain.Add(time.Hour)
	for _, s := range eval {
		if s.DecidedAt.Before(minEval) {
			minEval = s.DecidedAt
		}
	}
	if maxTrain.After(minEval) {
		t.Fatalf("temporal ordering violated: train max %v after eval min %v", maxTrain, minEval)
	}
}

func fmtVendor(i int) string { return "V" + jsonInt(i) }

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestBuildSamplesCollapsesDuplicates(t *testing.T) {
	at := time.Now()
	labels := []trainingLabel{
		{Candidate: candidateFixture(), DecisionStatus: "CONFIRMED", DecidedAt: at},
		{Candidate: candidateFixture(), DecisionStatus: "CONFIRMED", DecidedAt: at},
	}
	samples, dups := BuildSamples(labels)
	if len(samples) != 1 || dups != 1 {
		t.Fatalf("expected 1 sample and 1 collapsed duplicate, got %d/%d", len(samples), dups)
	}
}

// ---------------------------------------------------------------------------
// Logistic regression: determinism and sanity
// ---------------------------------------------------------------------------

func TestTrainLogisticIsDeterministicAndSeparable(t *testing.T) {
	at := time.Now()
	pos := RankingFeatures{GstinUnique: 1, NameMatch: 1, MultiEvidence: 1}
	neg := RankingFeatures{GstinAmbiguous: 1, NameMatch: 0}
	var samples []LabelledSample
	for i := 0; i < 30; i++ {
		samples = append(samples,
			LabelledSample{CandidateID: int64(i), VendorID: "P" + jsonInt(i), Features: pos, Label: 1, DecidedAt: at},
			LabelledSample{CandidateID: int64(1000 + i), VendorID: "N" + jsonInt(i), Features: neg, Label: 0, DecidedAt: at})
	}
	m1 := TrainLogistic("learned-test-1", samples)
	m2 := TrainLogistic("learned-test-1", samples)
	if !reflect.DeepEqual(m1.Weights, m2.Weights) || m1.Bias != m2.Bias {
		t.Fatal("training must be reproducible")
	}
	if p := m1.Predict(pos); p < 0.8 {
		t.Fatalf("clearly-positive features should score high, got %v", p)
	}
	if p := m1.Predict(neg); p > 0.2 {
		t.Fatalf("clearly-negative features should score low, got %v", p)
	}
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

func TestEvaluateRankingMetrics(t *testing.T) {
	// 5 positives, 5 negatives. The given ordering has positives at ranks
	// 1,2,4,7,9 (by score).
	items := []EvalItem{
		{CandidateID: 1, Label: 1, Baseline: 0.9},
		{CandidateID: 2, Label: 1, Baseline: 0.8},
		{CandidateID: 3, Label: 0, Baseline: 0.7},
		{CandidateID: 4, Label: 1, Baseline: 0.6},
		{CandidateID: 5, Label: 0, Baseline: 0.5},
		{CandidateID: 6, Label: 0, Baseline: 0.4},
		{CandidateID: 7, Label: 1, Baseline: 0.3},
		{CandidateID: 8, Label: 0, Baseline: 0.2},
		{CandidateID: 9, Label: 1, Baseline: 0.1},
		{CandidateID: 10, Label: 0, Baseline: 0.0},
	}
	m := EvaluateRanking(items, func(i EvalItem) float64 { return i.Baseline })
	if m.SampleSize != 10 || m.Positives != 5 || m.Negatives != 5 {
		t.Fatalf("unexpected counts: %+v", m)
	}
	if m.PrecisionAt5 != 3.0/5.0 {
		t.Fatalf("precision@5: got %v want 0.6", m.PrecisionAt5)
	}
	if m.PrecisionAt10 != 0.5 {
		t.Fatalf("precision@10: got %v want 0.5", m.PrecisionAt10)
	}
	if m.RecallAt10 != 1.0 {
		t.Fatalf("recall@10 over 5 positives: got %v", m.RecallAt10)
	}
	if m.FalsePosRate10 != 0.5 {
		t.Fatalf("FPR@10: got %v want 0.5", m.FalsePosRate10)
	}
	if m.F1 <= 0 || m.F1 > 1 {
		t.Fatalf("F1 out of range: %v", m.F1)
	}
	if m.PRAUC <= 0.5 || m.PRAUC > 1 {
		t.Fatalf("PR-AUC out of expected range: %v", m.PRAUC)
	}
}

func TestEvaluateRankingPerfectAndInverted(t *testing.T) {
	pos := []EvalItem{{Label: 1, Baseline: 1}, {Label: 1, Baseline: 0.9}, {Label: 0, Baseline: 0.1}, {Label: 0, Baseline: 0}}
	perfect := EvaluateRanking(pos, func(i EvalItem) float64 { return i.Baseline })
	if perfect.PRAUC != 1 || perfect.PrecisionAt5 != 0.5 || perfect.FalsePosRate10 != 0.5 {
		t.Fatalf("perfect ordering metrics: %+v", perfect)
	}
	inverted := EvaluateRanking(pos, func(i EvalItem) float64 { return 1 - i.Baseline })
	if inverted.PRAUC > 0.5 {
		t.Fatalf("inverted ordering should score below the random 0.5 mark: %v", inverted.PRAUC)
	}
}

func TestBootstrapCIIsDeterministic(t *testing.T) {
	items := []EvalItem{
		{CandidateID: 1, Label: 1, Baseline: 0.4, Learned: 0.9},
		{CandidateID: 2, Label: 0, Baseline: 0.6, Learned: 0.1},
		{CandidateID: 3, Label: 1, Baseline: 0.5, Learned: 0.8},
		{CandidateID: 4, Label: 0, Baseline: 0.2, Learned: 0.2},
		{CandidateID: 5, Label: 1, Baseline: 0.3, Learned: 0.7},
	}
	metric := func(is []EvalItem) float64 {
		l := EvaluateRanking(is, func(i EvalItem) float64 { return i.Learned })
		b := EvaluateRanking(is, func(i EvalItem) float64 { return i.Baseline })
		return l.PrecisionAt5 - b.PrecisionAt5
	}
	lo1, hi1 := BootstrapDeltaCI(items, metric)
	lo2, hi2 := BootstrapDeltaCI(items, metric)
	if lo1 != lo2 || hi1 != hi2 {
		t.Fatal("bootstrap CI must be deterministic")
	}
}

// ---------------------------------------------------------------------------
// Promotion gates
// ---------------------------------------------------------------------------

func TestEvaluatePromotionInsufficientData(t *testing.T) {
	m := LogisticModel{TrainSamples: 5, PosTrain: 3, NegTrain: 2, EvalSamples: 4, PosEval: 2, NegEval: 2}
	d := EvaluatePromotion(&m, nil)
	if !d.NotReady || d.Promote {
		t.Fatalf("insufficient data must block promotion: %+v", d)
	}
	if len(d.Reasons) < 2 {
		t.Fatalf("expected reasons for both train and eval shortfalls: %+v", d.Reasons)
	}
}

func TestEvaluatePromotionLeakageBlocks(t *testing.T) {
	m := LogisticModel{
		TrainSamples: 60, PosTrain: 25, NegTrain: 35,
		EvalSamples: 20, PosEval: 8, NegEval: 12,
		EvalConfig: map[string]any{"vendor_group_leakage": true},
	}
	d := EvaluatePromotion(&m, nil)
	if d.Promote {
		t.Fatal("leakage must block promotion")
	}
}

func TestEvaluatePromotionImprovingModelPasses(t *testing.T) {
	// Build a held-out set where the learned score orders perfectly and the
	// baseline does not, with enough items for stable metrics.
	var items []EvalItem
	for i := 0; i < 10; i++ {
		items = append(items,
			EvalItem{CandidateID: int64(i), Label: 1, Baseline: 0.5, Learned: 0.9 - 0.01*float64(i)},
			EvalItem{CandidateID: int64(100 + i), Label: 0, Baseline: 0.6, Learned: 0.1 + 0.001*float64(i)})
	}
	m := LogisticModel{
		TrainSamples: 200, PosTrain: 90, NegTrain: 110,
		EvalSamples: len(items), PosEval: 10, NegEval: 10,
		EvalConfig: map[string]any{"vendor_group_leakage": false},
	}
	d := EvaluatePromotion(&m, items)
	if !d.Promote {
		t.Fatalf("expected promotion, reasons: %v", d.Reasons)
	}
	if d.DeltaPrecision5 <= 0 {
		t.Fatalf("delta precision@5 must be positive: %v", d.DeltaPrecision5)
	}
}

func TestEvaluatePromotionRegressingFPRBlocks(t *testing.T) {
	// Baseline: 9 of its top-10 are positive (FPR@10 = 0.1) but its top-5
	// contains a negative (P@5 = 0.8). Learned: perfect top-5 (P@5 = 1.0)
	// but 4 negatives in its top-10 (FPR@10 = 0.4) — a real regression.
	items := []EvalItem{}
	// Baseline: 4 positives at 0.9, 1 negative at 0.85, 5 positives at 0.5, 6 negatives at 0.2.
	// Learned: 5 positives at 0.95, 3 negatives at 0.9, 4 positives at 0.6, 4 negatives at 0.05.
	for i := 0; i < 4; i++ {
		items = append(items, EvalItem{CandidateID: int64(i), Label: 1, Baseline: 0.9, Learned: 0.95})
	}
	for i := 0; i < 5; i++ {
		learnedScore := 0.6
		if i == 0 {
			learnedScore = 0.97 // pushes one second-tier positive above the negatives
		}
		items = append(items, EvalItem{CandidateID: int64(100 + i), Label: 1, Baseline: 0.5, Learned: learnedScore})
	}
	items = append(items, EvalItem{CandidateID: 90, Label: 0, Baseline: 0.85, Learned: 0.9})
	for i := 0; i < 3; i++ {
		items = append(items, EvalItem{CandidateID: int64(200 + i), Label: 0, Baseline: 0.2, Learned: 0.9})
	}
	for i := 0; i < 4; i++ {
		items = append(items, EvalItem{CandidateID: int64(300 + i), Label: 0, Baseline: 0.2, Learned: 0.05})
	}
	m := LogisticModel{
		TrainSamples: 200, PosTrain: 90, NegTrain: 110,
		EvalSamples: len(items), PosEval: 9, NegEval: 7,
		EvalConfig: map[string]any{"vendor_group_leakage": false},
	}
	base := EvaluateRanking(items, func(i EvalItem) float64 { return i.Baseline })
	learned := EvaluateRanking(items, func(i EvalItem) float64 { return i.Learned })
	if base.PrecisionAt5 != 0.8 || learned.PrecisionAt5 != 1.0 {
		t.Fatalf("test construction broken: baseline P@5=%v learned P@5=%v", base.PrecisionAt5, learned.PrecisionAt5)
	}
	if base.FalsePosRate10 != 0.1 || learned.FalsePosRate10 != 0.4 {
		t.Fatalf("test construction broken: baseline FPR@10=%v learned FPR@10=%v", base.FalsePosRate10, learned.FalsePosRate10)
	}
	d := EvaluatePromotion(&m, items)
	if d.Promote {
		t.Fatalf("FPR regression must block promotion: %+v", d)
	}
}

func TestScoresClamp(t *testing.T) {
	if clampScore(-5) != 0 || clampScore(15) != 10 {
		t.Fatal("score clamping failed")
	}
	if math.IsNaN(BaselineScore(RankingFeatures{EvidenceCount: 2})) {
		t.Fatal("score must be a number")
	}
}
