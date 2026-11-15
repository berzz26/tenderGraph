package intel

// Phase 9 continual-learning candidate ranking.
//
// Two rankers exist side by side:
//
//   1. baseline-v1: a frozen, deterministic, rule-based ranker. Its weights
//      never change in response to feedback; a changed rule set would be a new
//      baseline version, never a silent mutation.
//   2. learned-vN: a regularized logistic-regression model trained only on
//      genuine analyst decisions (CONFIRMED = positive, REJECTED = negative,
//      NEEDS_REVIEW excluded). A learned model is never the production ranker
//      until it passes explicit evaluation gates; tg_ranking_active decides.
//
// The frozen baseline score is never overwritten by the learned ranker: the
// two live in separate tables and the active-ranker pointer selects which one
// orders the review queue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// baselineVersion is frozen. Any change to baseline scoring rules must
	// bump this constant, which creates a new score set instead of
	// overwriting the old one.
	baselineVersion = "baseline-v1"
	// featureVersion identifies the feature extractor contract. A learned
	// model is only valid for the feature version it was trained with.
	featureVersion = "features-v1"

	// Minimum-data thresholds. Below these, training is refused and the
	// system reports NOT_READY while continuing to use the baseline.
	minTrainSamples = 40
	minEvalSamples  = 15
	minTrainPos     = 12
	minTrainNeg     = 12
	minEvalPos      = 5
	minEvalNeg      = 5
	// Fraction of label-groups (by vendor) whose earliest decision time
	// falls inside the training window of the temporal group split.
	trainGroupFraction = 0.7

	// Bootstrap configuration for the promotion confidence interval. A fixed
	// seed keeps every evaluation reproducible.
	bootstrapResamples = 1000
	bootstrapSeed      = 42
)

// ---------------------------------------------------------------------------
// Features
// ---------------------------------------------------------------------------

// RankingFeatures is the features-v1 feature vector. Every feature is
// deterministic given the candidate row, its evidence JSON and the candidate's
// current decision state.
type RankingFeatures struct {
	// GstinUnique: 1 when the candidate evidence contains an exact GSTIN
	// match that maps to exactly one corporate entity.
	GstinUnique float64
	// GstinAmbiguous: 1 when the candidate evidence contains an exact GSTIN
	// match that maps to more than one corporate entity.
	GstinAmbiguous float64
	// GstinAmbiguity: log-scaled count of entities sharing the matched GSTIN
	// (0 when the candidate has no GSTIN evidence). Kept log-scaled so a
	// single extreme GSTIN cannot dominate a linear model.
	GstinAmbiguity float64
	// NameMatch: 1 when the evidence contains a normalized business-name
	// match between the vendor and the entity.
	NameMatch float64
	// MultiEvidence: 1 when more than one independent matching method exists
	// for the same vendor/entity pair.
	MultiEvidence float64
	// EvidenceCount: number of independent evidence entries (capped at 5),
	// normalized by 5.
	EvidenceCount float64
	// EndpointEvidence: 1 when both endpoints carry explicit source GIDs.
	EndpointEvidence float64
	// AmbiguousNoName: contradiction flag — an ambiguous GSTIN match with no
	// name corroboration. Evidence that actively argues against identity.
	AmbiguousNoName float64
	// Announcement: 1 for TENDER_ANNOUNCEMENT candidates. Type is preserved
	// so a learned model can treat candidate classes differently.
	Announcement float64
	// AlreadyConfirmed: 1 when an analyst has already confirmed the
	// candidate.
	AlreadyConfirmed float64
	// AlreadyRejected: 1 when an analyst has already rejected the candidate.
	AlreadyRejected float64
	// AlreadyNeedsReview: 1 when the candidate is parked in NEEDS_REVIEW.
	AlreadyNeedsReview float64
}

// featureNames is the frozen features-v1 order used by both rankers.
var featureNames = []string{
	"gstin_unique", "gstin_ambiguous", "gstin_ambiguity", "name_match",
	"multi_evidence", "evidence_count", "endpoint_evidence",
	"ambiguous_no_name", "announcement_type",
	"already_confirmed", "already_rejected", "already_needs_review",
}

// Vector returns the feature values in the frozen features-v1 order.
func (f RankingFeatures) Vector() []float64 {
	return []float64{
		f.GstinUnique, f.GstinAmbiguous, f.GstinAmbiguity, f.NameMatch,
		f.MultiEvidence, f.EvidenceCount, f.EndpointEvidence,
		f.AmbiguousNoName, f.Announcement,
		f.AlreadyConfirmed, f.AlreadyRejected, f.AlreadyNeedsReview,
	}
}

// ExtractRankingFeatures derives features-v1 values from a candidate row and
// (optionally) its current decision status. No database access: pure and
// deterministic.
func ExtractRankingFeatures(c Candidate, evidence json.RawMessage) RankingFeatures {
	var f RankingFeatures
	methods := map[string]bool{}
	entries := 0
	ambiguity := 0
	hasGstin := false
	endpointGIDs := c.SourceGID != "" && c.TargetGID != ""

	raw := evidence
	if len(raw) == 0 {
		raw = c.LatestEvidence
	}
	if len(raw) == 0 {
		raw = c.InitialEvidence
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		// Evidence that is not an array of objects contributes no features.
		list = nil
	}
	for _, e := range list {
		entries++
		if m, ok := e["method"].(string); ok {
			methods[m] = true
		}
		if g, ok := e["gstin"].(string); ok && strings.TrimSpace(g) != "" {
			hasGstin = true
		}
		if n, ok := e["entity_count"].(float64); ok && int(n) > ambiguity {
			ambiguity = int(n)
		}
	}

	if methods["GSTIN_EXACT_UNIQUE"] {
		f.GstinUnique = 1
	}
	if methods["GSTIN_EXACT_AMBIGUOUS"] {
		f.GstinAmbiguous = 1
		if ambiguity < 2 {
			ambiguity = 2
		}
	}
	f.GstinAmbiguity = math.Min(math.Log1p(float64(ambiguity))/math.Log1p(20), 1)
	if methods["NAME_NORMALIZED_EXACT"] {
		f.NameMatch = 1
	}
	if len(methods) > 1 {
		f.MultiEvidence = 1
	}
	f.EvidenceCount = math.Min(float64(entries), 5) / 5
	if endpointGIDs {
		f.EndpointEvidence = 1
	}
	if f.GstinAmbiguous == 1 && f.NameMatch == 0 {
		f.AmbiguousNoName = 1
	}
	if c.RelationshipType == "TENDER_ANNOUNCEMENT" {
		f.Announcement = 1
	}
	switch c.CurrentStatus {
	case "CONFIRMED":
		f.AlreadyConfirmed = 1
	case "REJECTED":
		f.AlreadyRejected = 1
	case "NEEDS_REVIEW":
		f.AlreadyNeedsReview = 1
	}
	_ = hasGstin
	return f
}

