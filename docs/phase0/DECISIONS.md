# DECISIONS.md — TenderGraph architecture decisions (Phase 0)

Each entry: Decision / Reasoning / Alternatives considered / Trade-offs / Consequences.
Status column states whether the decision is VERIFIED by evidence in this repo/DB, or
PROPOSED for approval.

## D1 — PostgreSQL remains the authoritative source of truth
- **Decision**: All TenderGraph data originates from, and remains owned by, the existing
  PostgreSQL `vyuha` database. The graph is a rebuildable projection. Analyst decisions are
  persisted in additive `tg_*` PostgreSQL tables.
- **Reasoning (VERIFIED)**: The DB is live production for the Vyuha/scraper system
  (`scrapes`, `sources`, Alembic `alembic_version`); 30+ relevant tables already hold the
  data, integrity rules, and derived intelligence (`vw_vyuha_l1_intelligence`); the existing
  Go API is built entirely on it. Rebuilding authority elsewhere would fork truth.
- **Alternatives**: (a) migrate data into HydraDB as primary — rejected: loses FK
  constraints, trgm/vector features, and the working dashboard; (b) dual-master — rejected:
  split-brain risk with no payoff.
- **Trade-offs**: every graph query returns potentially minutes-stale data; cross-store joins
  need the query service.
- **Consequences**: sync must be idempotent and reconcilable; PostgreSQL schema untouched
  (additive-only migrations for control tables).

## D2 — HydraDB is the sole graph database
- **Decision**: All graph storage and traversal is served by HydraDB (Bolt + HTTP, S3-backed
  SlateDB). No Neo4j/Memgraph/Neptune.
- **Reasoning (VERIFIED capability, PROPOSED deployment)**: HydraDB natively provides the
  required shape — typed directed relationships, bounded variable-length paths, optional
  match, aggregation, native path procedures (`algo.SPpaths/SSpaths/MSpaths`, `algo.degree`),
  Neo4j-driver compatibility, snapshot-consistent reads, budgeted queries — on S3-compatible
  object storage that already exists in this stack (MinIO). This is *not* a claim that other
  graph databases could not implement the same functionality; they could. HydraDB fits this
  architecture because: object-store durability matches the existing MinIO-based
  infrastructure; disposable caches mean the graph can be rebuilt from PostgreSQL at any
  time, which is exactly our projection model; and its disciplined, bounded Cypher subset
  matches our controlled query surface.
- **Alternatives**: Neo4j (mature, but heavier operational footprint and license for
  clustering; store-local rather than object-store-native); Memgraph (in-memory focus);
  Neptune (managed cloud, contradicts self-hosted MinIO stack); embedding graphs in
  PostgreSQL (recursive CTEs — verified workable for small depth, but no native path
  procedures or CSC-indexed traversals; rejected as the traversal engine, retained as source).
- **Trade-offs**: narrower Cypher dialect (no `IN/CONTAINS/IS NULL`, no arithmetic in RETURN,
  no multi-statement transactions — all verified); no prebuilt binaries at audit time
  (build-from-source deployment).
- **Consequences**: application code must honor the dialect rules; ingestion is
  MERGE/batch-based and idempotent.

## D3 — No separate vector database (capability statement corrected)
- **Decision**: Semantic search stays in PostgreSQL using the already-installed pgvector. No
  vector capability is assumed from HydraDB.
- **Reasoning (VERIFIED, two distinct levels)**: pgvector is installed in the DB with real
  usage (`boq_items.item_description_embedding vector(1536)` — 1,200 populated;
  `embeddings_title_desc vector(384)`). On HydraDB: the **open-source engine** intended for
  self-hosting (`hydra-db/hydradb` main) contains **zero vector/embedding code paths**
  (741 repository paths scanned 2026-11-14 — no vector/hnsw/faiss/ann modules or documented
  procedures) → **NOT AVAILABLE IN VERIFIED TARGET**. The **commercial product**
  (hydradb.com) markets graph-memory/hybrid-retrieval capability → **PRODUCT CLAIM ONLY**,
  not a verified property of the self-hosted engine. The two must not be conflated.
