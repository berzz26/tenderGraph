# PHASE_0_REPORT.md — TenderGraph Phase 0 final report

Date: 2026-11-14 · Auditor: GLM agent (OpenCode) · No production data or schema was modified.

## Deliverables produced (all under `docs/phase0/`)

| File | Status |
|---|---|
| `PROJECT_AUDIT.md` | VERIFIED findings, file/symbol-cited |
| `DATABASE_AUDIT.md` | VERIFIED via live `pg_dump` + read-only probes |
| `HYDRADB_COMPATIBILITY.md` | VERIFIED against official repo docs; environment test BLOCKED |
| `GRAPH_ONTOLOGY.md` | PROPOSED (grounded in verified schema) |
| `DATA_MAPPING.md` | PROPOSED (field-level, verified source columns) |
| `ARCHITECTURE.md` | PROPOSED |
| `DECISIONS.md` | D1–D14 (mixed VERIFIED/PROPOSED, labeled; superseded decisions recorded) |
| `spike/hydradb_smoke.md` | Deployment recipe — NOT EXECUTED (extended with ID/replay/consistency steps) |
| `CORRECTIONS.md` | Correction-pass record (finding → evidence → corrected decision → affected files) |
| `PHASE_0_REPORT.md` | this file |

## Status summary

| Area | Status | Key facts |
|---|---|---|
| Repository audit | **VERIFIED** | Go 1.22 + pgx/v5 + stdlib HTTP API (`api/`), React/Vite frontend; no ORM, no workers, no tests, no Docker/CI; reusable modules: `db.Connect`, `srv.Query`, `sqlf.{NormExpr,NumValExpr,FYExpr}`, `repo` mapping CTEs |
| Database audit | **VERIFIED** | Full schema dumped; 45 tables reviewed; 30 relevant; all FKs verified from `pg_constraint`; `vw_vyuha_l1_intelligence` definition verified in live DB (5,317 rows) |
| Data quality | **VERIFIED** | 15 findings (Q1–Q15), incl. 733 GSTINs mapping to >1 entity, all `isin` NULL, `aoc_date` 100% NULL, money-as-varchar, 10 empty schema-only tables |
| HydraDB compatibility | **VERIFIED (docs) / BLOCKED (runtime)** | Full capability matrix from current official docs (README, USING-HYDRADB, cypher-compat, architecture); dialect limits documented; runtime smoke test NOT EXECUTED — no toolchain/image in env |
| Graph ontology | **PROPOSED** | 17 node types, 16 relationship types, GID convention, evidence-backed identity rules R1–R5, provenance model, Mermaid diagram |
| Data mapping | **PROPOSED** | Field-level mapping for 20 source tables → graph; view role decided (validation + analytical projection, not source); dependency order and batching rules |
| Architecture | **PROPOSED** | 10 components defined (responsibility/I/O/dependencies/error handling/testing); watermark-based sync, no broker introduced |
| Decisions | **RECORDED** | D1–D14 with alternatives and trade-offs; superseded decisions tabulated with rationale |

## Technical blockers (BLOCKED)

1. **HydraDB runtime unavailable** — no Rust toolchain (`cargo`/`rustc` missing), no
   `libcypher-parser`/`libgraphblas`, no prebuilt image, no running node. The smoke test
   (Phase 0 §9) could not be executed. Unblocking options: (a) install toolchain + build
   from source (recipe in `spike/hydradb_smoke.md`), or (b) obtain a prebuilt image/binary
   from the maintainers.
2. **`api/migrations` applied manually** — no migration runner in this repo; Phase 1 needs a
   decision on how `tg_*` control tables are created (documented SQL + manual apply is the
   current convention).
3. **PostgreSQL service role** — the only available role is `prod` (read/write) on a shared
   production database. Recommended model (D14): one narrowly scoped `tendergraph_svc` role —
   SELECT on enumerated source tables + INSERT/UPDATE/SELECT on `tg_*` only, no writes to
   source tables. Provision before sync work.

## Unverified assumptions (NOT INVESTIGATED / needs confirmation)

1. Timezone of tz-naive timestamps (`tender_extracted_meta.*`, `announcement_time`) — assumed UTC.
2. Exact Go-driver Bolt handshake against a real graph-node (docs confirm Bolt 5.1–5.4
   support; runtime handshake untested here).
3. `algo.MSpaths` default `pairwise` value (docs: "default is unspecified" — code will set it
   explicitly).
4. `MERGE` edge-pattern acceptance inside batched `UNWIND MATCH … MERGE` (documented form
   used; verify in smoke test).
5. DataFusion engine variant behavior (we plan to stay on the default `legacy` engine).
6. Intent of empty `api/cmd/market-ingest/` (no code present).

## Dependencies required for Phase 1

1. HydraDB runtime (blocker 1) — needed for the deferred smoke test before any real ingestion.
2. Read-only PostgreSQL role + additive migration `002_tendergraph_control.sql`
   (`tg_identity_candidates`, `tg_evidence`, `tg_projection_watermark`, `tg_projection_batches`).
3. Neo4j Go driver (`neo4j-go-driver` v5) as the only new dependency (Bolt client).
4. Approval of the ontology (GRAPH_ONTOLOGY.md) and of DECISIONS D1–D9 by the team.
5. Auth decision for analyst endpoints (no auth layer exists today).

