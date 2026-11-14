# PROJECT_AUDIT.md — Vyuha Dash repository reconnaissance (Phase 0)

Date: 2026-11-14. All findings below were verified by reading the actual source files; every claim cites a path or symbol.

## 1. What this repository is

`vyuha-dash` is a small, two-component analytics application over an existing production
PostgreSQL database (`vyuha`):

| Component | Location | Language / framework |
|---|---|---|
| REST API | `api/` | Go 1.22, stdlib `net/http` (Go 1.22 method-pattern mux), `pgx/v5` |
| Dashboard | `frontend/` | TypeScript + React + Vite (React Router via `frontend/src/router.ts`) |

There is **no monorepo task runner, no CI config, no Dockerfile, no deployment manifest**
in the repo. The API runs with `go run .` (`README.md` §Run); the frontend is a Vite dev
server (`frontend/vite.config.ts` proxies `/api` → `:8091`) with a production build served
as static files by the Go API when `./dist` exists next to its CWD
(`api/main.go:75-79`, `infoStat`).

The database (`vyuha`, PostgreSQL 16.14 at `100.108.102.22:5433`) is a shared production
database used by a larger scraping/CRM system (Alembic-managed — `alembic_version`,
`scrapes`, `sources`, `customers`, `campaigns`, `delivery_history`, `auth_user` tables are
part of that other system). **TenderGraph must not disturb any of it.**

## 2. Directory map (with verified purpose)

```
vyuha-dash/
├── README.md                       # Pipeline description, matching priority table, API list, data facts
├── skills-lock.json                # agent skills manifest (not app code)
├── .agents/skills/                 # agent skills (not app code)
├── api/
│   ├── main.go                     # route table, CORS, static dist serving; entrypoint
│   ├── .env / .env.example         # DATABASE_URL, PORT, MINIO_* (gitignored), TEJHQ_API_KEY read at runtime
│   ├── go.mod                      # module vyuha/api; deps: pgx/v5 v5.6.0, minio-go/v7 v7.0.78
│   ├── cmd/market-ingest/          # EMPTY directory (no Go files) — intended market ingestion cmd, not implemented
│   ├── migrations/001_vyuha_intelligence.sql   # CREATE VIEW vw_vyuha_l1_intelligence + 2 tables (see §6)
│   └── internal/
│       ├── cfg/config.go           # env loading incl. minimal .env parser; Config{DatabaseURL, ServerAddr}
│       ├── db/db.go                # Connect(): pgxpool (MaxConns 8, Min 1, 1h lifetime, 15m idle)
│       ├── srv/server.go           # Server{Pool}; Query()/QueryOne() generic row-mapping helpers; paging helpers
│       ├── srv/handlers_fe.go      # financial-evaluation endpoints (participant-level)
│       ├── srv/handlers_awards.go  # awarded-contract endpoints
│       ├── srv/handlers_intelligence.go  # /intelligence/* endpoints over vw_vyuha_l1_intelligence
│       ├── srv/handlers_tenders.go # tender detail endpoint
│       ├── srv/handlers_files.go   # MinIO presigned-URL download for tender_files
│       ├── srv/handlers_market.go  # MarketHistory: live proxy to TejHQ OHLCV API (does NOT read market_daily_prices)
│       ├── srv/handlers_materiality.go   # materiality + data-quality endpoints
│       ├── srv/handlers_misc.go    # mappings, companies, overview KPIs (559 lines, several handlers)
│       ├── repo/queries.go         # Sort whitelists (Sorts maps) per endpoint
│       ├── repo/tenders.go         # tender detail query composition
│       ├── repo/awards.go          # award mapping CTEs (AwardMappingCTEs + AwardRowsSelect)
│       ├── repo/mapping.go         # participant mapping CTEs (ParticipantMappingCTEs + ParticipantRowsSelect)
│       └── sqlf/sqlf.go            # SHARED SQL FRAGMENTS: NormExpr, NumValExpr, FYExpr (the normalization contract)
└── frontend/src/                   # React pages incl. EntityResolution.tsx, pages mirror the API
```

## 3. Backend architecture (verified)

- **Framework**: plain `net/http` with Go 1.22 method+path patterns
  (`mux.HandleFunc("GET /api/v1/...")`, `api/main.go:32-66`). No router library, no
  middleware framework; a single `withCORS` wrapper (GET/OPTIONS only, `*` origin).