- **Corrected rationale (supersedes the earlier "HydraDB has no vector search" as the
  decision basis)**: the no-separate-vector-DB decision rests on (a) PostgreSQL pgvector
  already covering the semantic-search requirement, (b) TenderGraph's initial core questions
  being relationship- and provenance-driven, and (c) no demonstrated requirement for more.
  It does **not** rest on any HydraDB vector capability or its absence. Existing PostgreSQL
  embeddings are preserved — not removed, migrated, or duplicated.
- **Alternatives**: dedicated vector DB (rejected — no demonstrated gap); relying on a
  future HydraDB commercial vector feature (rejected for now — unverified product claim,
  evaluated separately if ever demonstrated).
- **Trade-offs**: vector search scales with PostgreSQL, not the graph tier.
- **Consequences**: BOQ similarity queries run against PostgreSQL; the graph stores no
  embeddings; HydraDB vector capability is re-evaluated only if/when it appears in the
  self-hosted engine and a concrete requirement exists.

## D4 — Graph traversal is the primary retrieval mechanism for network questions
- **Decision**: Structural questions (co-bidder networks, contract-to-capital paths,
  hub detection) are answered by HydraDB traversals and `algo.*` procedures; PostgreSQL
  remains primary for scalar filtering, text search (pg_trgm), and vector search.
- **Reasoning (VERIFIED)**: The intelligence features are inherently path/network-shaped
  (bidder networks, evidence chains); HydraDB's CSC traversal indexes and GraphBLAS path
  procedures are designed for this; PostgreSQL recursive CTEs degrade on multi-hop, many-path
  queries.
- **Alternatives**: precompute all relationships as SQL view joins (rejected — explodes
  combinatorially across hops).
- **Trade-offs**: two query engines in the stack; the query service must route by question
  type.
- **Consequences**: the graph ontology must remain traversal-friendly (bounded depth
  conventions, evidence edges traversable).

## D5 — Stable IDs are mandatory; numeric HydraDB id + canonical gid string
- **Decision**: Every node carries a canonical, immutable GID string
  (`<TYPE>:<origin>:<key>`) and a numeric HydraDB `id` — a **versioned 63-bit hash of the
  complete GID string** (UTF-8 bytes, unsigned output masked to 63 bits; HydraDB documents
  node ids as non-negative integers; string ids are not documented and are not used). The
  gid remains the canonical application identity even if the numeric id changes in a future
  hash-version migration (`id_algo_v` property on nodes).
- **Reasoning (VERIFIED)**: HydraDB `id` is the only upsert key (no auto-generated ids;
  MERGE matches on id — verified); algo procedures take integer vertex ids; string ids are
  not documented. Without stable IDs, re-runs duplicate relationships (plain CREATE
  duplicates verified) or merge unrelated nodes.
- **Alternatives**: content-addressed ids (breaks when source rows update); UUIDs (HydraDB
  ids are integers); using the GID string itself as `id` (rejected — string ids are not
  documented HydraDB behavior; this corrected an earlier HYDRADB_COMPATIBILITY example,
  `MERGE (n {id: row.gid})`, which is now `row.id` numeric).
- **Hashing policy**: algorithm + version fixed at Phase-1 implementation (candidate xxh64
  variant; not implemented in Phase 0); input always includes type + origin + full key.
- **Collision policy**: pre-write check that any existing node at the same numeric `id`
  carries the same `gid`; mismatch aborts the batch and logs the pair
  (`tg_id_collisions`-style additive table); resolution is a versioned re-key — **a collision
  never silently merges two source entities**.
- **Trade-offs**: hash-collision handling code; GID scheme becomes a compatibility contract;
  numeric ids are not human-readable (mitigated by the `gid` property).