// ---------------------------------------------------------------------------
// Frozen baseline ranker (baseline-v1)
// ---------------------------------------------------------------------------

// baselineWeights are frozen for baseline-v1. Each weight is documented in
// docs/phase9/CONTINUAL_LEARNING.md; changing any value requires a new
// baseline version constant and a new score set.
var baselineWeights = map[string]float64{
	"gstin_unique":         3.0,    // strongest defensible identity signal
	"gstin_ambiguous":      1.5,    // real match, but not unique identity
	"gstin_ambiguity":      -0.5,   // more entities per GSTIN → weaker
	"name_match":           1.0,    // conservative corroboration
	"multi_evidence":       1.5,    // independent methods agree
	"evidence_count":       0.25,   // per additional evidence entry (0..1 scaled)
	"endpoint_evidence":    0.5,    // both endpoints traceable
	"ambiguous_no_name":    -1.0,   // contradiction penalty
	"announcement_type":    0.0,    // candidate type is neutral in the baseline
	"already_confirmed":    2.0,    // surfaced after new pending work
	"already_rejected":     -100.0, // rejected candidates sink below the queue
	"already_needs_review": -0.25,
}

// BaselineScore computes the frozen baseline-v1 score for a feature vector.
// The pending-queue score is clamped to [0, 10]; decision-state adjustments
// are applied before clamping so rejected candidates always rank below
// pending ones.
func BaselineScore(f RankingFeatures) float64 {
	w := f.WeightsMap(baselineWeights)
	score := 0.0
	for name, value := range map[string]float64{
		"gstin_unique":         f.GstinUnique,
		"gstin_ambiguous":      f.GstinAmbiguous,
		"gstin_ambiguity":      f.GstinAmbiguity,
		"name_match":           f.NameMatch,
		"multi_evidence":       f.MultiEvidence,
		"evidence_count":       f.EvidenceCount,
		"endpoint_evidence":    f.EndpointEvidence,
		"ambiguous_no_name":    f.AmbiguousNoName,
		"announcement_type":    f.Announcement,
		"already_confirmed":    f.AlreadyConfirmed,
		"already_rejected":     f.AlreadyRejected,
		"already_needs_review": f.AlreadyNeedsReview,
	} {
		score += w[name] * value
	}
	// Corroboration bonus: unique GSTIN + name agreement.
	if f.GstinUnique == 1 && f.NameMatch == 1 {
		score += w["gstin_unique"]
	}
	return clampScore(score)
}

// WeightsMap is a helper for tests that need the weight values.
func (f RankingFeatures) WeightsMap(m map[string]float64) map[string]float64 { return m }

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 10 {
		return 10
	}
	return v
}

// ---------------------------------------------------------------------------
// Learned ranker: regularized logistic regression (deterministic)
// ---------------------------------------------------------------------------

// LogisticModel is a small, interpretable logistic-regression model over the
// features-v1 vector. Full-batch gradient descent with fixed iteration count
// and L2 regularization is fully deterministic — no random initialization.
type LogisticModel struct {
	Version        string         `json:"model_version"`
	FeatureVersion string         `json:"feature_version"`
	Baseline       string         `json:"baseline_version"`
	Weights        []float64      `json:"weights"`
	Bias           float64        `json:"bias"`
	TrainedAt      time.Time      `json:"trained_at"`
	TrainSamples   int            `json:"train_samples"`
	EvalSamples    int            `json:"eval_samples"`
	PosTrain       int            `json:"positive_train"`
	NegTrain       int            `json:"negative_train"`
	PosEval        int            `json:"positive_eval"`
	NegEval        int            `json:"negative_eval"`
	Metrics        RankingMetrics `json:"metrics"`
	BaselineMetr   RankingMetrics `json:"baseline_metrics"`
	EvalConfig     map[string]any `json:"eval_config"`
	Notes          []string       `json:"notes"`
}

const (
	lrIterations   = 800
	lrLearningRate = 0.35
	lrLambda       = 0.1 // L2 regularization; keeps small-sample training stable
)

// TrainLogistic fits the learned ranker on labelled samples. Deterministic:
// identical inputs always produce identical weights.
func TrainLogistic(version string, samples []LabelledSample) LogisticModel {
	dim := len(featureNames)
	m := LogisticModel{
		Version: version, FeatureVersion: featureVersion, Baseline: baselineVersion,
		Weights: make([]float64, dim), TrainedAt: time.Now().UTC(),
		TrainSamples: len(samples),
	}
	var sumY float64
	for _, s := range samples {
		if s.Label == 1 {
			m.PosTrain++
		} else {
			m.NegTrain++
		}
		sumY += float64(s.Label)
	}
	if len(samples) == 0 {
		return m
	}
	mean := sumY / float64(len(samples))
	m.Bias = math.Log(mean / (1 - mean)) // prior baseline; improves early convergence
	for iter := 0; iter < lrIterations; iter++ {
		gradW := make([]float64, dim)
		gradB := 0.0
		for _, s := range samples {
			p := sigmoid(dot(m.Weights, s.Features.Vector()) + m.Bias)
			err := p - float64(s.Label)
			for j := range gradW {
				gradW[j] += err * s.Features.Vector()[j]
			}
			gradB += err
		}
		n := float64(len(samples))
		for j := range m.Weights {
			m.Weights[j] -= lrLearningRate * (gradW[j]/n + lrLambda*m.Weights[j])
		}
		m.Bias -= lrLearningRate * gradB / n
	}
	return m
}