- **DB access**: one shared `*pgxpool.Pool` inside `srv.Server` (`api/internal/srv/server.go:22`).
  There is **no ORM**. All SQL is hand-written strings; rows are scanned generically into
  `[]map[string]any` by `Server.Query` with value normalization
  (`normalizeVal`, server.go:84 — numeric→float64, time→RFC3339, UUID→hex string).
- **SQL composition**: the entity-resolution logic is expressed as reusable CTE string
  fragments in `api/internal/repo/mapping.go` and `api/internal/repo/awards.go`
  (`ParticipantMappingCTEs`, `ParticipantRowsSelect`, `AwardMappingCTEs`,
  `AwardRowsSelect`), shared across list/summary/leaderboard handlers.
  **This is the de-facto entity resolution engine** and it is the most reusable module for
  TenderGraph.
- **Normalization contract**: `api/internal/sqlf/sqlf.go` — `NormExpr(col)` (lowercase,
  strip non `[a-z0-9&]`, `&`→`and`, remove legal-suffix tokens `pvt|private|ltd|limited|llp|limted|limte`,
  collapse whitespace), `NumValExpr(col)` (varchar money with Indian/western grouping → numeric),
  `FYExpr(dateCol)` (Indian fiscal year `FY2027`). Validated in production: 5973/5973 entities,
  ≈99.8% of participants/awardees. **TenderGraph identity resolution must reuse this, not re-invent it.**
- **Sorting safety**: sort keys are whitelisted per endpoint via `repo.Sorts` maps
  (`repo/queries.go:27-65`); user input reaches SQL only via `$n` placeholders.
- **Auth**: **none** on the API itself (no middleware, no sessions). `auth_user` exists in
  the DB and is referenced by `tender_announcement_matches.reviewed_by`
  (`api/migrations/001_vyuha_intelligence.sql:84`) — i.e., analyst identity is a DB concept,
  not yet an API concept.
- **Logging/errors**: stdlib `log.Printf`; SQL errors are logged server-side and surfaced as
  generic `{"error": msg}` JSON (`httpLog`, `fail`, `sendErr`).
- **Testing infrastructure**: **none**. No `_test.go` files exist in `api/`; the frontend has
  no test runner configured.

## 4. Background work and ingestion (verified)

- **No scheduler, no queue, no worker framework** in the API. There are no goroutines started
  in `main.go`; the server is synchronous request/response only.
- The only ingestion artifacts:
  - `api/cmd/market-ingest/` — **empty directory** (no Go files). Intent only.
  - `api/internal/srv/handlers_market.go` — `MarketHistory` **proxies live TejHQ data**
    (`https://api.tejhq.dev/v1/ohlcv/nse/{symbol}`) and deliberately does not read or write
    `market_daily_prices` (comment at handler top). The DB-side ingestion machinery
    (`market_data_ingestions`, `market_daily_prices`, source `tejhq`) exists in the shared
    production database and was presumably built by the scraping system, not by this repo.
  - MinIO object storage (S3-compatible, self-hosted) is used for tender files:
    `tender_files.cloud_file_name` → presigned GET (`handlers_files.go`). Env:
    `MINIO_ENDPOINT`, `MINIO_PUBLIC_ENDPOINT`, `MINIO_ACCESS_KEY/SECRET_KEY`,
    `MINIO_BUCKET=vyuha`, `MINIO_REGION` (`api/.env`).
- The shared DB carries pipeline state for the scraper system: `tender_states` (per-tender
  processing states incl. `title_desc_embedding_state`, `work_scope_generator_state`,
  `location_state`), `embedding_batch`, `boq_items.item_description_embedding vector(1536)`,
  `embeddings_title_desc vector(384)`. **pgvector is already installed** — this matters for
  the "no separate vector database" decision.

## 5. Dependencies & runtime requirements (verified)

Go API (`api/go.mod`): `jackc/pgx/v5 v5.6.0` (PostgreSQL 16 wire protocol), `minio-go/v7
v7.0.78`. Runtime: Go 1.22+, PostgreSQL 16, MinIO (S3 API), optional `TEJHQ_API_KEY` for live
market proxy. Frontend: Vite + React (see `frontend/package.json`).

## 6. Migrations (verified)

Single additive migration `api/migrations/001_vyuha_intelligence.sql`:
1. `CREATE OR REPLACE VIEW public.vw_vyuha_l1_intelligence` — the L1 intelligence view
   (participant mapping + winner evidence + outcome classification + financial/market join).
   Full definition verified in the live DB; see DATABASE_AUDIT.md §View.
