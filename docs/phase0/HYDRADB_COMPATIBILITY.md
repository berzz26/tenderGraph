# HYDRADB_COMPATIBILITY.md — HydraDB capability matrix (Phase 0)

Investigated 2026-11-14 against the official repository
`https://github.com/hydra-db/hydradb` (main branch): `README.md`, `USING-HYDRADB.md`,
`cypher-compat.md`, `architecture.md`. Everything below reflects the **current**
implementation as documented by the maintainers ("Every behaviour below was verified
against a running server" — USING-HYDRADB.md). No claim here rests on old Neo4j-fork
material; HydraDB is an original Rust engine on SlateDB + S3-compatible object storage.

**Environment status (updated 2026-11-14):** HydraDB build
`0.1.0+916637477f3f` is running locally with query HTTP on `127.0.0.1:8443`,
Bolt on `127.0.0.1:7687`, and admin endpoints on `127.0.0.1:19091`. Health and
read-only query procedures were exercised against the populated TenderGraph
graph. Mutation probes currently return HTTP 500 because the local storage
backend reports `Operation put_opts with mode PutMode::Update not yet implemented
by LocalFileSystem`; no Phase 6 mutation was attempted. The original deployment
recipe remains below as reference, and the write smoke test is not rerun while
the runtime is read-only.

## 1. What HydraDB is (verified)

- Object-store-native distributed graph database in Rust: durable graph records in
  **SlateDB** over S3-compatible storage; query nodes (`graph-node`) and indexer workers
  (`graph-indexer`) hold only disposable memory/SSD caches.
- Graph scope = namespace + graph id + **cell** (partition). One durable writer per cell
  via object-store CAS lease; SlateDB writer epochs fence stale writers.
- Every query runs against **one pinned SlateDB snapshot**; immutable compressed sparse
  column (CSC) traversal-index generations are accelerators combined with the WAL overlay,
  never the source of truth.
- Clients: **Bolt 5.1–5.4 (official Neo4j drivers work unmodified)** and an HTTP JSON /
  NDJSON API. Auth = bearer token. Read consistency: `causal` (default) / `strong`, plus
  bookmarks for read-your-writes. Auto-commit only — **no transactions**.

## 2. Capability matrix

| Capability | Supported | Verification | TenderGraph use |
|---|---|---|---|
| Bolt protocol, Neo4j drivers | Yes (Bolt 5.1–5.4, `neo4j://` routing, `bolt://` direct) | README §Querying; USING-HYDRADB §Connecting (Python/JS examples) | Graph query service + ingestion client in Go (`neo4j-go-driver`) |
| HTTP JSON query API | Yes — `POST /v1/graphs/{graph}/query`, NDJSON streaming | USING-HYDRADB §HTTP query API | Fallback / scripting path; NDJSON for bulk reads |
| Health / readiness | Yes — `/healthz` on query port (8443), `/readyz` + `/metrics` on admin port (9090) | USING-HYDRADB table | Deployment liveness in compose/k8s |
| Authentication | Yes — bearer token (Bolt password field); TLS required in deployed envs (`neo4j+s://`, `https://`) | USING-HYDRADB §Connecting | Secrets via `.env`, like existing MINIO_* keys |
| Node `CREATE` | Yes, but **only as one-hop edge pattern**; bare `CREATE (n {...})` is rejected | cypher-compat §CREATE; USING-HYDRADB §dialect | Ingest nodes via MERGE/UNWIND upsert instead |
| Node `MERGE` (bare) | Rejected (edge pattern required) | same | Node upsert via `UNWIND $rows MERGE (n {id: row.id}) SET n:Label, n.prop=...` |
| Node upsert keyed on `id` | Yes — `id` is the identity/upsert key; no auto-generated ids; **documented id type is non-negative integers** (cypher-compat §Values); string ids are not documented | USING-HYDRADB §Identity and idempotency; cypher-compat §Values | Vertex id = versioned 63-bit hash of the complete `gid` string (numeric); `gid` kept as a string property (GRAPH_ONTOLOGY §3) |
| Relationship `MERGE` by `id` property | Documented only in batch form; **Phase 2 live probe verified replay uniqueness across separate requests** for `UNWIND … MATCH … MERGE (a)-[r:T {id: row.rid}]->(b)`; no general relationship constraint is assumed | Phase 2 integration probe against build `0.1.0+916637477f3f`; cypher-compat §Batches | Use deterministic relationship ids with batch MERGE; observation nodes remain required for multi-observation facts |
| Relationship `CREATE` / `MERGE` | Batch `MERGE` by deterministic relationship `id` replayed safely across separate requests; plain `CREATE` duplicates relationships across requests | Phase 2/3 live probe against the verified development build | Use deterministic batch `MERGE`; do not use `CREATE` for replayable ingestion |
| `SET` / `REMOVE` | Yes, after a `MATCH`; `id` immutable; labels settable | cypher-compat §SET | Patch provenance/timestamp properties |
| `DELETE` / `DETACH DELETE` | Yes, after `MATCH` | cypher-compat | Identity rejections, test isolation |
| `OPTIONAL MATCH` | Yes for reads; rejected in mutations | cypher-compat §Quick reference | Left-outer semantics for missing financials |
| `WHERE` | `= <> < > <= >= STARTS WITH`, AND/OR/NOT. **No `IN`, `ENDS WITH`, `CONTAINS`, `IS NULL`** | cypher-compat §WHERE | Client-side filtering for the unsupported operators |
| `RETURN` | Only `<binding>.<property>` or aggregate (`count, sum, avg, collect`); aliases; `DISTINCT` rows; **no arithmetic, no path bindings, no `RETURN *`, no `count(DISTINCT …)`, no `min/max`** | cypher-compat §RETURN | All aggregations planned around this |
| Grouped aggregation | Yes (implicit GROUP BY, multiple keys); zero-count groups omitted | USING-HYDRADB §What is supported | Leaderboards; default zeros client-side |
| `ORDER BY / SKIP / LIMIT` | Yes; multiple sort keys supported | same | Pagination + deterministic top-N |
| `WITH` | Pass-through only (no alias/filter/order) | cypher-compat §WITH | Keep queries single-phase |
| `UNION / UNION ALL` | Read only, same columns, non-nested | cypher-compat | Multi-shape summaries |
| Variable-length paths | Yes, **bounded only** (`*1..3`); `*`/`*1..` rejected; range inclusive; cycles revisit nodes | cypher-compat §VLP; USING-HYDRADB §Variable-length | Co-bidder networks, layered paths — always bound |
| Multi-hop MATCH patterns, comma-joined patterns | Yes | USING-HYDRADB §What is supported | Tender→Bid→Vendor chains |
| Undirected patterns, `[:A\|B]`, `(a)-->(b)` | Rejected (one type, one direction) | USING-HYDRADB §dialect table | Query service enumerates directions client-side |
| `MATCH` needs anchor | Yes — id, label, or property predicate required | USING-HYDRADB §dialect | Ontology mandates labels on every node |
| Label-anchored counts | Yes — but only if **every source node carries the label** | USING-HYDRADB verified caveat | Ingestion writes labels in the same batch |
| `UNWIND` batch writes | Yes, parameter-driven list of maps; **cap 1024 items**; a single request may deduplicate plain edge `CREATE` on (src,dst,type), but replay across requests is not safe; node batch is `UNWIND … MERGE (n {id}) SET n:Label, props` | cypher-compat §Batches; Phase 2/3 live probe | **Primary ingestion path** (chunk 1000). Relationship `MERGE` with deterministic `id` is used for replay; co-bid evidence remains node-based (`CoBidObservation`) |
| `UNWIND` inline lists / batch reads | Parameter only; batched read has fixed 2-column projection | same | Bulk loader design |
| Transactions | **None** — auto-commit only; `BEGIN/COMMIT/ROLLBACK` rejected | architecture §Client Boundary; USING-HYDRADB §Transactions | Write idempotency via MERGE semantics + PK-watermarked re-runs |
| Query parameters | Scalars everywhere; lists only as UNWIND input | cypher-compat §Values | Projection batches pass row maps |
| Pagination | HTTP: `page_size` (default 1024, max 4096), `cursor`+`query_id`, non-retryable consuming cursors; Bolt driver-native | USING-HYDRADB §Pagination | HTTP NDJSON for big exports; Bolt for interactive |
| Read consistency `causal`/`strong` + bookmark | Yes | USING-HYDRADB §Read consistency | Sync verification uses bookmark after write |
| Query cancellation | Yes — `POST /v1/graphs/{g}/queries/{id}/cancel` | USING-HYDRADB §Limits | Long graph queries |
| Execution budgets | Yes — scanned edges, rows, result vertices, bytes, wall-clock; partial results are NOT returned silently, query fails | architecture §Failure Semantics | Bound all traversals; treat failures explicitly |
| Property / relationship indexes | Yes — planner-selectable property indexes, reverse adjacency, ordered limit pushdown | architecture §Query Execution | Ontology property conventions; indexer builds CSC automatically |
| Native `algo.SPpaths` | Yes — `{sourceNode, targetNode, relTypes:[...], relDirection, maxLen, pathCount, weightProp?, costProp?, maxCost?}`; `maxLen` counts **edges**; yields `path, pathWeight, pathCost`; simple paths (no revisits) | README §Native path procedures; cypher-compat §Path procedures; USING-HYDRADB §Path queries | Contract→capital path queries |
| Native `algo.SSpaths` | Yes — one source, same config minus target; optional `targetLabel/Property/Values` | same | "Who is connected to vendor X" |
| Native `algo.MSpaths` | Yes — server-side many-to-many via `sourceLabel/sourceProperty/sourceValues` (**lists of strings only, inlined**, not parameters), `targetValues`, `pairwise` (default unspecified — set explicitly), `resultLimit`; pairs with no path are omitted | USING-HYDRADB §Path queries | Vendor network investigations at scale |
| `algo.degree` | Yes — degree ranking per label, `relDirection` incl. `undirected`, `neighborLabel`, `minDegree`, `limit` | README §Querying | Hub-vendor detection |
| Multi-type traversal in one MATCH | No (one type per pattern) — but `algo.*` `relTypes` accepts several | USING-HYDRADB §Path queries | Path algorithms handle cross-type paths |
| Vector search / embeddings | **NOT AVAILABLE IN VERIFIED TARGET.** The open-source engine (`hydra-db/hydradb` main, 741 repo paths scanned 2026-11-14) contains **zero vector/embedding code paths** (no vector, hnsw, faiss, ann modules or procedures documented). The commercial product (hydradb.com) markets graph-memory/hybrid-retrieval capability — **PRODUCT/ECOSYSTEM CLAIM ONLY**, not present in the verified self-hosted target | OSS repo tree scan (verified absence) + hydradb.com (marketing claim only) | No vector features assumed from HydraDB. Semantic BOQ matching stays in **PostgreSQL pgvector** (`boq_items.item_description_embedding`, `embeddings_title_desc`) — existing embeddings are preserved, not migrated. The no-separate-vector-DB decision rests on PostgreSQL pgvector + the relationship-driven question set, **not** on any HydraDB capability |
| Full-text search | No (`CONTAINS` rejected) | cypher-compat §Not supported | Name search stays in PostgreSQL (pg_trgm) |
| Graph algorithms beyond paths/degree | No (no PageRank, community detection, etc. in the procedures documented) | docs reviewed | Compute in app layer from projection data |
| Schema/constraints (uniqueness, existence) | None documented — only `id` upsert semantics | docs reviewed | Enforce at ingestion + PostgreSQL control tables |
| Date/time property type | Only int/float/bool/string values | cypher-compat §Values | Store timestamps as epoch-millis ints or ISO strings |
| Multi-statement requests | No (one statement per request) | cypher-compat | Batch via UNWIND |

## 3. Working query examples (validated against the docs' verified examples)

All examples assume Bolt with `neo4j-go-driver` (`neo4j://host:7687`, password = token,
database = graph id) or HTTP with `cell_id` mandatory on every request.

### 3.1 Node upsert with label + properties (batch)
```cypher
UNWIND $rows AS row MERGE (n {id: row.id})
SET n:Vendor, n.gid = row.gid, n.name = row.name, n.gstin = row.gstin, n.updated_at_ms = row.updated_at_ms
```
- `$rows` is a list of maps passed as a **parameter** (Go driver value), ≤1000 items per call.
- `row.id` is the **numeric** vertex id (versioned 63-bit hash of the complete `gid` string —
  GRAPH_ONTOLOGY §3); the gid string rides along as the `gid` property. **String ids are not
  documented** for HydraDB and are not used.
- Label must come first in `SET`. Running over existing ids is a safe re-label/property patch.
- Pre-write collision check (existing node at that `id` must carry the same `gid`) happens in
  the projector before dispatch.

### 3.2 Edge batch (row-keyed, replay-safe)
```cypher
UNWIND $rows AS row
  MATCH (t:Tender {id: row.t}), (v:Vendor {id: row.v})
  MERGE (v)-[r:CONTRACTOR_ON_RECORD {id: row.rid}]->(t)
  SET r.src_pk = row.pk
```
- Edge batch creates endpoint records if absent (**unlabelled** — run the node batch first).
- Ordering rule: labels/properties batch → edge batch (avoids unlabelled windows).
- Plain batch `CREATE` may deduplicate within one request, but separate requests
  can create duplicates. Replayable ingestion therefore uses relationship
  `MERGE` with a deterministic `id`; multi-observation facts remain
  `CoBidObservation` **nodes** (3.3), not direct bid-to-bid edges.

### 3.3 Bounded multi-hop MATCH (co-bid network via observation nodes)
```cypher
MATCH (b:Bid {id: $bidId})-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid)
WHERE other.id <> $bidId
RETURN other.id AS other_id, other.participant_normalized AS other_name, count(o) AS shared_tenders
ORDER BY shared_tenders DESC, other_id ASC LIMIT 25
```
Each shared tender is one `CoBidObservation`, so `count(o)` preserves per-tender evidence
(a `(src,dst,type)`-deduped direct edge could not).

### 3.4 Bounded variable-length path
```cypher
MATCH (a:Bid {id: $bidId})-[:CO_BID_IN*2..4]->(b:Bid)
WHERE b.id <> $bidId
RETURN DISTINCT b.id AS reached, b.participant_normalized AS name ORDER BY name LIMIT 200
```
(`CO_BID_IN` edges run `Bid→CoBidObservation`; other bids are reached at even hop counts,
hence the `2..4` bound.)

### 3.5 OPTIONAL MATCH (may have no financials)
```cypher
MATCH (ce:CorporateEntity {id: $eid})
OPTIONAL MATCH (ce)-[:HAS_FINANCIAL_STATEMENT]->(fs:FinancialStatement {fiscal_year: $fy})
RETURN ce.canonical_name AS name, fs.revenue AS revenue
```
(count with `count(fs.revenue)`, never `count(*)` after OPTIONAL MATCH.)

### 3.6 Grouped aggregation / leaderboard
```cypher
MATCH (v:Vendor)-[r:WON]->(a:Award)
RETURN a.fiscal_year AS fy, count(*) AS awards, sum(a.awarded_value_inr) AS value
ORDER BY value DESC LIMIT 20
```
(`sum` runs on the float analytics property; exact monetary arithmetic stays in
PostgreSQL/Go decimal code — money policy, DECISIONS D13.)

### 3.7 Native path procedure (contract→capital)
```cypher
CALL algo.SPpaths({
  sourceNode: $tenderVertexId, targetNode: $entityVertexId,
  relTypes: ['HAS_AWARD','WON','IDENTIFIED_AS','HAS_FINANCIAL_STATEMENT'],
  relDirection: 'outgoing', maxLen: 4, pathCount: 3
})
YIELD path, pathWeight, pathCost
RETURN path, pathWeight, pathCost
```
- `relTypes` may list several types (unlike MATCH). `maxLen` counts edges. Paths are simple.

### 3.8 Many-to-many investigation
```cypher
CALL algo.MSpaths({
  sourceLabel: 'Vendor', sourceProperty: 'name',
  sourceValues: ['Alpha Infra Ltd', 'Beta Constructions Pvt Ltd'],
  targetValues: ['Alpha Infra Ltd', 'Beta Constructions Pvt Ltd'],
  relTypes: ['CO_BID_IN'], relDirection: 'outgoing',
  maxLen: 2, pathCount: 1, pairwise: false, resultLimit: 200
})
YIELD path
RETURN path
```
- `sourceLabel: 'Bid'`, `sourceProperty: 'participant_normalized'` in this corrected shape:
  co-bid paths run `Bid→CoBidObservation→Bid`, so `maxLen: 2` with outgoing direction.
- Values must be a list of **strings**, inlined with manual escaping; set `pairwise` explicitly;
  pairs with no path are silently omitted — join results back by endpoint ids.

### 3.9 Read-your-writes after a sync batch
Take `bookmark` from the mutation response, send it in the next read's `bookmark` field;
or set `"consistency": "strong"` for a globally fresh read.

### 3.10 Test isolation
```cypher
UNWIND $ids AS id MATCH (n {id: id}) DETACH DELETE n
```
(`DETACH DELETE` on absent ids is a no-op; batch cap 1024.)

## 4. Application-level functionality we must implement (not native)

1. **Relationship identity / idempotency**: relationship `MERGE`-by-`id` is
   verified across separate requests in the target development build, but no
   database-wide relationship constraint is assumed. Idempotency is still our responsibility: deterministic row-keyed edges,
   `CoBidObservation` nodes for multi-observation facts, replay-safe batches — not assumed
   from HydraDB constraints (none are documented).
2. **Uniqueness beyond `id`** (e.g., one CONFIRMED identity per vendor): enforce in the
   projection engine + PostgreSQL control tables.
3. **Counting with ambiguity**: `count(DISTINCT …)` rejected, grouped aggregation drops
   zero groups — compute exact distinct sets client-side where needed.
4. **Filtering** unsupported in Cypher (`IN/CONTAINS/IS NULL/ENDS WITH`): fetch candidate sets
   and filter in Go, or keep predicates in PostgreSQL and push only vertex ids to the graph.
5. **Name/semantic search**: stays in PostgreSQL (pg_trgm + pgvector); the graph answers
   structural questions.
6. **Aggregation breadth** (min/max, percentiles, windowed stats): compute in Go or
   PostgreSQL; graph returns the raw paths/rows.
7. **Multi-statement atomicity**: sequence batches and verify via bookmarks; design all
   writes idempotent (auto-commit only).
8. **Schema governance** (label/property registry, drift detection): a TenderGraph control table.
9. **Date handling**: property values are scalar types only — normalize all timestamps to
   epoch-millis ints (`*_ms`) and ISO strings for display.

## 5. Unsupported / needs further investigation

### Phase 2 runtime update (2026-11-14)

The Phase 2 vertical-slice integration test ran against the development HydraDB
instance and verified that the documented batch relationship form
`UNWIND $rows AS row MATCH ... MERGE (a)-[r:TYPE {id: row.rid}]->(b)`
returns one relationship after two separate mutation requests with the same
relationship id. The projector therefore uses deterministic relationship ids
and relationship `MERGE` for replay. This does not introduce or assume a
database-wide relationship uniqueness constraint, and it does not change the
observation-node requirement for legitimate repeated facts.

| Item | Status |
|---|---|
| **String vertex ids** (`MERGE (n {id: <string>)`) | **NOT INVESTIGATED in engine** — docs state node ids are non-negative integers; string id behavior (accept/reject/coerce) must be probed in the smoke test before any reliance. TenderGraph does not use string ids. |
| `GRAPH_CYPHER_ENGINE=datafusion` variant | Needs investigation before relying on it; documented as rejecting `DETACH DELETE` and some multi-row mutations. We plan to stay on the default `legacy` engine. |
| `MERGE` edge with extra pattern properties under UNWIND | Edge-MERGE with `{id: row.rid}` and a separate `SET` is verified in the Phase 2/3 live projection. |
| Relationship `MERGE`-by-`id` uniqueness on replay | Verified across separate requests in the Phase 2/3 live projection for the target build; not promoted to a general database constraint. |
| Bookmark / `strong` consistency behavior | Documented; **runtime-unverified** — smoke test steps B (bookmark round trip) gate any read-your-writes reliance. |
| Delete behavior (`DETACH DELETE` visibility to subsequent reads) | Documented as supported; runtime-unverified — gates the source-deletion handling decision (ARCHITECTURE §2.6). |
| Exact behavior of `algo.MSpaths` default `pairwise` | Default unspecified per docs; smoke test to confirm, code sets it explicitly. |
| Bolt driver version floor in Go driver | Neo4j Go driver v5 targets Bolt ≥5.1 — matches; confirm handshake in smoke test. |
| Numeric id precision in the Go driver (Bolt int64 vs float coercion) | Neo4j drivers carry integers natively; confirm no conversion loss for 63-bit masked values in the smoke test (step I). |
| Concurrent-writer behavior during bulk sync | Single-writer per cell by lease; bulk sync is single-threaded per cell by design; further investigation only if we shard cells. |
| Snapshot/pagination interactions for very long NDJSON exports | Documented (pinned snapshot, consuming cursor, non-retryable pages); robust page loop to be built and tested in Phase 1. |

## 6. Deployment requirements (verified from README/USING-HYDRADB)

- Build: Rust 1.99+, C/C++ toolchain, `libcypher-parser`, SuiteSparse GraphBLAS
  (`just native-check`), `just build` produces `graph-node` and `graph-indexer`.
- Single dev node env: `CLOUD_PROVIDER=local`, `LOCAL_PATH`, `GRAPH_NAMESPACE`,
  `GRAPH_ID`, `GRAPH_CELL_ID`, `GRAPH_CELLS`, `GRAPH_NODE_ID`,
  `GRAPH_BOLT_NODE_ADDRESSES`, `GRAPH_ADVERTISED_BOLT_ADDR`, `GRAPH_DATA_CACHE_DIR`,
  `GRAPH_AUTH_TOKEN_FILE`, `GRAPH_ALLOW_PLAINTEXT=true`, `RUST_MIN_STACK=33554432`.
- Production: S3-compatible object storage (MinIO already present in this stack),
  `graph-node` replicas + `graph-indexer` workers, TLS, bearer token; Helm chart available.
- Alternative: `bash scripts/runtime_smoke.sh` builds and exercises Bolt+HTTP on a
  throwaway store (requires Python `neo4j` package).
- **`pg_hydradb` (PostgreSQL extension, verified from `crates/pg-hydradb/README.md`)**:
  answers graph queries inside PostgreSQL over existing tables. **Not deployable here**: it
  requires `shared_preload_libraries` (a server restart) on the shared production PostgreSQL
  that hosts another live system — out of bounds under the no-destructive-PostgreSQL-change
  constraint. Recorded as an evaluated alternative only; TenderGraph targets the standalone
  `graph-node` deployment.

## 7. Smoke-test status

NOT EXECUTED (see §1 environment status). The complete recipe, environment variables, and a
ready-to-run script are in `docs/phase0/spike/hydradb_smoke.sh` with the 8-step test sequence
(connect → node → node → typed relationship → MATCH retrieve → bounded path → property
verification → delete). Execution is gated on installing the Rust toolchain and native libs
or on a prebuilt image from the maintainers; it is the first Phase-1 task.
