package main

// workload.go defines the six read-only benchmark workloads (W_A..W_E, with
// W_B at depth 2 and 3) and their three implementations:
//
//	hydradb  - native HydraDB Cypher / algo.* procedures (one statement where
//	           the engine supports it);
//	postgres - pure relational SQL (joins and a recursive CTE);
//	go-app   - application-level traversal issued from Go (per-node queries,
//	           set intersection in process memory).
//
// Every implementation of a workload must return the identical result set
// before timings are compared. All statements are SELECT / MATCH / CALL
// (read-only); nothing in this file writes to any store.

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/projection"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Shared workload parameters. Identical for all three implementations.
const (
	oneHopLimit    = 200 // max co-bidder records returned (W_A)
	multiHopLimit  = 200 // max distinct reached records (W_B)
	pairLimit      = 50  // max shared tenders returned (W_C)
	evidenceBids   = 2   // evidence chain lookups per op (W_D)
	gstLimit       = 64  // max GST registrations returned (W_E)
	pathCountCap   = 1024
	percentileFrom = 20 // warm samples required before p95/p99 are reported
)

// entityUUID is a real entity with GST registrations (Bank of Maharashtra,
// 64 entity_gst rows, all projected into HydraDB with HAS_GST_REGISTRATION
// edges). Chosen from entities JOIN entity_gst ordered by row count.
const entityUUID = "e3818456-e1d5-4faf-888f-9692a8dbfc63"

// ---------------------------------------------------------------------------
// Environment and configuration
// ---------------------------------------------------------------------------

// loadConfig resolves DATABASE_URL from the environment / .env file.
func loadConfig() cfg.Config { return cfg.Load() }

func environment() map[string]any {
	out := map[string]any{
		"go_version":    runtime.Version(),
		"os":            runtime.GOOS + "/" + runtime.GOARCH,
		"generated_by":  "cmd/graphbench",
		"started_utc":   time.Now().UTC().Format(time.RFC3339),
		"fairness_note": "identical starting records, limits and result requirements for all implementations; all statements read-only; cold pass excluded from warm percentiles",
	}
	if c := graph.LoadGraphConfig(); c.AuthToken != "" {
		out["hydradb"] = c.String() // token redacted by Config.String()
	}
	return out
}

// ---------------------------------------------------------------------------
// W_A: one-hop co-bidders of one tender_participants record
// ---------------------------------------------------------------------------