- **Consequences**: GID/hash algorithm versioned; collisions abort loudly; relationship
  identity is handled separately (see D12 and the relationship-idempotency rules in
  GRAPH_ONTOLOGY §3.4 — HydraDB relationship uniqueness is not assumed).

## D6 — Identity matching requires evidence; no name-only merges; analyst-only confirmation
- **Decision**: `IDENTIFIED_AS` edges exist only through the R1–R5 evidence rules with a
  CANDIDATE→CONFIRMED/REJECTED state machine. **All CONFIRMED/REJECTED transitions are
  analyst actions** — no rule combination (R1+R2, R1+R3, or any "dual-rule corroboration")
  auto-confirms, and **GSTIN equality (R3) is candidate evidence only that must never serve
  as the second confirming rule**. Multiple candidates at the same matching priority stay
  ambiguous. Graph CONFIRMED and dashboard "mapped" are distinct concepts — dashboard mapping
  (presentation-time, `api/internal/repo/mapping.go`) is preserved unchanged.
- **Reasoning (VERIFIED)**: 733 GSTINs map to >1 entity and 502 GST company names map to >1
  entity; all `entities.isin` NULL; the dashboard's own methodology (README
  matching-priority table, `vw_vyuha_l1_intelligence` ambiguous-safe resolution,
  `tender_announcement_matches` CONFIRMED/REJECTED workflow) already embodies evidence-first
  resolution. Similar names are weak evidence: "L&T" matches multiple distinct listed
  entities. An earlier ARCHITECTURE draft allowed automatic confirmation via "R1+R3
  dual-rule corroboration" — **superseded**: it contradicted the ontology and would have
  auto-confirmed through exactly the signal the audit discredits.
- **Alternatives**: automatic fuzzy merge (rejected — silently corrupts financial
  attribution); single deterministic winner per name (rejected — hides ambiguity); dual-rule
  auto-confirmation (rejected — R3 is not confirmable evidence, and no other combination has
  verified justification).
- **Trade-offs**: slower coverage growth (analyst in the loop); more edges (rejected
  candidates are retained); no invented confidence scores.
- **Consequences**: every identity edge carries evidence GIDs; reconciliation tests must
  reproduce the dashboard's MATCHED/AMBIGUOUS/UNRESOLVED partition; the one-confirmed-entity-
  per-vendor invariant is enforced (design only) by a PostgreSQL control-table constraint or
  application transaction — HydraDB enforces no uniqueness constraints.

## D7 — Source provenance must be retained on every graph fact
- **Decision**: `src_table`/`src_pk` on all nodes; `src_table`+PKs+`evidence_gid` on all
  derived/identity edges.
- **Reasoning (VERIFIED design constraint)**: "Graph data is a projection of existing source
  data" — provenance is what makes the projection auditable and rebuildable; also required to
  re-derive any graph fact after source corrections.
- **Alternatives**: provenance only in PostgreSQL (rejected — breaks graph-local audit);
  hash-only provenance (rejected — not human-traceable).
- **Trade-offs**: larger property payloads per element.
- **Consequences**: provenance properties are mandatory in the projection engine's golden
  tests.

## D8 — Temporal representation
- **Decision**: Business timestamps stored as epoch-millis integer properties (`*_ms`) with
  ISO strings for date-only fields; no invented validity intervals in Phase 0.
- **Reasoning (VERIFIED)**: HydraDB property values are int/float/bool/string only (no date
  type — verified); source tables carry point-in-time facts (contract_date, snapshot_date,
  announcement_time), not intervals; `aoc_date` is 100% empty and `fiscal_year` includes
  `TTM` — interval semantics would be invented data.
- **Alternatives**: Neo4j-style temporal types (unsupported); string-only dates (breaks
  range filtering, which HydraDB supports only via numeric comparison).
- **Trade-offs**: clients must convert; epoch-ms assumes UTC for tz-naive source timestamps
  (documented; needs confirmation from pipeline owner).