## Recommended implementation sequence (Phase 1)

1. Execute the HydraDB smoke test per `spike/hydradb_smoke.md`; record results in
   HYDRADB_COMPATIBILITY.md.
2. Additive migration for `tg_*` control tables (no existing table modified).
3. Build `graph/` Go service skeleton: config extension, HydraDB client (Bolt + HTTP),
   batch sender with verified error-handling rules.
4. Source adapter + projection engine for the core chain:
   GovernmentOrganization → CorporateEntity/Alias/GstRegistration → Vendor → Tender → Bid →
   Award, with reconciliation against `vw_vyuha_l1_intelligence`.
5. Identity resolution engine (R1–R5) + evidence service; analyst feedback endpoints
   (gated on auth decision).
6. Graph query service endpoints for bidder networks and contract-to-capital paths
   (`algo.SPpaths/MSpaths`), then BOQ price intelligence.

## Compliance with execution boundaries

Not done (as instructed): no application implementation, no frontend work, no bulk
ingestion, no PostgreSQL modifications (only read-only SELECTs and a `pg_dump`), no
destructive migrations, no vector DB, no second graph DB, no refactors, no mock production
data, no integration claims without a real connection (the HydraDB connection was never
available; this is reported as BLOCKED, not claimed as working).

## Correction pass (2026-11-14, same day — documentation only)

An engineering review found contradictions between documents; every finding was re-verified
against the repository, the live database, and the current official HydraDB
repository/docs before correcting. Full record: `CORRECTIONS.md`; corrected decisions:
DECISIONS.md (D3/D5/D6 rewritten, D10–D14 added, superseded statements tabulated).

| # | Correction | Evidence | Status |
|---|---|---|---|
| C1 | Identity: removed all automatic confirmation (incl. "R1+R3 dual-rule corroboration"); R3/GSTIN is candidate-only evidence; transitions are analyst-only; state transitions + one-confirmed-entity invariant documented | 733 GSTINs → >1 entity (live probe); `api/internal/repo/awards.go` mapping priorities | CORRECTED |
| C2 | `tenders.contractor_v2_id` is a contractor-of-record **assignment**, not bid participation → `CONTRACTOR_ON_RECORD`; bidder networks derive from `tender_participants` only | Schema FK; repo mapping logic; DECISIONS D10 | CORRECTED |
| C3 | One vertex-ID convention: numeric = versioned 63-bit hash of complete `gid` string (collision check, `id_algo_v` versioning); fixed string-id example `MERGE (n {id: row.gid})` | HydraDB docs: node ids are non-negative integers; string ids undocumented | CORRECTED (hash impl Phase 1) |
| C4 | Relationship identity: HydraDB relationship-MERGE-by-id uniqueness under-specified; app-level idempotency documented; multi-observation facts → nodes | cypher-compat batch dedupe on `(src,dst,type)`; smoke-test step R added | CORRECTED |
| C5 | GeM: `seller_details` row = contract, not seller → `GemContract`-only baseline, `IN_CATEGORY` edge; no `Seller` node | Live probe: all seller identity fields 0/4,078 non-null | CORRECTED |
| C6 | Co-bid: `CoBidObservation` nodes (one per tender/pair) + `CO_BID_IN` edges replace `CO_BID_WITH`; no collusion inference | Batch-dedupe collapse demonstrated; DECISIONS D12 | CORRECTED |
| C7 | Per-table watermark strategy from schema-verified timestamps; at-least-once batch semantics; reconciliation spec | 15-table timestamp audit; ARCHITECTURE §2.6 | CORRECTED |
| C8 | Money: exact decimal string (`_exact`) canonical + float analytics; units explicit; no float-exactness claims | `numeric(20,2)` + varchar sources; HydraDB scalar-only values | CORRECTED |
| C9 | Vector capability: OSS engine has **no** vector code (741 paths scanned) vs commercial product claims — classified; no-vector-DB rationale re-grounded on pgvector + question fit | Repo tree scan; hydradb.com | CORRECTED |
| C10 | Permission model: single narrowly scoped service role (D14) replaces inconsistent "read-only role"; analyst write endpoints blocked pending auth | ARCHITECTURE §2.10/§7 | CORRECTED |
| C11 | Consistency claims demoted to "documented, runtime-unverified" where the smoke test hasn't run (bookmarks, strong consistency, delete behavior) | spike/hydradb_smoke.md steps B/D | CORRECTED |

**The HydraDB runtime blocker is unchanged: still BLOCKED, smoke test NOT EXECUTED.**
**Phase 1 has NOT started — no implementation work was done in this pass.**

Remaining unresolved decisions (post-correction):
- Hash algorithm selection + `id_algo_v` constant (Phase 1 implementation task).
- Whether HydraDB relationship `MERGE`-by-id gives replay uniqueness (smoke-test step R).
- Bookmark/strong-consistency and delete-visibility behavior (smoke-test steps B/D).
- Deleted-source-row handling (tombstone vs unlink vs remove) — after step D.
- Mutable/append-only classification per table — confirm against live update traffic.
- Auth/authorization design for analyst endpoints (Phase 1 decision).
- `pg_hydradb` deployment — out of bounds (needs `shared_preload_libraries` on shared PG).

**STOP — awaiting explicit approval before Phase 1.**