func workloadOneHop(ctx context.Context, pool *pgxpool.Pool, h *graph.HTTPClient, bidPK int64) WorkloadResult {
	wl := WorkloadResult{
		ID:    "wa_one_hop",
		Title: "W_A - one-hop co-bidders: all other tender_participants records on the same tender as the starting record (limit 200)",
		Inputs: map[string]any{
			"bid_pk":     bidPK,
			"limit":      oneHopLimit,
			"definition": "same_tender_participant_pair, one Cypher hop = Bid -> CO_BID_IN -> CoBidObservation <- CO_BID_IN <- Bid",
		},
		Equivalent: true,
	}
	var ref string
	runImpl(ctx, &wl, "hydradb", hydraOneHop(ctx, h, bidPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "postgres", postgresOneHop(ctx, pool, bidPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "go-app", goAppOneHop(ctx, pool, bidPK), &ref, percentileFrom)
	return wl
}

func hydraOneHop(ctx context.Context, h *graph.HTTPClient, bidPK int64) op {
	// HydraDB node properties are STRING-typed for source keys: src_pk must be
	// passed as a string (verified against build 0.1.0+916637477f3f).
	q := `MATCH (start:Bid)-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid)
WHERE start.src_pk = $bid_pk AND other.src_pk <> $bid_pk
RETURN DISTINCT other.src_pk AS bid_pk ORDER BY bid_pk LIMIT $limit`
	return func(ctx context.Context) (opResult, error) {
		res, err := h.DoQuery(ctx, q, map[string]any{"bid_pk": fmt.Sprint(bidPK), "limit": oneHopLimit})
		if err != nil {
			return opResult{}, fmt.Errorf("hydradb one-hop: %w", err)
		}
		keys := make([]string, 0, res.Count())
		for i := range res.Rows {
			s, err := res.String(i, 0)
			if err != nil {
				return opResult{}, err
			}
			keys = append(keys, s)
		}
		return opResult{
			rows:           res.Count(),
			nodes:          res.Count() + 1,
			edges:          2 * res.Count(),
			statements:     1,
			keyFingerprint: fingerprintStrings(keys),
		}, nil
	}
}

func postgresOneHop(ctx context.Context, pool *pgxpool.Pool, bidPK int64) op {
	q := `SELECT DISTINCT p2.id::bigint AS bid_pk
FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id = p1.tender_id
WHERE p1.id = $1 AND p2.id <> $1
ORDER BY bid_pk LIMIT $2`
	return func(ctx context.Context) (opResult, error) {
		rows, err := pool.Query(ctx, q, bidPK, oneHopLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("postgres one-hop: %w", err)
		}
		defer rows.Close()
		keys := []string{}
		n := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return opResult{}, err
			}
			keys = append(keys, strconv.FormatInt(id, 10))
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		return opResult{rows: n, nodes: n + 1, edges: n, statements: 1, keyFingerprint: fingerprintStrings(keys)}, nil
	}
}

// goAppOneHop is the application-level equivalent: an application that already
// holds one participant record issues a second lookup by tender_id and filters
// in process memory (two statements, no SQL join).
func goAppOneHop(ctx context.Context, pool *pgxpool.Pool, bidPK int64) op {
	return func(ctx context.Context) (opResult, error) {
		var tenderID int64
		if err := pool.QueryRow(ctx, `SELECT tender_id::bigint FROM tender_participants WHERE id=$1`, bidPK).Scan(&tenderID); err != nil {
			return opResult{}, fmt.Errorf("go-app one-hop lookup: %w", err)
		}
		rows, err := pool.Query(ctx, `SELECT id::bigint FROM tender_participants WHERE tender_id=$1 AND id<>$2 ORDER BY id LIMIT $3`, tenderID, bidPK, oneHopLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("go-app one-hop fetch: %w", err)
		}
		defer rows.Close()
		keys := []string{}
		n := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return opResult{}, err
			}
			keys = append(keys, strconv.FormatInt(id, 10))
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		return opResult{rows: n, nodes: n + 1, edges: n, statements: 2, keyFingerprint: fingerprintStrings(keys)}, nil
	}
}

// ---------------------------------------------------------------------------
// W_B: multi-hop co-bidder neighbourhood (bounded BFS to `depth` co-bidder hops)
// ---------------------------------------------------------------------------

func workloadMultiHop(ctx context.Context, pool *pgxpool.Pool, h *graph.HTTPClient, bidPK int64, depth int) WorkloadResult {
	wl := WorkloadResult{
		ID:    fmt.Sprintf("wb_multi_hop_d%d", depth),
		Title: fmt.Sprintf("W_B depth %d - co-bidder neighbourhood: distinct tender_participants records reachable within %d co-bidder hops of the starting record (limit %d)", depth, depth, multiHopLimit),
		Inputs: map[string]any{
			"bid_pk":            bidPK,
			"depth_hops":        depth,
			"hydradb_max_edges": 2 * depth,
			"limit":             multiHopLimit,
		},
		Equivalent: true,
	}
	var ref string
	runImpl(ctx, &wl, "hydradb", hydraMultiHop(ctx, h, bidPK, depth), &ref, percentileFrom)
	runImpl(ctx, &wl, "postgres", postgresMultiHop(ctx, pool, bidPK, depth), &ref, percentileFrom)
	runImpl(ctx, &wl, "go-app", goAppMultiHop(ctx, pool, bidPK, depth), &ref, percentileFrom)
	return wl
}

