// graphbench executes the Phase 9 graph-value benchmark workloads against
// HydraDB, PostgreSQL and a Go application-level traversal over PostgreSQL.
//
// Rules honoured by every workload:
//
//   - identical starting records, output requirements, limits and dataset for
//     all three implementations;
//   - every database statement is read-only (SELECT / MATCH only);
//   - identical result sets are verified across implementations before any
//     timing is reported;
//   - cold and warm latency are reported separately; p50/p95/p99 are only
//     reported when the sample count supports them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type LatencyStats struct {
	Samples          int      `json:"samples"`
	ColdMS           float64  `json:"cold_ms"`
	WarmMeanMS       float64  `json:"warm_mean_ms"`
	WarmMedianMS     float64  `json:"warm_p50_ms"`
	P95MS            *float64 `json:"warm_p95_ms,omitempty"`
	P99MS            *float64 `json:"warm_p99_ms,omitempty"`
	MinMS            float64  `json:"warm_min_ms"`
	MaxMS            float64  `json:"warm_max_ms"`
	PercentilesValid bool     `json:"percentiles_valid"`
}

type ImplResult struct {
	Implementation  string       `json:"implementation"`
	StatementsPerOp int          `json:"statements_per_op"`
	Latency         LatencyStats `json:"latency"`
	RowsReturned    int          `json:"rows_returned"`
	NodesVisited    int          `json:"nodes_visited"`
	EdgesTraversed  int          `json:"edges_traversed"`
	ThroughputOps   *float64     `json:"throughput_ops_per_sec,omitempty"`
	ResultKey       string       `json:"result_key"`
	Equivalent      *bool        `json:"result_equivalent_to_reference,omitempty"`
	Notes           []string     `json:"notes,omitempty"`
}

type WorkloadResult struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	Inputs           map[string]any `json:"inputs"`
	ReferenceSummary string         `json:"reference_result_summary"`
	Impls            []ImplResult   `json:"implementations"`
	Equivalent       bool           `json:"all_implementations_equivalent"`
	EquivalenceNotes []string       `json:"equivalence_notes"`
}

type BenchReport struct {
	StartedAt       time.Time        `json:"started_at"`
	Environment     map[string]any   `json:"environment"`
	GraphCounts     map[string]any   `json:"graph_counts"`
	StartingRecords map[string]any   `json:"starting_records"`
	Workloads       []WorkloadResult `json:"workloads"`
	Memory          map[string]any   `json:"memory_observations,omitempty"`
	FinishedAt      time.Time        `json:"finished_at"`
}

// opResult is one execution's cost and identity fingerprint.
type opResult struct {
	rows           int
	nodes          int
	edges          int
	statements     int
	keyFingerprint string
}

// op is a single read-only execution of one workload for one implementation.
type op func(context.Context) (opResult, error)

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

var benchSamples = 30

