# TenderGraph Phase 6 Report — Native Graph Algorithms and Performance Evaluation

**Date:** 2026-11-14  
**Status:** bounded native read algorithms implemented and validated; HydraDB remains read-only for mutations.

## 1. Central conclusion

HydraDB provides a measurable advantage for bounded graph-native path queries.
`algo.SPpaths`, `algo.SSpaths` and `algo.MSpaths` return source-preserving
Bid/CoBidObservation paths in approximately 2.7–3.6 ms per warm HTTP query in
the tested workload. They remove client-side traversal/fan-out and return the
complete path structure, including graph node and relationship provenance, for
the requested bound.

HydraDB does not currently provide an advantage for global degree ranking in
this graph. The first `algo.degree` query took approximately 8.6 seconds while
warming the populated topology; subsequent queries were approximately 2.7–3.5
ms. PostgreSQL grouped aggregation completed in approximately 85 ms server-side
and approximately 100 ms per `psql` process including connection/startup. The
existing bounded Go BFS remains useful when its source-enrichment behavior is
needed, but native path procedures are the cleaner graph operation.

Recommended division of responsibility:

- HydraDB: bounded native path exploration and graph-structural evidence.
- PostgreSQL: global degree/leaderboard aggregation, distinct participant
  calculations, pair overlap, time windows and source enrichment.
- Go: orchestration, result interpretation, identity semantics, limits and
  structured CLI responses.

## 2. Runtime and safety status

The tested runtime is HydraDB `0.1.0+916637477f3f`:

- query HTTP: `127.0.0.1:8443`;
- Bolt: `127.0.0.1:7687`;
- admin: `127.0.0.1:19091`;
- graph health: HTTP 200;
- admin readiness: HTTP 200.

Mutation probes remain blocked by the known local-storage error:
`Operation put_opts with mode PutMode::Update not yet implemented by
LocalFileSystem`. No graph mutation, reset, rebuild or cleanup write was
attempted in Phase 6.

## 3. Algorithms investigated and verified syntax

All experiments used one bounded HTTP query with `cell_id: "cell-0"`, the
existing default graph and the populated TenderGraph data.

### `algo.degree`

```cypher
CALL algo.degree({
  label: 'Bid',
  relTypes: ['CO_BID_IN'],
  relDirection: 'undirected',
  minDegree: 1,
  limit: 5
})
YIELD node, degree
RETURN node.gid AS gid, node.participant_normalized AS participant, degree
```

Verified behavior: rows are ordered by degree descending and then vertex ID.
`undirected` counts every relationship touching a vertex once. Degree counts
graph relationships, including parallel source-backed relationships; it does
not count distinct legal bidders, distinct normalized participant names or
distinct tenders.

### `algo.SPpaths`

```cypher
CALL algo.SPpaths({
  sourceNode: $source,
  targetNode: $target,
  relTypes: ['CO_BID_IN'],
  relDirection: 'both',
  maxLen: 2,
  pathCount: 1
})
YIELD path
RETURN path
```

`maxLen` counts edges. Paths are bounded and simple. `CO_BID_IN` is stored as
Bid → CoBidObservation for both participants, so a Bid-to-Bid connection must
use `relDirection: 'both'`; using `outgoing` alone does not traverse from the
observation back to the other Bid.

### `algo.SSpaths`

```cypher
CALL algo.SSpaths({
  sourceNode: $source,
  relTypes: ['CO_BID_IN'],
  relDirection: 'both',
  maxLen: 2,
  pathCount: 1,
  resultLimit: 3
})
YIELD path
RETURN path
```

This returns bounded paths from one Bid. `resultLimit` bounds returned paths;
it does not establish graph-wide completeness.

### `algo.MSpaths`