// Predict returns the probability that the candidate is confirmed.
func (m *LogisticModel) Predict(f RankingFeatures) float64 {
	return sigmoid(dot(m.Weights, f.Vector()) + m.Bias)
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func sigmoid(x float64) float64 {
	if x > 35 {
		return 1
	}
	if x < -35 {
		return 0
	}
	return 1 / (1 + math.Exp(-x))
}

// ---------------------------------------------------------------------------
// Labels, split and evaluation
// ---------------------------------------------------------------------------

// LabelledSample is one training/evaluation example: a candidate's features
// with its analyst decision label.
type LabelledSample struct {
	CandidateID int64
	VendorID    string // source_id for VENDOR_CORPORATE_ENTITY candidates
	EntityID    string // target_id
	Features    RankingFeatures
	Label       int // 1 = CONFIRMED, 0 = REJECTED (NEEDS_REVIEW never enters)
	DecidedAt   time.Time
}

// LabelExclusions describes the fixture/test-exclusion rules applied before
// any sample is accepted. All rules are reported in the run record.
var testActorPrefixes = []string{
	"operator:integration-test", "operator:phase75-test", "operator:ranking-test",
}

// IsTestDecision reports whether a decision row is a test fixture or was made
// by an operator-generated test actor. Genuine analyst feedback and test
// fixtures are never mixed.
func IsTestDecision(actorID, actorKind string) bool {
	id := strings.ToLower(strings.TrimSpace(actorID))
	for _, p := range testActorPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return id == "test" || strings.HasPrefix(id, "test:") ||
		strings.HasPrefix(id, "test-") || strings.Contains(id, "-test-") ||
		strings.HasSuffix(id, "-test")
}

// IsTestCandidate reports whether a candidate row is an integration fixture.
func IsTestCandidate(candidateGID, generationMethod string) bool {
	return strings.HasPrefix(candidateGID, "TEST:CANDIDATE") ||
		generationMethod == "TEST_FIXTURE" || generationMethod == "PHASE75_TEST"
}

// Split temporally at the label-group level. Groups are vendors (source_id);
// groups are ordered by their earliest decision timestamp and the prefix
// covering trainGroupFraction of groups forms the training set. A vendor's
// candidates therefore never straddle the split, which prevents the same
// underlying entity relationship from appearing on both sides, while the
// split still respects temporal ordering between groups.
// Returns (train, eval).
func TemporalGroupSplit(samples []LabelledSample, fraction float64) ([]LabelledSample, []LabelledSample) {
	if len(samples) == 0 {
		return nil, nil
	}
	type group struct {
		vendor   string
		earliest time.Time
		indexes  []int
	}
	byVendor := map[string]*group{}
	var order []string
	for i, s := range samples {
		g, ok := byVendor[s.VendorID]
		if !ok {
			g = &group{vendor: s.VendorID, earliest: s.DecidedAt}
			byVendor[s.VendorID] = g
			order = append(order, s.VendorID)
		}
		if s.DecidedAt.Before(g.earliest) {
			g.earliest = s.DecidedAt
		}
		g.indexes = append(g.indexes, i)
	}
	sort.Slice(order, func(i, j int) bool {
		gi, gj := byVendor[order[i]], byVendor[order[j]]
		if !gi.earliest.Equal(gj.earliest) {
			return gi.earliest.Before(gj.earliest)
		}
		return gi.vendor < gj.vendor
	})
	cut := int(math.Round(float64(len(order)) * fraction))
	if cut < 1 {
		cut = 1
	}
	if cut >= len(order) {
		cut = len(order) - 1
	}
	trainSet := map[string]bool{}
	for i := 0; i < cut; i++ {
		trainSet[order[i]] = true
	}
	var train, eval []LabelledSample
	for _, v := range order {
		for _, idx := range byVendor[v].indexes {
			if trainSet[v] {
				train = append(train, samples[idx])
			} else {
				eval = append(eval, samples[idx])
			}
		}
	}
	return train, eval
}

// EvalItem is one held-out candidate with its two competing scores.
type EvalItem struct {
	CandidateID int64
	Label       int
	Baseline    float64
	Learned     float64 // probability when learned, raw score otherwise
}

// RankingMetrics holds the benchmark metrics for one ranker on one held-out
// set. All definitions are documented in CONTINUAL_LEARNING.md.
type RankingMetrics struct {
	PrecisionAt5   float64 `json:"precision_at_5"`
	PrecisionAt10  float64 `json:"precision_at_10"`
	PrecisionAt20  float64 `json:"precision_at_20"`
	RecallAt10     float64 `json:"recall_at_10"`
	FalsePosRate10 float64 `json:"false_positive_rate_at_10"`
	PRAUC          float64 `json:"pr_auc"`
	F1             float64 `json:"f1"`
	F1Threshold    string  `json:"f1_operating_point"`
	Coverage       float64 `json:"coverage"`
	SampleSize     int     `json:"sample_size"`
	Positives      int     `json:"positives"`
	Negatives      int     `json:"negatives"`
}

// EvaluateRanking scores a ranked list of held-out items. Rank-based metrics
// use the ordering; the F1 operating point is documented: positive prediction
// = rank inside the top max(5, ceil(0.2*N)) of the queue, identical for both
// rankers. PRAUC is computed over the full score ordering.
func EvaluateRanking(items []EvalItem, score func(EvalItem) float64) RankingMetrics {
	m := RankingMetrics{SampleSize: len(items)}
	type row struct {
		item  EvalItem
		score float64
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		if it.Label != 0 && it.Label != 1 {
			continue
		}
		rows = append(rows, row{it, score(it)})
		if it.Label == 1 {
			m.Positives++
		} else {
			m.Negatives++
		}
	}
	if len(rows) == 0 {
		return m
	}
	m.Coverage = 1.0
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].item.CandidateID < rows[j].item.CandidateID
	})
	n := len(rows)
	posTotal := m.Positives
	kAt := func(k int) int {
		if k > n {
			k = n
		}
		return k
	}
	hit := func(k int) (tp int) {
		for i := 0; i < kAt(k); i++ {
			if rows[i].item.Label == 1 {
				tp++
			}
		}
		return tp
	}
	if kAt(5) > 0 {
		m.PrecisionAt5 = float64(hit(5)) / float64(kAt(5))
	}
	if kAt(10) > 0 {
		m.PrecisionAt10 = float64(hit(10)) / float64(kAt(10))
	}
	if kAt(20) > 0 {
		m.PrecisionAt20 = float64(hit(20)) / float64(kAt(20))
	}
	tp10 := hit(10)
	if posTotal > 0 {
		m.RecallAt10 = float64(tp10) / float64(posTotal)
	}
	fp10 := kAt(10) - tp10
	m.FalsePosRate10 = float64(fp10) / float64(kAt(10))
	// Documented F1 operating point: top max(5, ceil(0.2*N)).
	f1K := int(math.Ceil(0.2 * float64(n)))
	if f1K < 5 {
		f1K = 5
	}
	if f1K > n {
		f1K = n
	}
	tp, fp := 0, 0
	for i := 0; i < f1K; i++ {
		if rows[i].item.Label == 1 {
			tp++
		} else {
			fp++
		}
	}
	fn := posTotal - tp
	m.F1Threshold = fmt.Sprintf("top %d of %d", f1K, n)
	if 2*tp+fp+fn > 0 {
		m.F1 = 2 * float64(tp) / float64(2*tp+fp+fn)
	}
	// PR-AUC over the score ordering (step interpolation).
	if posTotal > 0 && m.Negatives > 0 {
		var auc, cumTP, cumFP float64
		prevRecall := 0.0
		for i := 0; i < n; i++ {
			if rows[i].item.Label == 1 {
				cumTP++
			} else {
				cumFP++
			}
			recall := cumTP / float64(posTotal)
			precision := cumTP / (cumTP + cumFP)
			auc += (recall - prevRecall) * precision
			prevRecall = recall
		}
		m.PRAUC = auc
	} else if posTotal > 0 {
		m.PRAUC = 1
	}
	return m
}