func runImpl(ctx context.Context, wl *WorkloadResult, implName string, workloadOp op, refFingerprint *string, validFrom int) {
	// Cold pass: first execution in this process (connection + cache state
	// are whatever the process sees; the cold number is reported separately
	// and never mixed into warm percentiles).
	coldStart := time.Now()
	coldRes, coldErr := workloadOp(ctx)
	coldMS := float64(time.Since(coldStart).Microseconds()) / 1000.0
	if coldErr != nil {
		wl.Equivalent = false
		wl.EquivalenceNotes = append(wl.EquivalenceNotes, fmt.Sprintf("%s cold execution failed: %v", implName, coldErr))
		wl.Impls = append(wl.Impls, ImplResult{Implementation: implName, Notes: []string{"cold execution failed: " + coldErr.Error()}})
		return
	}
	warmTimings := make([]float64, 0, benchSamples)
	last := coldRes
	var firstErr error
	for i := 0; i < benchSamples; i++ {
		s := time.Now()
		r, err := workloadOp(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		warmTimings = append(warmTimings, float64(time.Since(s).Microseconds())/1000.0)
		last = r
	}
	st := latencyStats(coldMS, warmTimings, validFrom)
	ir := ImplResult{
		Implementation:  implName,
		StatementsPerOp: last.statements,
		Latency:         st,
		RowsReturned:    last.rows,
		NodesVisited:    last.nodes,
		EdgesTraversed:  last.edges,
		ResultKey:       last.keyFingerprint,
	}
	if firstErr != nil {
		ir.Notes = append(ir.Notes, fmt.Sprintf("stopped after %d warm samples: %v", len(warmTimings), firstErr))
	}
	if st.Samples > 0 {
		tp := 1000.0 / st.WarmMedianMS
		ir.ThroughputOps = &tp
	}
	if *refFingerprint == "" {
		// First implementation defines the reference result.
		*refFingerprint = last.keyFingerprint
		wl.ReferenceSummary = summarize(last)
		ir.Equivalent = boolPtr(true)
	} else {
		eq := last.keyFingerprint == *refFingerprint
		ir.Equivalent = &eq
		if !eq {
			wl.Equivalent = false
			wl.EquivalenceNotes = append(wl.EquivalenceNotes,
				fmt.Sprintf("%s result differs from reference: %s vs %s", implName, truncateKey(last.keyFingerprint), truncateKey(*refFingerprint)))
		}
	}
	wl.Impls = append(wl.Impls, ir)
}

func truncateKey(k string) string {
	if len(k) > 160 {
		return k[:160] + "…(" + strconv.Itoa(len(k)) + " bytes)"
	}
	return k
}

func summarize(r opResult) string {
	return fmt.Sprintf("rows=%d nodes=%d edges=%d key=%s", r.rows, r.nodes, r.edges, truncateKey(r.keyFingerprint))
}

func latencyStats(coldMS float64, warm []float64, validFrom int) LatencyStats {
	s := LatencyStats{Samples: len(warm), ColdMS: coldMS, PercentilesValid: len(warm) >= validFrom}
	if len(warm) == 0 {
		return s
	}
	sorted := append([]float64(nil), warm...)
	sort.Float64s(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	s.WarmMeanMS = sum / float64(len(sorted))
	s.WarmMedianMS = pct(sorted, 0.5)
	s.MinMS = sorted[0]
	s.MaxMS = sorted[len(sorted)-1]
	if s.PercentilesValid {
		p95 := pct(sorted, 0.95)
		p99 := pct(sorted, 0.99)
		s.P95MS = &p95
		s.P99MS = &p99
	}
	return s
}

func pct(sorted []float64, p float64) float64 {
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func boolPtr(v bool) *bool { return &v }

func fingerprintStrings(v []string) string {
	c := append([]string(nil), v...)
	sort.Strings(c)
	b, _ := json.Marshal(c)
	return string(b)
}

func fingerprintInts(v []int64) string {
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	b, _ := json.Marshal(c)
	return string(b)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	samples := flag.Int("samples", 30, "warm samples per workload (percentiles valid at >= 20)")
	out := flag.String("out", "", "output JSON path (default docs/phase9/bench/graph/results.json)")
	only := flag.String("workloads", "all", "comma-separated workload ids or 'all'")
	start := flag.String("bids", "1760,1710,1711", "comma-separated starting participant record ids")
	flag.Parse()
	benchSamples = *samples
	if *out == "" {
		*out = "../docs/phase9/bench/graph/results.json"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, gc, err := connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()
	defer gc.Close(context.Background())

	primary, _ := strconv.ParseInt(strings.Split(*start, ",")[0], 10, 64)

	report := BenchReport{
		StartedAt:   time.Now().UTC(),
		Environment: environment(),
		GraphCounts: graphCounts(ctx, gc.HTTP, primary),
	}
	report.Memory = map[string]any{"source_table_counts": datasetCounts(ctx, pool)}
	report.StartingRecords = loadStartingRecords(ctx, pool, *start)

	wl := strings.Split(strings.ToLower(*only), ",")
	want := func(id string) bool {
		if strings.TrimSpace(*only) == "all" {
			return true
		}
		for _, w := range wl {
			if strings.TrimSpace(w) == id {
				return true
			}
		}
		return false
	}

	if want("wa") {
		report.Workloads = append(report.Workloads, workloadOneHop(ctx, pool, gc.HTTP, primary))
	}
	if want("wb2") {
		report.Workloads = append(report.Workloads, workloadMultiHop(ctx, pool, gc.HTTP, primary, 2))
	}
	if want("wb3") {
		report.Workloads = append(report.Workloads, workloadMultiHop(ctx, pool, gc.HTTP, primary, 3))
	}
	if want("wc") {
		left, right := pairFromStart(*start)
		report.Workloads = append(report.Workloads, workloadPair(ctx, pool, gc.HTTP, left, right))
	}
	if want("wd") {
		report.Workloads = append(report.Workloads, workloadEvidence(ctx, pool, gc.HTTP, primary))
	}
	if want("we") {
		report.Workloads = append(report.Workloads, workloadCorporate(ctx, pool, gc.HTTP))
	}

	report.FinishedAt = time.Now().UTC()
	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func pairFromStart(start string) (int64, int64) {
	parts := strings.Split(start, ",")
	if len(parts) < 3 {
		return 1710, 1711
	}
	l, _ := strconv.ParseInt(parts[1], 10, 64)
	r, _ := strconv.ParseInt(parts[2], 10, 64)
	return l, r
}

func connect() (*pgxpool.Pool, *graph.Clients, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	config := loadConfig()
	if config.DatabaseURL == "" {
		return nil, nil, fmt.Errorf("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, config.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: %w", err)
	}
	gc, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("hydradb: %w", err)
	}
	return pool, gc, nil
}

func graphCounts(ctx context.Context, g *graph.HTTPClient, primary int64) map[string]any {
	out := map[string]any{}
	pk := strconv.FormatInt(primary, 10)
	q := `MATCH (start:Bid {src_pk: $pk})-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid) WHERE other.src_pk <> $pk RETURN count(*) AS c`
	if res, err := g.DoQuery(ctx, q, map[string]any{"pk": pk}); err == nil && res.Count() > 0 {
		if v, err := res.Int64(0, 0); err == nil {
			out["co_bid_paths_for_starting_bid"] = v
		}
	}
	out["full_label_count_note"] = "MATCH (n:Bid) RETURN count(*) exceeds the 30s query timeout on this HydraDB build (legacy Cypher engine); unbounded full-graph scans are excluded from the benchmark by design."
	return out
}