```cypher
CALL algo.MSpaths({
  sourceLabel: 'Bid',
  sourceProperty: 'gid',
  sourceValues: ['BID:v:3223', 'BID:v:3222'],
  targetValues: ['BID:v:3223', 'BID:v:3222'],
  relTypes: ['CO_BID_IN'],
  relDirection: 'both',
  maxLen: 2,
  pathCount: 1,
  pairwise: false,
  resultLimit: 10
})
YIELD path
RETURN path
```

Verified behavior: selector values must be strings and are indexed by the
selected property; numeric vertex IDs are not accepted as selector lists. Pairs
with no path are omitted. `pairwise` is set explicitly. The live test returned
both ordered paths for the known Bid pair.

An initial attempt that appended Cypher `LIMIT` after native procedure output
was rejected by the runtime. Native procedures must use their own `limit` or
`resultLimit` options and the supported `YIELD … RETURN` form.

## 4. Degree semantics and comparison

The implemented native degree CLI reports:

```json
{
  "method": "HydraDB algo.degree",
  "metric_meaning": "Undirected CO_BID_IN relationship degree of Bid nodes...",
  "rows": [
    {"bid_id": 1760, "bid_gid": "BID:v:1760", "relationship_degree": 84}
  ]
}
```

The top result is a Bid record, not a company. Multiple Bid rows can represent
the same observed participant key, and each CoBidObservation is a per-tender
observation. Therefore the result must not be presented as “the company has 84
competitors”. Phase 4 PostgreSQL rankings remain the correct source for
distinct observed participant and distinct tender metrics.

The PostgreSQL reference query grouped source participant rows against other
participants in the same tender and produced 156,402 pair rows before grouping.
`EXPLAIN (ANALYZE, BUFFERS)` measured 84.906 ms execution time on the live
database. Five process-level `psql` runs were 0.10 seconds each. Native degree
measurements were:

| Sample set | Timings (seconds) | Interpretation |
|---|---|---|
| First native call | 8.604787 | cold topology/index/algorithm setup cost |
| Five warm native calls | 0.003463, 0.003159, 0.002992, 0.002656, 0.002805 | p50 ≈ 0.00299 s; p95 ≈ 0.00346 s |

The cold-start cost is material and must be included in operational capacity
planning. The native result is still a graph relationship count, not a
distinct-participant analytical ranking.

## 5. Path experiments against real records

The following records were used without modifying the graph:

- `BID:v:3223` — `AMAR ENTERPRISES`;
- `BID:v:3222` — `aman upadhyay`;
- shared observation `COBID:v:17405:3222:3223`.

`SPpaths` with both directions and `maxLen: 2` returned the expected path:

```text
BID:v:3223
  -> COBID:v:17405:3222:3223
  <- BID:v:3222
```

The path response included both `CO_BID_IN` relationships and their source
properties, including `observation_tender_pk: 17405` and
`derivation_method: same_tender_participant_pair`.

`SSpaths` from Bid 3223 with `maxLen: 2` and `resultLimit: 3` returned bounded
path evidence. `MSpaths` returned the ordered pair paths for Bids 3223 and 3222.
An SPpaths request using `relDirection: outgoing` returned no path, which is
the expected result for this directed edge layout rather than evidence that the
bids are disconnected.

No path to a CorporateEntity was fabricated. A company path requires an actual
evidence-backed `IDENTIFIED_AS` relationship, and the current Phase 5 identity
logic deliberately leaves ambiguous links as candidates.

## 6. Performance comparison

Direct HTTP timings used five samples, result limits shown below and the live
development graph. These timings exclude Go CLI startup and isolate the query
request as measured by `curl`; the first degree cold sample is reported
separately above.