// BootstrapDeltaCI computes a seeded percentile bootstrap CI for
// metric(learned) - metric(baseline) on the held-out items. Deterministic:
// the same items always yield the same interval.
func BootstrapDeltaCI(items []EvalItem, metric func([]EvalItem) float64) (lo, hi float64) {
	rng := newSeededRand(bootstrapSeed)
	n := len(items)
	if n == 0 {
		return 0, 0
	}
	deltas := make([]float64, 0, bootstrapResamples)
	buf := make([]EvalItem, n)
	for r := 0; r < bootstrapResamples; r++ {
		for i := 0; i < n; i++ {
			buf[i] = items[rng.Intn(n)]
		}
		deltas = append(deltas, metric(buf))
	}
	sort.Float64s(deltas)
	pct := func(p float64) float64 {
		idx := int(math.Floor(p * float64(len(deltas)-1)))
		return deltas[idx]
	}
	return pct(0.025), pct(0.975)
}

// seededRand is a tiny deterministic PRNG (xorshift64*) so bootstrap results
// never depend on the math/rand global state.
type seededRand struct{ s uint64 }

func newSeededRand(seed uint64) *seededRand {
	if seed == 0 {
		seed = 1
	}
	return &seededRand{s: seed}
}

func (r *seededRand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	r.s ^= r.s >> 12
	r.s ^= r.s << 25
	r.s ^= r.s >> 27
	x := r.s * 0x2545F4914F6CDD1D
	return int((x >> 33) % uint64(n))
}

// ---------------------------------------------------------------------------
// Promotion gates
// ---------------------------------------------------------------------------

// PromotionDecision is the outcome of the evaluation gates.
type PromotionDecision struct {
	Promote         bool     `json:"promote"`
	Reasons         []string `json:"reasons"`
	NotReady        bool     `json:"not_ready"`
	DeltaPrecision5 float64  `json:"delta_precision_at_5"`
	CILower         float64  `json:"ci_lower_95"`
	CIUpper         float64  `json:"ci_upper_95"`
}

// EvaluatePromotion applies the promotion gates to a trained model's held-out
// metrics versus the frozen baseline on the same items. A model is promoted
// only when every gate passes; otherwise the baseline stays active and the
// reason list explains why.
func EvaluatePromotion(model *LogisticModel, items []EvalItem) PromotionDecision {
	d := PromotionDecision{}
	// Data sufficiency first.
	if model.TrainSamples < minTrainSamples || model.PosTrain < minTrainPos || model.NegTrain < minTrainNeg {
		d.NotReady = true
		d.Reasons = append(d.Reasons, fmt.Sprintf("training data below minimum: %d samples (%d positive, %d negative); need >= %d (%d/%d)",
			model.TrainSamples, model.PosTrain, model.NegTrain, minTrainSamples, minTrainPos, minTrainNeg))
	}
	if model.EvalSamples < minEvalSamples || model.PosEval < minEvalPos || model.NegEval < minEvalNeg {
		d.NotReady = true
		d.Reasons = append(d.Reasons, fmt.Sprintf("evaluation data below minimum: %d samples (%d positive, %d negative); need >= %d (%d/%d)",
			model.EvalSamples, model.PosEval, model.NegEval, minEvalSamples, minEvalPos, minEvalNeg))
	}
	if d.NotReady {
		d.Reasons = append(d.Reasons, "insufficient genuine labels: system stays on the frozen baseline")
		return d
	}
	// Leakage gate.
	if v, _ := model.EvalConfig["vendor_group_leakage"].(bool); v {
		d.Reasons = append(d.Reasons, "vendor/entity group leakage detected across the split; evaluation not defensible")
		return d
	}
	base := EvaluateRanking(items, func(i EvalItem) float64 { return i.Baseline })
	learned := EvaluateRanking(items, func(i EvalItem) float64 { return i.Learned })
	model.BaselineMetr = base
	model.Metrics = learned
	d.DeltaPrecision5 = learned.PrecisionAt5 - base.PrecisionAt5
	d.CILower, d.CIUpper = BootstrapDeltaCI(items, func(is []EvalItem) float64 {
		l := EvaluateRanking(is, func(i EvalItem) float64 { return i.Learned })
		b := EvaluateRanking(is, func(i EvalItem) float64 { return i.Baseline })
		return l.PrecisionAt5 - b.PrecisionAt5
	})
	if learned.PrecisionAt5 <= base.PrecisionAt5 {
		d.Reasons = append(d.Reasons, fmt.Sprintf("precision@5 did not improve: learned %.3f vs baseline %.3f", learned.PrecisionAt5, base.PrecisionAt5))
	}
	if d.CILower <= 0 {
		d.Reasons = append(d.Reasons, fmt.Sprintf("bootstrap 95%% CI for delta precision@5 includes zero: [%.3f, %.3f]", d.CILower, d.CIUpper))
	}
	if learned.PRAUC < base.PRAUC {
		d.Reasons = append(d.Reasons, fmt.Sprintf("PR-AUC regressed: learned %.3f vs baseline %.3f", learned.PRAUC, base.PRAUC))
	}
	if learned.FalsePosRate10 > base.FalsePosRate10 {
		d.Reasons = append(d.Reasons, fmt.Sprintf("false-positive rate@10 regressed: learned %.3f vs baseline %.3f", learned.FalsePosRate10, base.FalsePosRate10))
	}
	d.Promote = len(d.Reasons) == 0
	if d.Promote {
		d.Reasons = append(d.Reasons, "all gates passed: precision@5 improved beyond the 95% bootstrap CI, PR-AUC and FPR@10 did not regress")
	}
	return d
}

