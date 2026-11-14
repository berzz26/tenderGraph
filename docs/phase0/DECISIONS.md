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

## D3 — No separate vector database
- **Decision**: Semantic search stays in PostgreSQL using the already-installed pgvector.
- **Reasoning (VERIFIED)**: pgvector is installed in the DB with real usage
  (`boq_items.item_description_embedding vector(1536)` — 1,200 populated;
  `embeddings_title_desc vector(384)`); HydraDB has no vector search. Adding Qdrant/Milvus
  would duplicate embeddings infrastructure with zero gap.
- **Alternatives**: dedicated vector DB (rejected — no capability gap).
- **Trade-offs**: vector search scales with PostgreSQL, not the graph tier.
- **Consequences**: BOQ similarity queries run against PostgreSQL; the graph stores no
  embeddings.

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

## D5 — Stable IDs are mandatory
- **Decision**: Every node and edge carries a deterministic GID (`<TYPE>:<origin>:<key>`)
  and an integer HydraDB `id` derived by a versioned 63-bit hash of the GID.
- **Reasoning (VERIFIED)**: HydraDB `id` is the only upsert key (no auto-generated ids;
  MERGE matches on id — verified); without stable IDs, re-runs duplicate relationships
  (plain CREATE duplicates verified) or merge unrelated nodes.
- **Alternatives**: content-addressed ids (breaks when source rows update); UUIDs (HydraDB
  ids are integers; algo procedures take integer ids).
- **Trade-offs**: hash-collision handling code; GID scheme becomes a compatibility contract.
- **Consequences**: GID algorithm versioned in control tables; collisions abort loudly.

## D6 — Identity matching requires evidence; no name-only merges
- **Decision**: `IDENTIFIED_AS` edges exist only through the R1–R5 evidence rules with a
  CANDIDATE→CONFIRMED/REJECTED state machine; multiple candidates at the winning priority
  stay ambiguous.
- **Reasoning (VERIFIED)**: 733 GSTINs map to >1 entity; the dashboard's own methodology
  (README matching-priority table, `vw_vyuha_l1_intelligence` ambiguous-safe resolution,
  `tender_announcement_matches` CONFIRMED/REJECTED workflow) already embodies
  evidence-first resolution. Similar names are weak evidence: "L&T" matches multiple distinct
  listed entities.
- **Alternatives**: automatic fuzzy merge (rejected — silently corrupts financial
  attribution); single deterministic winner per name (rejected — hides ambiguity).
- **Trade-offs**: slower coverage growth (analyst in the loop); more edges (rejected
  candidates are retained).
- **Consequences**: every identity edge carries evidence GIDs; reconciliation tests must
  reproduce the dashboard's MATCHED/AMBIGUOUS/UNRESOLVED partition.

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
- **Trade-offs**: minutes-level freshness; polling load on the shared DB (mitigated by
  read-only role + watermarks).
- **Consequences**: `tg_projection_watermark`/`tg_projection_batches` control tables;
  reconciliation job mandatory; interface stays compatible with a future CDC adapter.
