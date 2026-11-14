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
| `DECISIONS.md` | D1–D9 (mixed VERIFIED/PROPOSED, labeled) |
| `spike/hydradb_smoke.md` | Deployment recipe — NOT EXECUTED |
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
| Decisions | **RECORDED** | D1–D9 with alternatives and trade-offs |

## Technical blockers (BLOCKED)

1. **HydraDB runtime unavailable** — no Rust toolchain (`cargo`/`rustc` missing), no
   `libcypher-parser`/`libgraphblas`, no prebuilt image, no running node. The smoke test
   (Phase 0 §9) could not be executed. Unblocking options: (a) install toolchain + build
   from source (recipe in `spike/hydradb_smoke.md`), or (b) obtain a prebuilt image/binary
   from the maintainers.
2. **`api/migrations` applied manually** — no migration runner in this repo; Phase 1 needs a
   decision on how `tg_*` control tables are created (documented SQL + manual apply is the
   current convention).
3. **Read-only DB role** — the only available role is `prod` (read/write) on a shared
   production database; a dedicated read-only role should be provisioned before sync work.

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

**STOP — awaiting explicit approval before Phase 1.**