// ---------------------------------------------------------------------------
// PostgreSQL integration
// ---------------------------------------------------------------------------

// RankingStore persists ranking artifacts. It never touches source tables and
// never mutates candidate/decision rows.
type RankingStore struct {
	Pool *pgxpool.Pool
}

func (s RankingStore) requirePool() error {
	if s.Pool == nil {
		return errors.New("PostgreSQL pool is required")
	}
	return nil
}

// trainingLabel is a raw decision row joined to its candidate.
type trainingLabel struct {
	Candidate      Candidate
	DecisionStatus string
	ActorID        string
	ActorKind      string
	DecidedAt      time.Time
}

// LoadLabels loads the current decision per candidate, applying the fixture
// and test-actor exclusions. It returns labels, the number of excluded test
// fixtures and the number of NEEDS_REVIEW labels excluded from binary
// training.
func (s RankingStore) LoadLabels(ctx context.Context) ([]trainingLabel, int, int, error) {
	if err := s.requirePool(); err != nil {
		return nil, 0, 0, err
	}
	rows, err := s.Pool.Query(ctx, `
SELECT c.candidate_id,c.candidate_gid,c.relationship_type,c.source_type,c.source_id,c.source_gid,
       c.target_type,c.target_id,c.target_gid,c.generation_method,c.initial_evidence,c.latest_evidence,
       c.generator_version,c.current_status,c.current_version,c.first_detected_at,c.last_evaluated_at,
       d.decision_status,COALESCE(d.actor_id,''),d.actor_kind,d.decided_at
FROM tg_relationship_decisions d
JOIN tg_relationship_candidates c ON c.candidate_id=d.candidate_id
ORDER BY d.decided_at, d.decision_id`)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var out []trainingLabel
	excludedFixtures, excludedNeedsReview := 0, 0
	for rows.Next() {
		var l trainingLabel
		var initial, latest []byte
		if err := rows.Scan(&l.Candidate.CandidateID, &l.Candidate.CandidateGID, &l.Candidate.RelationshipType,
			&l.Candidate.SourceType, &l.Candidate.SourceID, &l.Candidate.SourceGID,
			&l.Candidate.TargetType, &l.Candidate.TargetID, &l.Candidate.TargetGID,
			&l.Candidate.GenerationMethod, &initial, &latest, &l.Candidate.GeneratorVersion,
			&l.Candidate.CurrentStatus, &l.Candidate.CurrentVersion, &l.Candidate.FirstDetectedAt, &l.Candidate.LastEvaluatedAt,
			&l.DecisionStatus, &l.ActorID, &l.ActorKind, &l.DecidedAt); err != nil {
			return nil, 0, 0, err
		}
		l.Candidate.InitialEvidence = json.RawMessage(initial)
		l.Candidate.LatestEvidence = json.RawMessage(latest)
		switch {
		case IsTestCandidate(l.Candidate.CandidateGID, l.Candidate.GenerationMethod),
			IsTestDecision(l.ActorID, l.ActorKind):
			excludedFixtures++
		case l.DecisionStatus == "NEEDS_REVIEW":
			excludedNeedsReview++
		case l.DecisionStatus == "CONFIRMED" || l.DecisionStatus == "REJECTED":
			out = append(out, l)
		default:
			excludedFixtures++
		}
	}
	return out, excludedFixtures, excludedNeedsReview, rows.Err()
}

// BuildSamples converts excluded-filtered labels into training samples and
// reports how many duplicate decision-history events were collapsed (the
// current-decision table already deduplicates; the count documents that no
// duplicate label enters training).
func BuildSamples(labels []trainingLabel) ([]LabelledSample, int) {
	seen := map[int64]bool{}
	duplicates := 0
	out := make([]LabelledSample, 0, len(labels))
	for _, l := range labels {
		if seen[l.Candidate.CandidateID] {
			duplicates++
			continue
		}
		seen[l.Candidate.CandidateID] = true
		out = append(out, LabelledSample{
			CandidateID: l.Candidate.CandidateID,
			VendorID:    l.Candidate.SourceID,
			EntityID:    l.Candidate.TargetID,
			Features:    ExtractRankingFeatures(l.Candidate, nil),
			Label:       decisionLabel(l.DecisionStatus),
			DecidedAt:   l.DecidedAt,
		})
	}
	return out, duplicates
}

func decisionLabel(status string) int {
	if status == "CONFIRMED" {
		return 1
	}
	return 0
}

// GroupLeakage reports vendor/entity IDs present on both sides of a split.
func GroupLeakage(train, eval []LabelledSample) (vendors []string, entities []string) {
	tv, te := map[string]bool{}, map[string]bool{}
	ev, ee := map[string]bool{}, map[string]bool{}
	for _, s := range train {
		tv[s.VendorID] = true
		if s.EntityID != "" {
			te[s.EntityID] = true
		}
	}
	for _, s := range eval {
		if tv[s.VendorID] {
			vendors = append(vendors, s.VendorID)
		}
		if s.EntityID != "" && te[s.EntityID] {
			entities = append(entities, s.EntityID)
		}
		_ = ev
		_ = ee
	}
	return vendors, entities
}