// hydraMultiHop uses the native SSpaths procedure. relDirection "both" is
// required because every co-bidding edge points Bid -> CoBidObservation, so a
// Bid-to-Bid step through an observation costs two edges: maxLen = 2*depth.
// HydraDB caps pathCount at 1024 paths per response on this build.
func hydraMultiHop(ctx context.Context, h *graph.HTTPClient, bidPK int64, depth int) op {
	src := projection.NumericID(projection.GID("BID", "v", strconv.FormatInt(bidPK, 10)))
	q := `CALL algo.SSpaths({sourceNode: $src, relTypes: ['CO_BID_IN'], relDirection: 'both', maxLen: $maxlen, pathCount: $pathcount}) YIELD path RETURN path`
	pk := strconv.FormatInt(bidPK, 10)
	startGID := "BID:v:" + pk
	return func(ctx context.Context) (opResult, error) {
		res, err := h.DoQuery(ctx, q, map[string]any{
			"src": src, "maxlen": 2 * depth, "pathcount": pathCountCap,
		})
		if err != nil {
			return opResult{}, fmt.Errorf("hydradb SSpaths: %w", err)
		}
		seen := map[string]bool{}
		edges := 0
		for i := range res.Rows {
			path, ok := res.Value(i, 0).(map[string]any)
			if !ok {
				continue
			}
			rawNodes, _ := path["nodes"].([]any)
			for _, rn := range rawNodes {
				node, ok := rn.(map[string]any)
				if !ok {
					continue
				}
				props, _ := node["properties"].(map[string]any)
				gid, _ := props["gid"].(string)
				if gid == "" || !strings.HasPrefix(gid, "BID:") || gid == startGID {
					continue
				}
				seen[gid] = true
			}
			if len(rawNodes) > 1 {
				edges += len(rawNodes) - 1
			}
		}
		keys := make([]string, 0, len(seen))
		for gid := range seen {
			keys = append(keys, strings.TrimPrefix(gid, "BID:v:"))
		}
		if len(keys) > multiHopLimit {
			sort.Strings(keys)
			keys = keys[:multiHopLimit]
		}
		return opResult{
			rows:           len(keys),
			nodes:          len(keys) + 1,
			edges:          edges,
			statements:     1,
			keyFingerprint: fingerprintStrings(keys),
		}, nil
	}
}

// postgresMultiHop is the relational-native equivalent: one recursive CTE.
// UNION (not UNION ALL) keeps the recursion finite on the cyclic co-bidding
// relation.
func postgresMultiHop(ctx context.Context, pool *pgxpool.Pool, bidPK int64, depth int) op {
	q := `WITH RECURSIVE walk AS (
    SELECT id::bigint AS id, 0 AS depth FROM tender_participants WHERE id = $1
  UNION
    SELECT p2.id::bigint, w.depth + 1
    FROM walk w
    JOIN tender_participants p1 ON p1.id = w.id
    JOIN tender_participants p2 ON p2.tender_id = p1.tender_id AND p2.id <> p1.id
    WHERE w.depth < $2
)
SELECT DISTINCT id FROM walk WHERE id <> $1 ORDER BY id LIMIT $3`
	return func(ctx context.Context) (opResult, error) {
		rows, err := pool.Query(ctx, q, bidPK, depth, multiHopLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("postgres recursive CTE: %w", err)
		}
		defer rows.Close()
		keys := []string{}
		n := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return opResult{}, err
			}
			keys = append(keys, strconv.FormatInt(id, 10))
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		return opResult{rows: n, nodes: n + 1, edges: n, statements: 1, keyFingerprint: fingerprintStrings(keys)}, nil
	}
}

// goAppMultiHop runs the same bounded BFS in Go: one query per frontier node
// per hop, de-duplication and cycle prevention in process memory.
func goAppMultiHop(ctx context.Context, pool *pgxpool.Pool, bidPK int64, depth int) op {
	const stepQuery = `SELECT p2.id::bigint FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id = p1.tender_id AND p2.id <> p1.id WHERE p1.id = $1`
	return func(ctx context.Context) (opResult, error) {
		visited := map[int64]bool{bidPK: true}
		frontier := []int64{bidPK}
		statements := 0
		edges := 0
		for d := 0; d < depth && len(visited) <= multiHopLimit; d++ {
			var next []int64
			for _, id := range frontier {
				rows, err := pool.Query(ctx, stepQuery, id)
				if err != nil {
					return opResult{}, fmt.Errorf("go-app BFS step: %w", err)
				}
				statements++
				for rows.Next() {
					var nid int64
					if err := rows.Scan(&nid); err != nil {
						rows.Close()
						return opResult{}, err
					}
					edges++
					if !visited[nid] {
						visited[nid] = true
						next = append(next, nid)
					}
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return opResult{}, err
				}
				if len(visited) > multiHopLimit {
					break
				}
			}
			frontier = next
			if len(frontier) == 0 {
				break
			}
		}
		keys := make([]string, 0, len(visited))
		for id := range visited {
			if id == bidPK {
				continue
			}
			keys = append(keys, strconv.FormatInt(id, 10))
		}
		if len(keys) > multiHopLimit {
			sort.Strings(keys)
			keys = keys[:multiHopLimit]
		}
		return opResult{rows: len(keys), nodes: len(keys) + 1, edges: edges, statements: statements, keyFingerprint: fingerprintStrings(keys)}, nil
	}
}