2. `CREATE TABLE tender_announcement_matches` — analyst-reviewed tender↔announcement links
   with `match_status IN ('CANDIDATE','CONFIRMED','REJECTED')` — **the only existing
   confirmed/candidate/rejected identity workflow in the codebase**; a direct precedent for
   TenderGraph's identity-candidate model.
3. `CREATE TABLE market_daily_prices` with `CHECK`-constrained instrument types.

No migration runner exists in this repo; migrations are applied manually. The shared DB uses
Alembic (other system).

## 7. Existing reusable modules for TenderGraph

| Module | Path | Reuse |
|---|---|---|
| pgxpool bootstrap | `api/internal/db/db.go` `Connect` | direct reuse for the graph sync service's source connection |
| Env config + .env | `api/internal/cfg/config.go` | extend `Config` with HydraDB/graph fields |
| Generic row mapper | `srv/server.go` `Query`/`QueryOne`/`normalizeVal` | reuse for source-scan queries |
| Entity-mapping CTEs | `api/internal/repo/mapping.go`, `repo/awards.go` | source of *confirmed/candidate* identity pairs to seed `IDENTIFIED_AS` edges |
| Normalization / money / FY SQL fragments | `api/internal/sqlf/sqlf.go` | mandatory reuse in all projections and validations |
| Sort whitelists | `api/internal/repo/queries.go` | pattern for whitelist style in new query service |
| L1 intelligence view | `vw_vyuha_l1_intelligence` (DB) | precomputed procurement↔listed-entity intelligence; see DATABASE_AUDIT |
| tender_announcement_matches model | DB table | precedent for evidence + analyst review state machine |
| MinIO client pattern | `handlers_files.go` | same S3 store can host HydraDB object storage if deployed |
| Frontend pages | `frontend/src/pages/*.tsx` | eventual graph UI hooks (out of Phase 0 scope) |

## 8. Potential integration conflicts

1. **Shared production database.** `vyuha` is live production for the scraper/CRM system.
   The `prod` user can write. TenderGraph work must be read-only against these tables until
   explicit approvals; any new tables must not collide with existing names (checked: names
   like `tg_*` are free).
2. **`tender_parsed_meta` and `location_meta` are empty in production** (0 rows) even though
   the code and FKs reference them; `contractor` (v1) is also empty (0 rows), `contractor_v2`
   has 3257. Code paths touching v1 contractor or location joins are dead in practice.
3. **Market data mismatch**: the dashboard proxies TejHQ live, while the DB has
   `market_daily_prices` (0 rows) and `entity_market_data` (41,976 snapshot rows). A graph
   projection of market data should use `entity_market_data` snapshots, not `market_daily_prices`.
4. **API-less auth**: any new write endpoint (analyst decisions) has no auth layer to piggyback
   on; `tender_announcement_matches.reviewed_by` references `auth_user(id)` — needs a decision
   in Phase 1.
5. **No background-job infrastructure**: synchronization into HydraDB will need its own worker
   (no cron/K8s jobs/RabbitMQ present; a `rabbitmq` directory exists in the *user home
   directory*, not in this repo — not part of this application).
6. **ID type heterogeneity**: `tenders.id` integer, `entities.id` uuid, `announcements_raw.id`
   bigint, `seller_details.id` uuid — graph stable IDs must normalize these (see GRAPH_ONTOLOGY.md §IDs).

## 9. Recommended integration strategy

1. Keep `api/` as-is; add a **new Go service directory** `graph/` (same module conventions,
   same `internal/` layout) implementing: source adapter, projection engine, HydraDB client
   (Bolt via the official Neo4j Go driver `neo4j-go-driver`, which HydraDB explicitly
   supports), and a graph query HTTP surface. Rationale: Go is the established backend
   language; adding a framework/language is not justified by any requirement.
2. Add an additive-only migration `api/migrations/002_*.sql` for TenderGraph control tables
   (projection watermark, identity decisions) — no modification of existing tables.
3. Synchronization = batched Cypher via HydraDB's `UNWIND` upsert forms (verified batch
   semantics in HYDRADB_COMPATIBILITY.md), driven by source PK watermarks, not by a message
   broker (none exists to reuse).
4. Reuse `sqlf.NormExpr` and the repo CTE fragments as the single source of truth for
   name normalization and mapping priority so the graph never disagrees with the dashboard.