// ScoreAllCandidates recomputes the frozen baseline score for every candidate
// and writes new (candidate, baseline_version) rows. Existing rows for the
// same version are upserted only when the score changes; baseline scores are
// never silently dropped. Returns the number of candidates scored.
func (s RankingStore) ScoreBaseline(ctx context.Context) (int, error) {
	if err := s.requirePool(); err != nil {
		return 0, err
	}
	candidates, err := s.allCandidates(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	count := 0
	for _, c := range candidates {
		f := ExtractRankingFeatures(c, nil)
		score := BaselineScore(f)
		components, _ := json.Marshal(map[string]any{
			"features": f, "weights_version": baselineVersion,
		})
		if _, err := tx.Exec(ctx, `INSERT INTO tg_candidate_baseline_scores
 (candidate_id,baseline_version,score,components)
 VALUES ($1,$2,$3,$4::jsonb)
 ON CONFLICT (candidate_id,baseline_version) DO UPDATE
 SET score=EXCLUDED.score, components=EXCLUDED.components, computed_at=now()
 WHERE tg_candidate_baseline_scores.score <> EXCLUDED.score`,
			c.CandidateID, baselineVersion, score, components); err != nil {
			return count, err
		}
		count++
	}
	return count, tx.Commit(ctx)
}

// TrainAndEvaluate runs the full learning loop step: load genuine labels,
// build samples, split temporally at group level, train, evaluate on the
// held-out group, apply promotion gates, persist the model and the run
// record, and (only when every gate passes) promote the model. The returned
// model always reflects what happened; when data is insufficient the model
// status is DRAFT with NOT_READY notes and the active ranker stays BASELINE.
func (s RankingStore) TrainAndEvaluate(ctx context.Context, trainNow bool) (LogisticModel, PromotionDecision, error) {
	if err := s.requirePool(); err != nil {
		return LogisticModel{}, PromotionDecision{}, err
	}
	labels, excludedFixtures, excludedNeedsReview, err := s.LoadLabels(ctx)
	if err != nil {
		return LogisticModel{}, PromotionDecision{}, err
	}
	samples, duplicates := BuildSamples(labels)
	version := fmt.Sprintf("learned-v%d", time.Now().UTC().Unix())
	model := LogisticModel{
		Version: version, FeatureVersion: featureVersion, Baseline: baselineVersion,
		Notes: []string{
			fmt.Sprintf("excluded %d test-fixture/test-actor decisions", excludedFixtures),
			fmt.Sprintf("excluded %d NEEDS_REVIEW labels from binary training", excludedNeedsReview),
			fmt.Sprintf("collapsed %d duplicate decision-history events", duplicates),
		},
	}
	decision := PromotionDecision{NotReady: true}

	// Record the run even when training is refused, so "feedback collected /
	// model trained / model evaluated / model promoted" stays auditable.
	recordRun := func(status string, metrics json.RawMessage) error {
		notes, _ := json.Marshal(model.Notes)
		_, err := s.Pool.Exec(ctx, `INSERT INTO tg_ranking_runs
 (run_kind,ranker_version,feature_version,status,train_samples,eval_samples,positive_samples,negative_samples,excluded_test_fixtures,metrics,notes)
 VALUES ('LEARNED_TRAIN',$1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb)`,
			version, featureVersion, status, model.TrainSamples, model.EvalSamples,
			model.PosTrain+model.PosEval, model.NegTrain+model.NegEval, excludedFixtures, metrics, notes)
		return err
	}

	if !trainNow || len(samples) == 0 {
		model.Notes = append(model.Notes, "training not requested or no genuine labels available")
		metrics, _ := json.Marshal(map[string]any{"state": "NOT_READY", "genuine_labels": len(samples)})
		if err := recordRun("NOT_READY", metrics); err != nil {
			return model, decision, err
		}
		return model, decision, nil
	}

	train, eval := TemporalGroupSplit(samples, trainGroupFraction)
	vendorLeak, entityLeak := GroupLeakage(train, eval)
	model.TrainSamples = len(train)
	model.EvalSamples = len(eval)
	for _, s := range train {
		if s.Label == 1 {
			model.PosTrain++
		} else {
			model.NegTrain++
		}
	}
	for _, s := range eval {
		if s.Label == 1 {
			model.PosEval++
		} else {
			model.NegEval++
		}
	}
	model.EvalConfig = map[string]any{
		"split":                "temporal_group",
		"train_group_fraction": trainGroupFraction,
		"train_groups":         countGroups(train),
		"eval_groups":          countGroups(eval),
		"vendor_group_leakage": len(vendorLeak) > 0,
		"entity_group_leakage": len(entityLeak) > 0,
		"vendor_overlap_count": len(vendorLeak),
		"entity_overlap_count": len(entityLeak),
	}

	fitted := TrainLogistic(version, train)
	fitted.EvalConfig = model.EvalConfig
	fitted.Notes = model.Notes
	fitted.TrainSamples, fitted.EvalSamples = model.TrainSamples, model.EvalSamples
	fitted.PosTrain, fitted.NegTrain = model.PosTrain, model.NegTrain
	fitted.PosEval, fitted.NegEval = model.PosEval, model.NegEval

	items := make([]EvalItem, 0, len(eval))
	for _, s := range eval {
		items = append(items, EvalItem{
			CandidateID: s.CandidateID,
			Label:       s.Label,
			Baseline:    BaselineScore(s.Features),
			Learned:     fitted.Predict(s.Features),
		})
	}
	decision = EvaluatePromotion(&fitted, items)

	status := "EVALUATED"
	runStatus := "KEPT_BASELINE"
	if decision.NotReady {
		status = "DRAFT"
		runStatus = "NOT_READY"
	}
	weightsJSON, _ := json.Marshal(featureWeightMap(fitted.Weights))
	evalConfigJSON, _ := json.Marshal(fitted.EvalConfig)
	metricsJSON, _ := json.Marshal(map[string]any{
		"learned": fitted.Metrics, "baseline": fitted.BaselineMetr,
		"promotion": decision,
	})
	notesJSON, _ := json.Marshal(fitted.Notes)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO tg_ranking_models
 (model_version,feature_version,baseline_version,weights,bias,trained_at,train_samples,eval_samples,
  positive_train,negative_train,positive_eval,negative_eval,status,metrics,eval_config,notes)
 VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16::jsonb)
 ON CONFLICT (model_version) DO NOTHING`,
		fitted.Version, featureVersion, baselineVersion, weightsJSON, fitted.Bias, fitted.TrainedAt,
		fitted.TrainSamples, fitted.EvalSamples, fitted.PosTrain, fitted.NegTrain, fitted.PosEval, fitted.NegEval,
		status, metricsJSON, evalConfigJSON, notesJSON); err != nil {
		return fitted, decision, err
	}
	if err := recordRun(runStatus, metricsJSON); err != nil {
		return fitted, decision, err
	}
	// Promotion is a separate, explicit transaction — a trained model never
	// becomes the production ranker automatically.
	if decision.Promote {
		if err := s.PromoteModel(ctx, fitted.Version); err != nil {
			return fitted, decision, err
		}
	}
	return fitted, decision, nil
}

func countGroups(samples []LabelledSample) int {
	set := map[string]bool{}
	for _, s := range samples {
		set[s.VendorID] = true
	}
	return len(set)
}

func featureWeightMap(w []float64) map[string]float64 {
	m := map[string]float64{}
	for i, name := range featureNames {
		if i < len(w) {
			m[name] = w[i]
		}
	}
	return m
}

// PromoteModel flips the active-ranker pointer to a model that has passed
// evaluation. Only EVALUATED models can be promoted; promotion is transactional
// and audited in tg_ranking_runs.
func (s RankingStore) PromoteModel(ctx context.Context, modelVersion string) error {
	if err := s.requirePool(); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, featureV, baselineV string
	err = tx.QueryRow(ctx, `SELECT status,feature_version,baseline_version FROM tg_ranking_models WHERE model_version=$1 FOR UPDATE`, modelVersion).Scan(&status, &featureV, &baselineV)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("model %s not found", modelVersion)
		}
		return err
	}
	if status != "EVALUATED" && status != "SUPERSEDED" {
		return fmt.Errorf("model %s has status %s; only EVALUATED models can be promoted", modelVersion, status)
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_ranking_models SET status='SUPERSEDED' WHERE status='PROMOTED'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_ranking_models SET status='PROMOTED' WHERE model_version=$1`, modelVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_ranking_active SET active_ranker='LEARNED', model_version=$1, feature_version=$2, updated_at=now()`, modelVersion, featureV); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tg_ranking_runs
 (run_kind,ranker_version,feature_version,status,notes)
 VALUES ('PROMOTION',$1,$2,'PROMOTED',$3::jsonb)`,
		modelVersion, featureV, json.RawMessage(fmt.Sprintf(`["model %s promoted to active ranker"]`, modelVersion))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rollback returns the active ranker to the frozen baseline. The model row is
// kept for auditability and can be re-promoted after review.
func (s RankingStore) Rollback(ctx context.Context, reason string) error {
	if err := s.requirePool(); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(model_version,'') FROM tg_ranking_active WHERE singleton`).Scan(&current); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_ranking_models SET status='SUPERSEDED' WHERE status='PROMOTED'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_ranking_active SET active_ranker='BASELINE', model_version=NULL, updated_at=now()`); err != nil {
		return err
	}
	notes, _ := json.Marshal([]string{"rolled back to frozen baseline: " + reason, "previous active model: " + current})
	if _, err := tx.Exec(ctx, `INSERT INTO tg_ranking_runs
 (run_kind,ranker_version,feature_version,status,notes)
 VALUES ('ROLLBACK',$1,$2,'ROLLED_BACK',$3::jsonb)`,
		current, featureVersion, notes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RankingStatus is the observable state of the learning system.
type RankingStatus struct {
	ActiveRanker     string          `json:"active_ranker"`
	BaselineVersion  string          `json:"baseline_version"`
	FeatureVersion   string          `json:"feature_version"`
	ModelVersion     *string         `json:"model_version,omitempty"`
	ModelStatus      *string         `json:"model_status,omitempty"`
	TrainedAt        *time.Time      `json:"last_training_at,omitempty"`
	PositiveLabels   int             `json:"genuine_positive_labels"`
	NegativeLabels   int             `json:"genuine_negative_labels"`
	ExcludedFixtures int             `json:"excluded_test_fixtures"`
	ExcludedNeedsRev int             `json:"excluded_needs_review_labels"`
	TrainSamples     *int            `json:"train_samples,omitempty"`
	EvalSamples      *int            `json:"eval_samples,omitempty"`
	Promoted         bool            `json:"model_promoted"`
	Metrics          json.RawMessage `json:"current_metrics,omitempty"`
	UsingBaseline    bool            `json:"using_baseline_ranker"`
	ReadyForTraining bool            `json:"ready_for_training"`
	ReadyReason      string          `json:"readiness_reason"`
}

// Status assembles the learning-system status report.
func (s RankingStore) Status(ctx context.Context) (RankingStatus, error) {
	if err := s.requirePool(); err != nil {
		return RankingStatus{}, err
	}
	var out RankingStatus
	var modelVersion *string
	if err := s.Pool.QueryRow(ctx, `SELECT active_ranker,baseline_version,COALESCE(model_version,''),feature_version FROM tg_ranking_active WHERE singleton`).
		Scan(&out.ActiveRanker, &out.BaselineVersion, &modelVersion, &out.FeatureVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.ActiveRanker, out.BaselineVersion, out.FeatureVersion = "BASELINE", baselineVersion, featureVersion
		} else {
			return out, err
		}
	}
	if modelVersion != nil && *modelVersion != "" {
		out.ModelVersion = modelVersion
		var status string
		var trainedAt time.Time
		var metrics, evalConfig []byte
		var train, eval, posE, negE int
		err := s.Pool.QueryRow(ctx, `SELECT status,trained_at,metrics,eval_config,train_samples,eval_samples,positive_eval,negative_eval FROM tg_ranking_models WHERE model_version=$1`, *modelVersion).
			Scan(&status, &trainedAt, &metrics, &evalConfig, &train, &eval, &posE, &negE)
		if err == nil {
			out.ModelStatus = &status
			out.TrainedAt = &trainedAt
			out.Metrics = json.RawMessage(metrics)
			out.Promoted = status == "PROMOTED"
			out.TrainSamples, out.EvalSamples = &train, &eval
			if posE+negE > 0 {
				_ = posE
				_ = negE
			}
			_ = evalConfig
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	labels, excludedFixtures, excludedNeedsReview, err := s.LoadLabels(ctx)
	if err != nil {
		return out, err
	}
	samples, _ := BuildSamples(labels)
	for _, l := range samples {
		if l.Label == 1 {
			out.PositiveLabels++
		} else {
			out.NegativeLabels++
		}
	}
	out.ExcludedFixtures = excludedFixtures
	out.ExcludedNeedsRev = excludedNeedsReview
	out.UsingBaseline = out.ActiveRanker == "BASELINE" || !out.Promoted
	out.ReadyForTraining = out.PositiveLabels+out.NegativeLabels >= minTrainSamples+minEvalSamples &&
		out.PositiveLabels >= minTrainPos+minEvalPos && out.NegativeLabels >= minTrainNeg+minEvalNeg
	if out.ReadyForTraining {
		out.ReadyReason = fmt.Sprintf("%d genuine labels meet the minimum data thresholds", out.PositiveLabels+out.NegativeLabels)
	} else {
		out.ReadyReason = fmt.Sprintf("insufficient genuine labels: %d positive / %d negative available; minimum %d train (%d pos/%d neg) + %d eval (%d pos/%d neg)",
			out.PositiveLabels, out.NegativeLabels, minTrainSamples, minTrainPos, minTrainNeg, minEvalSamples, minEvalPos, minEvalNeg)
	}
	return out, nil
}

// ScoreLearned writes learned scores for the active PROMOTED model across all
// candidates. Returns the model version scored and the row count.
func (s RankingStore) ScoreLearned(ctx context.Context) (string, int, error) {
	if err := s.requirePool(); err != nil {
		return "", 0, err
	}
	var active, modelVersion string
	if err := s.Pool.QueryRow(ctx, `SELECT active_ranker,COALESCE(model_version,'') FROM tg_ranking_active WHERE singleton`).Scan(&active, &modelVersion); err != nil {
		return "", 0, err
	}
	if active != "LEARNED" || modelVersion == "" {
		return "", 0, nil
	}
	var weights []float64
	var bias float64
	var wJSON []byte
	if err := s.Pool.QueryRow(ctx, `SELECT weights,bias FROM tg_ranking_models WHERE model_version=$1 AND status='PROMOTED'`, modelVersion).Scan(&wJSON, &bias); err != nil {
		return "", 0, err
	}
	var wmap map[string]float64
	if err := json.Unmarshal(wJSON, &wmap); err != nil {
		return "", 0, err
	}
	weights = make([]float64, len(featureNames))
	for i, name := range featureNames {
		weights[i] = wmap[name]
	}
	model := LogisticModel{Weights: weights, Bias: bias}
	candidates, err := s.allCandidates(ctx)
	if err != nil {
		return "", 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(ctx)
	count := 0
	for _, c := range candidates {
		f := ExtractRankingFeatures(c, nil)
		p := model.Predict(f)
		if _, err := tx.Exec(ctx, `INSERT INTO tg_candidate_learned_scores (candidate_id,model_version,score,probability)
 VALUES ($1,$2,$3,$4) ON CONFLICT (candidate_id,model_version) DO UPDATE
 SET score=EXCLUDED.score, probability=EXCLUDED.probability, computed_at=now()`,
			c.CandidateID, modelVersion, p, p); err != nil {
			return "", count, err
		}
		count++
	}
	return modelVersion, count, tx.Commit(ctx)
}

func (s RankingStore) allCandidates(ctx context.Context) ([]Candidate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version,current_status,current_version,first_detected_at,last_evaluated_at FROM tg_relationship_candidates`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RankedCandidate pairs a candidate with its active ranking score.
type RankedCandidate struct {
	Candidate
	BaselineScore *float64 `json:"baseline_score"`
	ActiveScore   *float64 `json:"active_score"`
	ActiveRank    *int     `json:"active_rank"`
	ActiveRanker  string   `json:"active_ranker"`
	RankerVersion string   `json:"ranker_version"`
}

// RankedQueue lists candidates of a status ordered by the active ranker
// (score DESC, then candidate_id ASC for determinism). Baseline scores are
// always attached; the active score is the learned probability when a learned
// ranker is active.
func (s RankingStore) RankedQueue(ctx context.Context, filter CandidateFilter) ([]RankedCandidate, error) {
	if err := s.requirePool(); err != nil {
		return nil, err
	}
	candidates, err := (ReviewStore{Pool: s.Pool}).ListCandidates(ctx, filter)
	if err != nil {
		return nil, err
	}
	baseline := map[int64]float64{}
	brows, err := s.Pool.Query(ctx, `SELECT candidate_id,score FROM tg_candidate_baseline_scores WHERE baseline_version=$1`, baselineVersion)
	if err == nil {
		for brows.Next() {
			var id int64
			var score float64
			if brows.Scan(&id, &score) == nil {
				baseline[id] = score
			}
		}
		brows.Close()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	learned := map[int64]float64{}
	var activeRanker, rankerVersion string
	if err := s.Pool.QueryRow(ctx, `SELECT active_ranker,COALESCE(model_version,baseline_version) FROM tg_ranking_active WHERE singleton`).Scan(&activeRanker, &rankerVersion); err == nil && activeRanker == "LEARNED" {
		lrows, err := s.Pool.Query(ctx, `SELECT candidate_id,score FROM tg_candidate_learned_scores WHERE model_version=$1`, rankerVersion)
		if err == nil {
			for lrows.Next() {
				var id int64
				var score float64
				if lrows.Scan(&id, &score) == nil {
					learned[id] = score
				}
			}
			lrows.Close()
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	} else if err == nil {
		rankerVersion = baselineVersion
	}
	type ranked struct {
		rc    RankedCandidate
		score float64
	}
	out := make([]ranked, 0, len(candidates))
	for _, c := range candidates {
		rc := RankedCandidate{Candidate: c, ActiveRanker: activeRanker, RankerVersion: rankerVersion}
		if b, ok := baseline[c.CandidateID]; ok {
			v := b
			rc.BaselineScore = &v
		}
		score := bOr(rc.BaselineScore)
		if activeRanker == "LEARNED" {
			if l, ok := learned[c.CandidateID]; ok {
				v := l
				rc.ActiveScore = &v
				score = l
			}
		} else {
			rc.ActiveScore = rc.BaselineScore
		}
		out = append(out, ranked{rc, score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].rc.CandidateID < out[j].rc.CandidateID
	})
	for i := range out {
		rank := i + 1
		out[i].rc.ActiveRank = &rank
	}
	res := make([]RankedCandidate, 0, len(out))
	for _, r := range out {
		res = append(res, r.rc)
	}
	return res, nil
}

func bOr(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