// ---------------------------------------------------------------------------
// W_C: pair co-bidding history - shared tenders of two participant records
// ---------------------------------------------------------------------------

func workloadPair(ctx context.Context, pool *pgxpool.Pool, h *graph.HTTPClient, leftPK, rightPK int64) WorkloadResult {
	wl := WorkloadResult{
		ID:    "wc_pair_history",
		Title: "W_C - pair co-bidding history: tenders on which the two tender_participants records co-occur (limit 50)",
		Inputs: map[string]any{
			"left_bid_pk":  leftPK,
			"right_bid_pk": rightPK,
			"limit":        pairLimit,
		},
		Equivalent: true,
	}
	var ref string
	runImpl(ctx, &wl, "hydradb", hydraPair(ctx, h, leftPK, rightPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "postgres", postgresPair(ctx, pool, leftPK, rightPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "go-app", goAppPair(ctx, pool, leftPK, rightPK), &ref, percentileFrom)
	return wl
}

func hydraPair(ctx context.Context, h *graph.HTTPClient, leftPK, rightPK int64) op {
	q := `MATCH (a:Bid {src_pk: $l})-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(b:Bid {src_pk: $r})
RETURN o.tender_pk AS tender ORDER BY tender LIMIT $limit`
	return func(ctx context.Context) (opResult, error) {
		res, err := h.DoQuery(ctx, q, map[string]any{
			"l": fmt.Sprint(leftPK), "r": fmt.Sprint(rightPK), "limit": pairLimit,
		})
		if err != nil {
			return opResult{}, fmt.Errorf("hydradb pair: %w", err)
		}
		ids := make([]int64, 0, res.Count())
		for i := range res.Rows {
			v, err := res.Int64(i, 0)
			if err != nil {
				return opResult{}, err
			}
			ids = append(ids, v)
		}
		return opResult{rows: res.Count(), nodes: 3, edges: 2 * res.Count(), statements: 1, keyFingerprint: fingerprintInts(ids)}, nil
	}
}

func postgresPair(ctx context.Context, pool *pgxpool.Pool, leftPK, rightPK int64) op {
	q := `SELECT DISTINCT p1.tender_id::bigint AS tid
FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id = p1.tender_id
WHERE p1.id = $1 AND p2.id = $2
ORDER BY tid LIMIT $3`
	return func(ctx context.Context) (opResult, error) {
		rows, err := pool.Query(ctx, q, leftPK, rightPK, pairLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("postgres pair: %w", err)
		}
		defer rows.Close()
		ids := []int64{}
		n := 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return opResult{}, err
			}
			ids = append(ids, id)
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		return opResult{rows: n, nodes: 3, edges: n, statements: 1, keyFingerprint: fingerprintInts(ids)}, nil
	}
}

// goAppPair resolves each record to its normalized participant key, fetches
// each side's tender set, and intersects the sets in process memory - the
// approach used by the intel.Service Pair analysis.
func goAppPair(ctx context.Context, pool *pgxpool.Pool, leftPK, rightPK int64) op {
	return func(ctx context.Context) (opResult, error) {
		var lkey, rkey string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(participant_normalized,'') FROM tender_participants WHERE id=$1`, leftPK).Scan(&lkey); err != nil {
			return opResult{}, fmt.Errorf("go-app pair left key: %w", err)
		}
		if err := pool.QueryRow(ctx, `SELECT COALESCE(participant_normalized,'') FROM tender_participants WHERE id=$1`, rightPK).Scan(&rkey); err != nil {
			return opResult{}, fmt.Errorf("go-app pair right key: %w", err)
		}
		leftSet := map[int64]bool{}
		rows, err := pool.Query(ctx, `SELECT DISTINCT tender_id::bigint FROM tender_participants WHERE participant_normalized=$1`, lkey)
		if err != nil {
			return opResult{}, fmt.Errorf("go-app pair left tenders: %w", err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return opResult{}, err
			}
			leftSet[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		rightRows, err := pool.Query(ctx, `SELECT DISTINCT tender_id::bigint FROM tender_participants WHERE participant_normalized=$1 ORDER BY tender_id LIMIT $2`, rkey, pairLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("go-app pair right tenders: %w", err)
		}
		defer rightRows.Close()
		ids := []int64{}
		for rightRows.Next() {
			var id int64
			if err := rightRows.Scan(&id); err != nil {
				return opResult{}, err
			}
			if leftSet[id] {
				ids = append(ids, id)
			}
		}
		if err := rightRows.Err(); err != nil {
			return opResult{}, err
		}
		if len(ids) > pairLimit {
			ids = ids[:pairLimit]
		}
		return opResult{rows: len(ids), nodes: 3, edges: len(ids), statements: 3, keyFingerprint: fingerprintInts(ids)}, nil
	}
}

// ---------------------------------------------------------------------------
// W_D: evidence chain - bid record -> its tender -> award evidence
// ---------------------------------------------------------------------------

func workloadEvidence(ctx context.Context, pool *pgxpool.Pool, h *graph.HTTPClient, bidPK int64) WorkloadResult {
	// Bid 1 is a real record with an explicit award: tender 16218, awardee
	// AMAR ENTERPRISES, winning bid number 3428582 (WON edge evidence kind
	// BID_NUMBER_MATCH). The primary bid (1760) has NO award evidence on
	// tender 16813 - the workload intentionally covers both branches.
	inputs := map[string]any{
		"bid_pks":            []int64{bidPK, 1},
		"expected_award_for": []int64{1},
		"note":               "bid 1 tender 16218 has award evidence; primary bid tender has none (L1 selected, AOC pending / no awardee recorded yet)",
	}
	wl := WorkloadResult{
		ID:         "wd_evidence_chain",
		Title:      "W_D - evidence chain: participant record -> tender -> award evidence (award fields or explicit absence)",
		Inputs:     inputs,
		Equivalent: true,
	}
	var ref string
	runImpl(ctx, &wl, "hydradb", hydraEvidence(ctx, h, bidPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "postgres", postgresEvidence(ctx, pool, bidPK), &ref, percentileFrom)
	runImpl(ctx, &wl, "go-app", goAppEvidence(ctx, pool, bidPK), &ref, percentileFrom)
	return wl
}

func hydraEvidence(ctx context.Context, h *graph.HTTPClient, bidPK int64) op {
	tenderQuery := `MATCH (b:Bid {src_pk: $pk})-[:SUBMITTED_BID]->(t:Tender)
RETURN t.src_pk AS tpk, t.tender_id AS ext, t.organization AS org, t.lifecycle_stage AS stage`
	awardQuery := `MATCH (t:Tender {src_pk: $tpk})-[:HAS_AWARD]->(a:Award)
RETURN a.gid AS ag, a.awardee_name AS aw, a.winning_bid_number AS wb, a.contract_date AS cd`
	return func(ctx context.Context) (opResult, error) {
		var fp strings.Builder
		rows, nodes, edges, statements := 0, 0, 0, 0
		for _, pk := range []int64{bidPK, 1} {
			res, err := h.DoQuery(ctx, tenderQuery, map[string]any{"pk": fmt.Sprint(pk)})
			if err != nil {
				return opResult{}, fmt.Errorf("hydradb evidence tender: %w", err)
			}
			statements++
			nodes += res.Count() + 1
			edges += res.Count()
			rows += res.Count()
			if res.Count() == 0 {
				fp.WriteString("bid=" + fmt.Sprint(pk) + ";no-tender\n")
				continue
			}
			fields := make([]string, 4)
			for c := 0; c < 4; c++ {
				s, err := res.String(0, c)
				if err != nil {
					return opResult{}, err
				}
				fields[c] = s
			}
			fp.WriteString(strings.Join(fields, ";") + "\n")
			tpk, err := res.String(0, 0)
			if err != nil {
				return opResult{}, err
			}
			ares, err := h.DoQuery(ctx, awardQuery, map[string]any{"tpk": tpk})
			if err != nil {
				return opResult{}, fmt.Errorf("hydradb evidence award: %w", err)
			}
			statements++
			rows += ares.Count()
			if ares.Count() == 0 {
				fp.WriteString("award=absent\n")
				continue
			}
			nodes += ares.Count()
			edges += ares.Count()
			fields = make([]string, 4)
			for c := 0; c < 4; c++ {
				s, err := ares.String(0, c)
				if err != nil {
					return opResult{}, err
				}
				fields[c] = s
			}
			fp.WriteString(strings.Join(fields, ";") + "\n")
		}
		return opResult{rows: rows, nodes: nodes, edges: edges, statements: statements, keyFingerprint: fp.String()}, nil
	}
}

func postgresEvidence(ctx context.Context, pool *pgxpool.Pool, bidPK int64) op {
	tenderQuery := `SELECT t.id::bigint, t.tender_id, COALESCE(t.organization,''), COALESCE(t.lifecycle_stage,'')
FROM tender_participants p JOIN tenders t ON t.id = p.tender_id WHERE p.id = $1`
	awardQuery := `SELECT m.id::bigint, COALESCE(m.awardee_name,''), COALESCE(m.winning_bid_number,''), COALESCE(m.contract_date::text,'')
FROM tender_extracted_meta m
WHERE m.tender_id = $1 AND (COALESCE(m.awardee_name,'') <> '' OR COALESCE(m.winning_bid_number,'') <> '')
ORDER BY m.id LIMIT 1`
	return func(ctx context.Context) (opResult, error) {
		var fp strings.Builder
		rows, nodes, edges, statements := 0, 0, 0, 0
		for _, pk := range []int64{bidPK, 1} {
			var tid int64
			var ext, org, stage string
			if err := pool.QueryRow(ctx, tenderQuery, pk).Scan(&tid, &ext, &org, &stage); err != nil {
				return opResult{}, fmt.Errorf("postgres evidence tender: %w", err)
			}
			statements++
			rows++
			nodes += 2
			edges++
			fmt.Fprintf(&fp, "%d;%s;%s;%s\n", tid, ext, org, stage)
			var aid int64
			var awardee, winning, contractDate string
			err := pool.QueryRow(ctx, awardQuery, tid).Scan(&aid, &awardee, &winning, &contractDate)
			if err != nil {
				if err.Error() == "no rows in result set" {
					fp.WriteString("award=absent\n")
					continue
				}
				return opResult{}, fmt.Errorf("postgres evidence award: %w", err)
			}
			statements++
			rows++
			nodes++
			edges++
			fmt.Fprintf(&fp, "AWARD:v:%d;%s;%s;%s\n", aid, awardee, winning, contractDate)
		}
		return opResult{rows: rows, nodes: nodes, edges: edges, statements: statements, keyFingerprint: fp.String()}, nil
	}
}

// goAppEvidence navigates the same chain without SQL joins: bid row, then
// tender row by primary key, then award meta row by tender_id (three
// statements per bid).
func goAppEvidence(ctx context.Context, pool *pgxpool.Pool, bidPK int64) op {
	return func(ctx context.Context) (opResult, error) {
		var fp strings.Builder
		rows, nodes, edges, statements := 0, 0, 0, 0
		for _, pk := range []int64{bidPK, 1} {
			var tenderID int64
			if err := pool.QueryRow(ctx, `SELECT tender_id::bigint FROM tender_participants WHERE id=$1`, pk).Scan(&tenderID); err != nil {
				return opResult{}, fmt.Errorf("go-app evidence bid: %w", err)
			}
			statements++
			nodes++
			rows++
			var ext, org, stage string
			if err := pool.QueryRow(ctx, `SELECT tender_id, COALESCE(organization,''), COALESCE(lifecycle_stage,'') FROM tenders WHERE id=$1`, tenderID).Scan(&ext, &org, &stage); err != nil {
				return opResult{}, fmt.Errorf("go-app evidence tender: %w", err)
			}
			statements++
			nodes++
			edges++
			rows++
			fmt.Fprintf(&fp, "%d;%s;%s;%s\n", tenderID, ext, org, stage)
			var aid int64
			var awardee, winning, contractDate string
			err := pool.QueryRow(ctx, `SELECT m.id::bigint, COALESCE(m.awardee_name,''), COALESCE(m.winning_bid_number,''), COALESCE(m.contract_date::text,'')
FROM tender_extracted_meta m WHERE m.tender_id=$1 AND (COALESCE(m.awardee_name,'')<>'' OR COALESCE(m.winning_bid_number,'')<>'') ORDER BY m.id LIMIT 1`, tenderID).Scan(&aid, &awardee, &winning, &contractDate)
			if err != nil {
				if err.Error() == "no rows in result set" {
					fp.WriteString("award=absent\n")
					continue
				}
				return opResult{}, fmt.Errorf("go-app evidence award: %w", err)
			}
			statements++
			rows++
			nodes++
			edges++
			fmt.Fprintf(&fp, "AWARD:v:%d;%s;%s;%s\n", aid, awardee, winning, contractDate)
		}
		return opResult{rows: rows, nodes: nodes, edges: edges, statements: statements, keyFingerprint: fp.String()}, nil
	}
}

// ---------------------------------------------------------------------------
// W_E: corporate identity - entity record and its GST registrations
// ---------------------------------------------------------------------------

func workloadCorporate(ctx context.Context, pool *pgxpool.Pool, h *graph.HTTPClient) WorkloadResult {
	wl := WorkloadResult{
		ID:    "we_corporate_gst",
		Title: "W_E - corporate identity: CorporateEntity record and its GST registrations (candidate identity graph; no confirmed IDENTIFIED_AS edges exist yet)",
		Inputs: map[string]any{
			"entity_uuid":  entityUUID,
			"gst_limit":    gstLimit,
			"relationship": "HAS_GST_REGISTRATION (entity -> GstRegistration)",
		},
		Equivalent: true,
	}
	var ref string
	runImpl(ctx, &wl, "hydradb", hydraCorporate(ctx, h), &ref, percentileFrom)
	runImpl(ctx, &wl, "postgres", postgresCorporate(ctx, pool), &ref, percentileFrom)
	runImpl(ctx, &wl, "go-app", goAppCorporate(ctx, pool), &ref, percentileFrom)
	return wl
}

func hydraCorporate(ctx context.Context, h *graph.HTTPClient) op {
	entityQuery := `MATCH (e:CorporateEntity {src_pk: $id}) RETURN e.gid AS eg, e.canonical_name AS cn, e.normalized_name AS nn`
	gstQuery := `MATCH (e:CorporateEntity {src_pk: $id})-[:HAS_GST_REGISTRATION]->(g:GstRegistration)
RETURN g.gid AS gg, g.gstin AS gstin ORDER BY gg LIMIT $limit`
	return func(ctx context.Context) (opResult, error) {
		eres, err := h.DoQuery(ctx, entityQuery, map[string]any{"id": entityUUID})
		if err != nil {
			return opResult{}, fmt.Errorf("hydradb entity: %w", err)
		}
		gres, err := h.DoQuery(ctx, gstQuery, map[string]any{"id": entityUUID, "limit": gstLimit})
		if err != nil {
			return opResult{}, fmt.Errorf("hydradb gst: %w", err)
		}
		var fp strings.Builder
		efields := make([]string, 3)
		for c := 0; c < 3; c++ {
			s, err := eres.String(0, c)
			if err != nil {
				return opResult{}, err
			}
			efields[c] = s
		}
		fp.WriteString(strings.Join(efields, ";") + "\n")
		keys := make([]string, 0, gres.Count())
		for i := range gres.Rows {
			a, err := gres.String(i, 0)
			if err != nil {
				return opResult{}, err
			}
			b, err := gres.String(i, 1)
			if err != nil {
				return opResult{}, err
			}
			keys = append(keys, a+","+b)
		}
		for _, k := range keys {
			fp.WriteString(k + "\n")
		}
		return opResult{
			rows:           1 + gres.Count(),
			nodes:          1 + gres.Count(),
			edges:          gres.Count(),
			statements:     2,
			keyFingerprint: fp.String(),
		}, nil
	}
}

func postgresCorporate(ctx context.Context, pool *pgxpool.Pool) op {
	entityQuery := `SELECT id::text, COALESCE(canonical_name,''), COALESCE(normalized_name,'') FROM entities WHERE id=$1::uuid`
	gstQuery := `SELECT id::bigint, COALESCE(gstin,'') FROM entity_gst WHERE entity_id=$1::uuid ORDER BY id LIMIT $2`
	return func(ctx context.Context) (opResult, error) {
		var eid, cn, nn string
		if err := pool.QueryRow(ctx, entityQuery, entityUUID).Scan(&eid, &cn, &nn); err != nil {
			return opResult{}, fmt.Errorf("postgres entity: %w", err)
		}
		rows, err := pool.Query(ctx, gstQuery, entityUUID, gstLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("postgres gst: %w", err)
		}
		defer rows.Close()
		keys := []string{}
		n := 0
		for rows.Next() {
			var id int64
			var gstin string
			if err := rows.Scan(&id, &gstin); err != nil {
				return opResult{}, err
			}
			keys = append(keys, fmt.Sprintf("GST:v:%d,%s", id, gstin))
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		var fp strings.Builder
		fp.WriteString("ENTITY:v:" + eid + ";" + cn + ";" + nn + "\n")
		for _, k := range keys {
			fp.WriteString(k + "\n")
		}
		return opResult{rows: 1 + n, nodes: 1 + n, edges: n, statements: 2, keyFingerprint: fp.String()}, nil
	}
}

// goAppCorporate fetches the entity row, then its GST rows by foreign key -
// two statements, no join, traversal logic in the application.
func goAppCorporate(ctx context.Context, pool *pgxpool.Pool) op {
	return func(ctx context.Context) (opResult, error) {
		var eid, cn, nn string
		if err := pool.QueryRow(ctx, `SELECT id::text, COALESCE(canonical_name,''), COALESCE(normalized_name,'') FROM entities WHERE id=$1::uuid`, entityUUID).Scan(&eid, &cn, &nn); err != nil {
			return opResult{}, fmt.Errorf("go-app entity: %w", err)
		}
		rows, err := pool.Query(ctx, `SELECT id::bigint, COALESCE(gstin,'') FROM entity_gst WHERE entity_id=$1::uuid ORDER BY id LIMIT $2`, entityUUID, gstLimit)
		if err != nil {
			return opResult{}, fmt.Errorf("go-app gst: %w", err)
		}
		defer rows.Close()
		keys := []string{}
		n := 0
		for rows.Next() {
			var id int64
			var gstin string
			if err := rows.Scan(&id, &gstin); err != nil {
				return opResult{}, err
			}
			keys = append(keys, fmt.Sprintf("GST:v:%d,%s", id, gstin))
			n++
		}
		if err := rows.Err(); err != nil {
			return opResult{}, err
		}
		var fp strings.Builder
		fp.WriteString("ENTITY:v:" + eid + ";" + cn + ";" + nn + "\n")
		for _, k := range keys {
			fp.WriteString(k + "\n")
		}
		return opResult{rows: 1 + n, nodes: 1 + n, edges: n, statements: 2, keyFingerprint: fp.String()}, nil
	}
}

// ---------------------------------------------------------------------------
// Starting records
// ---------------------------------------------------------------------------

func loadStartingRecords(ctx context.Context, pool *pgxpool.Pool, start string) map[string]any {
	out := map[string]any{}
	for _, part := range strings.Split(start, ",") {
		pk, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		var name, key string
		var tenderID int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(participant_name,''), COALESCE(participant_normalized,''), tender_id::bigint FROM tender_participants WHERE id=$1`, pk).Scan(&name, &key, &tenderID); err != nil {
			out[fmt.Sprint(pk)] = map[string]any{"error": err.Error()}
			continue
		}
		var org string
		_ = pool.QueryRow(ctx, `SELECT COALESCE(organization,'') FROM tenders WHERE id=$1`, tenderID).Scan(&org)
		out[fmt.Sprint(pk)] = map[string]any{
			"participant_name":       name,
			"participant_normalized": key,
			"tender_pk":              tenderID,
			"bid_gid":                projection.GID("BID", "v", fmt.Sprint(pk)),
			"tender_gid":             projection.GID("TENDER", "v", fmt.Sprint(tenderID)),
			"organization":           org,
			"source_table":           "tender_participants",
		}
	}
	out["entity_e"] = map[string]any{
		"entity_uuid":  entityUUID,
		"source_table": "entities",
	}
	return out
}

// ---------------------------------------------------------------------------
// Dataset counts (source side only; graph-wide counts are infeasible - see
// the note in main.graphCounts)
// ---------------------------------------------------------------------------

func datasetCounts(ctx context.Context, pool *pgxpool.Pool) map[string]any {
	out := map[string]any{}
	counts := map[string]string{
		"tenders":               `SELECT count(*) FROM tenders`,
		"tender_participants":   `SELECT count(*) FROM tender_participants`,
		"tender_extracted_meta": `SELECT count(*) FROM tender_extracted_meta`,
		"entities":              `SELECT count(*) FROM entities`,
		"entity_gst":            `SELECT count(*) FROM entity_gst`,
		"co_bidder_pairs_pg":    `SELECT count(*) FROM (SELECT p1.id FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id=p1.tender_id AND p2.id>p1.id) x`,
	}
	for name, q := range counts {
		var n int64
		if err := pool.QueryRow(ctx, q).Scan(&n); err == nil {
			out[name] = n
		} else {
			out[name] = "error: " + err.Error()
		}
	}
	return out
}