| Operation | Bound | Samples (seconds) | Empirical p50 | Empirical p95 |
|---|---:|---|---:|---:|
| Native degree, warm | top 5 | 0.003463, 0.003159, 0.002992, 0.002656, 0.002805 | 0.002992 | 0.003463 |
| Native SPpaths | depth 2, 1 path | 0.003559, 0.002757, 0.003296, 0.002712, 0.003165 | 0.003165 | 0.003559 |
| Native SSpaths | depth 2, 3 paths | 0.002896, 0.002847, 0.002881, 0.003343, 0.002760 | 0.002881 | 0.003343 |
| Existing Go network BFS CLI | depth 2, 3 nodes | 0.06, 0.05, 0.05, 0.05, 0.05 | 0.05 | 0.06 |
| Existing Go direct co-bidders CLI | limit 3 | 0.05, 0.05, 0.05, 0.05, 0.05 | 0.05 | 0.05 |
| PostgreSQL degree reference | top 5 | 0.10, 0.10, 0.10, 0.10, 0.10 | 0.10 | 0.10 |

The Go timings include process startup, PostgreSQL connection setup and graph
client setup, so they are not a pure algorithm comparison. The PostgreSQL
`EXPLAIN ANALYZE` server-side execution was 84.906 ms. Memory was not
meaningfully comparable from the HTTP client; the direct curl process stayed
around 11 MiB RSS in the native experiments.

## 7. CLI commands implemented

From `api/`:

```bash
graphintel degree --limit 5
graphintel paths 3223 3222 --depth 2 --limit 1
graphintel connections 3223 --depth 2 --limit 3
```

All return structured JSON. Native path responses include the method, source
and target IDs, depth, result limit, completeness caveat and raw bounded path
evidence. The HTTP decoder was extended to preserve 64-bit path vertex,
relationship and typed integer property values; generic `float64` decoding is
not used for these IDs.

The existing `network` command remains the bounded Go BFS implementation. It
was not replaced because it enriches source participant records from
PostgreSQL and has explicit visited-node behavior useful to Phase 4 responses.

## 8. Tests executed

Passed:

```text
go test ./...
go vet ./...
go test -tags integration ./internal/intel -count=1 -v
```

The live integration test covered:

- native degree against populated Bid/CoBidObservation data;
- SPpaths between Bids 3223 and 3222;
- SSpaths from Bid 3223;
- MSpaths with string GID selectors;
- actual path result decoding and bounded result limits.

The full required command was run:

```text
go test -tags integration ./... -count=1
```

The Phase 6/Phase 4/Phase 5 read tests passed. The overall command failed only
in existing `internal/graph` and `internal/ingest` mutation tests because the
known HydraDB LocalFileSystem runtime returns HTTP 500 for writes. No mutation
test failure was reinterpreted as an algorithm failure.

## 9. Files created or modified

Created:

- `api/internal/intel/algorithms.go`
- `api/internal/intel/algorithms_test.go`
- `docs/phase6/PHASE_6_REPORT.md`

Modified:

- `api/cmd/graphintel/main.go` — added `degree`, `paths` and `connections`.
- `api/internal/graph/result.go` — added precision-safe native path decoding.
- `api/internal/graph/hydrahttp_test.go` — path decoder precision test.
- `api/internal/intel/integration_test.go` — live native algorithm tests.
- `docs/phase0/HYDRADB_COMPATIBILITY.md` — updated runtime status and known
  mutation failure.

No graph data, PostgreSQL source data or PostgreSQL control-plane data was
modified. No graph writes were attempted.

## 10. Known limitations

- Native degree has a significant cold-start/topology setup cost in the current
  runtime. Warm latency is excellent, but this needs operational measurement
  after restart and at larger graph sizes.
- Degree reports Bid relationship degree, not distinct bidder or company
  degree. PostgreSQL/Go remains responsible for identity-aware aggregation.
- Native path result completeness is bounded by `maxLen`, `pathCount` and
  `resultLimit`; omitted pairs or paths are not global non-existence proofs.
- HydraDB's path envelope exposes raw graph properties; business-level source
  enrichment remains in PostgreSQL/Go.
- No community detection, PageRank, anomaly scoring or unbounded traversal was
  introduced; the runtime does not provide a verified need or safe capability
  for them.
- HydraDB mutation support remains blocked by the storage backend issue.

Phase 7 was not started.