- **Consequences**: temporal queries use numeric comparison; FY rollups computed at
  projection time via `sqlf.FYExpr`.

## D9 — Graph synchronization via watermark-based batch sync (no broker)
- **Decision**: Synchronization is watermark-driven batch projection executed by the Go
  ingestion worker; no Kafka/RabbitMQ/CDC is introduced now.
- **Reasoning (VERIFIED)**: no broker exists in this repo to reuse (the `~/rabbitmq` user
  directory belongs to unrelated work); source change semantics are PK/`updated_at`
  observable; HydraDB writes are idempotent MERGEs (verified), so replay-safe batch sync
  needs no queue; dataset sizes (tens of thousands of rows) are far below the scale that
  demands streaming CDC.
- **Alternatives**: logical replication/CDC (rejected for now — requires PostgreSQL config
  changes on shared production infra, a bigger operational ask); cron inside the API (no
  scheduler exists; deferred to Phase 1 with explicit approval).
- **Trade-offs**: minutes-level freshness; polling load on the shared DB (mitigated by the
  scoped service role (D14) + watermarks).
- **Consequences**: `tg_projection_watermark`/`tg_projection_batches` control tables;
  reconciliation job mandatory; interface stays compatible with a future CDC adapter.
- **Correction-pass update**: the original D9 sketch ("PK watermark for append-mostly,
  `updated_at` for updatable") was too coarse — see the per-table strategy in
  ARCHITECTURE §2.6: composite `(updated_at, id)` watermarks with bounded lookback for
  mutable tables, `MAX(id)` only for verified append-only tables (`announcements_raw`,
  `company_events` lack `updated_at` — schema-verified), full re-projection for
  low-volume mutable tables (`tender_announcement_matches`), `created_at`-based watermark
  for `entity_aliases` (no `updated_at`). Batches are replay-safe, idempotent,
  **at-least-once** — exactly-once is not claimed.

## D10 — Contractor assignment is not bid participation (`CONTRACTOR_ON_RECORD`)
- **Decision**: `tenders.contractor_v2_id → contractor_v2.id` is projected as
  `Vendor -[CONTRACTOR_ON_RECORD]-> Tender`: the contractor **of record named on the tender
  row**. It must never be read as proof of bid participation, bid submission, financial
  ranking, a winner flag, or an award. The name `PARTICIPATED_IN` (used in the original
  ontology) overstated the source and is superseded.
- **Reasoning (VERIFIED)**: the source is a single FK on the tender row to a contractor
  record; nothing in the schema or in `api/internal/repo` derives participant evidence from
  it. Bid participation has its own verified source: `tender_participants` (22,865 rows,
  `tender_id` FK, bid numbers, financial ranks) → `Bid -[SUBMITTED_BID]-> Tender`.
- **Alternatives**: keep `PARTICIPATED_IN` (rejected — semantically false); drop the
  contractor edge entirely (rejected — the assignment is real, auditable data; a named
  contractor-of-record relationship is honest); merge Vendor into Bid (rejected — different
  row identities, no verified per-row link).
- **Trade-offs**: dashboard consumers may expect a winner signal from the vendor-tender link;
  the edge carries an explicit meaning note in the ontology and never participates in `WON`
  or winner-evidence rules.
- **Consequences**: contractor assignment, bid submission, financial rank, winner flag,
  award record, and positive winner evidence remain six distinct concepts; bidder-network
  analysis derives only from `tender_participants` rows plus supported identity links.

## D11 — GeM: `GemContract` nodes only; no `Seller` nodes in the baseline
- **Decision**: each `seller_details` row projects to one `GemContract` node
  (`contract_no` = business dedup key, DB-unique — verified constraint). Seller attributes
  (company_name, GSTIN, PAN, gem_seller_id, MSME number) remain **on the contract node** as a
  snapshot. No `Seller` node and no `SELLS_VIA_CONTRACT` edge exist in the baseline.
- **Reasoning (VERIFIED)**: a contract row is contract-level information, not a seller
  identity. Live probe (2026-11-14): **all seller identity fields are empty — 0 non-null of
  4,078 rows for `gem_seller_id`, `gst_in`, `pan`, `company_name`**. There is therefore no
  stable seller identity key: not the UUID row id (per-contract), not `contract_no`
  (identifies the contract), and none of the seller fields exist. The original model (one
  `Seller` node per row) would have created 4,078 pseudo-seller nodes with no identity
  content.
- **Alternatives**: Seller-per-row (rejected — row ≠ seller); Seller keyed on GSTIN/name
  (rejected — fields empty, and GSTIN/name are never globally unique anyway); defer the whole
  GeM domain (rejected — contracts/categories are real data with a verified category FK).
- **Trade-offs**: seller-level questions ("all contracts by seller X") are unanswerable until
  a stable seller identity model exists; documented as a Phase-1+ gap requiring new evidence.
- **Consequences**: GeM stays a separate island domain (`IN_CATEGORY` edges only); no
  identity merging with procurement vendors; sellers are never merged on GSTIN-only or
  name-only equality even when populated.

## D12 — Co-bid evidence as `CoBidObservation` nodes (not `Bid→Bid` edges)
- **Decision**: for each pair of `tender_participants` rows sharing a tender, project one
  derived `CoBidObservation` node (`COBID:v:<tender_pk>:<min_pk>:<max_pk>`; carries
  `tender_pk`, `left_pk`, `right_pk`, both endpoint gids) with two `CO_BID_IN` edges, one
  from each participating `Bid` (`Bid→Obs`). The earlier direct `CO_BID_WITH` (Bid→Bid)
  derived edge is superseded.
- **Reasoning (VERIFIED)**: HydraDB batch edge writes dedupe on `(src,dst,type)` (documented)
  and relationship `MERGE`-by-`id` uniqueness is under-specified. Vendors A and B appearing
  together in 12 tenders would collapse to one edge under `(src,dst,type)` dedup —
  destroying the tender-level evidence needed to explain the relationship and compute repeat
  co-bidding. Observation nodes survive batch replay: one node per (tender, pair), keyed by
  the node upsert `id`.
- **Alternatives**: Option A — one relationship per shared tender with a composite identity
  (rejected — depends on unverified relationship-MERGE uniqueness); Option C — aggregated
  edge with count + tender list (rejected — stores derived state that can drift from
  observations and must not discard the underlying evidence); bidirectional edge pairs
  (rejected as redundant — Cypher matches `Bid→Obs←Bid` undirected patterns; one direction
  per participant suffices).
- **Trade-offs**: node fan-out (85 participants/tender → ~3.5k pairs per tender worst case,
  bounded and batched); two edges per observation.
- **Consequences**: per-tender evidence is retrievable; repeat co-bidding = count of
  observation nodes per pair; a co-bid pattern is a statistical observation only and is
  **never labeled collusion** without independent evidence; participant-name-to-vendor
  identity is resolved only through the R1–R5/analyst pipeline, never assumed.

## D13 — Monetary precision: exact decimal string canonical + float analytics
- **Decision**: every monetary field is stored twice: `<field>_exact str` — the canonical
  **exact decimal string** preserving source precision — and a float analytics copy
  (`_inr`/`_crore`/bare `num`). Procurement values (varchar, `sqlf.NumValExpr` semantics)
  are in **rupees**; financial-statement values (`numeric(20,2)`) are in **Rs crore**;
  `market_cap` in Rs crore, `current_price` in Rs/share; GeM `contract_value` in rupees.
  Units are explicit per field; no silent conversion. Exact arithmetic stays in PostgreSQL
  or Go decimal code; floats are never claimed exact.
- **Reasoning (VERIFIED)**: source columns mix varchar-parsed procurement values and
  `numeric(20,2)` financials; HydraDB property values are int/float/bool/string only —
  no decimal type — so a float-only graph representation would lose precision silently.
- **Alternatives**: integer minor units (rejected — mixed/unknown scales and units make a
  single paise-based integer misleading); float-only (rejected — precision loss, e.g. large
  crore sums); strings-only (rejected — breaks HydraDB numeric comparison/aggregation).
- **Trade-offs**: doubled property count for monetary fields; graph-side `sum()` runs on
  floats with documented precision limits — exact totals are PostgreSQL/Go computations.
- **Consequences**: mapping tables, ontology node table, and examples carry the `_exact`
  form; analytics that must be audit-exact are computed against PostgreSQL.

## D14 — PostgreSQL permission model: single narrowly scoped service role (Option B)
- **Decision**: one `tendergraph_svc` role: SELECT on the explicitly enumerated source
  tables/views the projection reads, plus INSERT/UPDATE/SELECT on the explicitly approved
  `tg_*` control tables; **no write permission of any kind on existing production source
  tables**. Analyst write API endpoints stay **blocked** until an authentication and
  authorization design is approved (an API key is a temporary operational control, not a
  user authorization model). No roles are created or changed in Phase 0.
- **Reasoning (VERIFIED constraint)**: the earlier "separate read-only role" recommendation
  was internally inconsistent — the service must also write `tg_*` control tables, which a
  strictly read-only role cannot do.
- **Alternatives**: Option A — separate `tg_reader` + `tg_control_writer` roles (viable;
  stronger blast-radius separation, but two credential sets and more provisioning surface;
  documented as the fallback).
- **Trade-offs**: one credential can both read source data and write control tables —
  mitigated by enumerated grants (no schema-wide ALL), additive-only `tg_*` tables, and
  keeping the API path read-only toward both stores.
- **Consequences**: provisioning script (Phase 1) must enumerate the grant list;
  `tg_identity_candidates` writes happen via the service or direct DB access by analysts
  (audited by `reviewed_by`) until the auth design ships.

---

## Superseded / corrected decisions (correction pass, 2026-11-14)

| Superseded statement (where) | Superseding decision | Why |
|---|---|---|
| ARCHITECTURE §2.3 "CONFIRMED/REJECTED transitions … from analyst actions or dual-rule corroboration (R1+R3)" | D6 (analyst-only transitions) | R3/GSTIN must never auto-confirm; 733 multi-entity GSTINs verified |
| GRAPH_ONTOLOGY/DATA_MAPPING `PARTICIPATED_IN` from `tenders.contractor_v2_id` | D10 (`CONTRACTOR_ON_RECORD`) | FK is an assignment, not bid participation |
| GRAPH_ONTOLOGY/DATA_MAPPING `CO_BID_WITH` (Bid→Bid derived edge) | D12 (`CoBidObservation` + `CO_BID_IN`) | `(src,dst,type)` batch dedupe collapses shared-tender evidence |
| GRAPH_ONTOLOGY/DATA_MAPPING `Seller` node per `seller_details` row | D11 (`GemContract` only) | Row ≠ seller; all seller identity fields verified empty |
| DATA_MAPPING money = float-only (`*_inr`/`*_crore`) | D13 (`_exact` string + float) | No decimal property type in HydraDB; exactness must be preserved |
| ARCHITECTURE §7 "separate read-only role" | D14 (Option B service role) | Read-only role cannot write `tg_*` control tables |
| HYDRADB_COMPATIBILITY example `MERGE (n {id: row.gid})` (string id) | D5 (numeric hash id; gid as property) | String ids not documented in HydraDB |
| HYDRADB_COMPATIBILITY vector row "No graph-side vector search" (undifferentiated) | D3 (OSS engine: no vector code — verified; commercial product claims: unverified) | Product claims vs self-hosted engine must not be conflated |
| D9 coarse watermark sketch (PK vs updated_at only) | Per-table strategy in ARCHITECTURE §2.6 | Three tables lack `updated_at`; mutable tables need lookback + reconciliation |
