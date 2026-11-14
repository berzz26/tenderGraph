# TenderGraph Phase 8 Report

## Status

Phase 8 has an implemented first application slice: a new evidence-first React/Vite investigation workspace backed by Go HTTP routes over the existing PostgreSQL, intelligence, review and projection services.

This is not a claim that every proposed Phase 8 capability is complete. The implemented slice is operational against the development database and the production frontend build passes. Full browser E2E automation, authenticated analyst identity and a production deployment are still open.

## Reconnaissance

The application repository is `/home/berzz/vyuha-dash`; the HydraDB source repository is `/home/berzz/hydradb`. The frontend is React 18 + Vite + TypeScript. The backend is Go 1.22 using pgx and the standard HTTP server. Existing Phase 4–7.5 intelligence services already own bidder, pair, network, corporate, review and outbox calculations, so the frontend calls those services rather than reimplementing business logic.

The supplied development runtime was used:

- HydraDB HTTP: `127.0.0.1:8443`
- HydraDB Bolt: `127.0.0.1:7687`
- HydraDB admin: `127.0.0.1:19091`
- API: `127.0.0.1:8091`

The live API smoke test returned actual company search results, company intelligence records, pending candidate evidence and PostgreSQL readiness. No source records were changed.

## Product architecture

```text
React/Vite investigation workspace
          |
          | bounded HTTP investigative operations
          v
Go API / srv
   |              \
   v               v
PostgreSQL       HydraDB HTTP client
source + tg_*    bounded graph traversal / projection status
```

The frontend preserves four information classes in its presentation:

- observed source facts;
- derived network statistics;
- system-generated candidate relationships;
- analyst assertions and their decision state.

## Implemented application surfaces

### Workspace

The previous dashboard entry point is replaced by an investigation workspace with search-first navigation, three investigative entry points and explicit guidance about observed, derived and asserted information.

### Unified search

`GET /api/v1/search?q=...` searches CorporateEntity records, Tender records and participant Bid records. Results retain their type and route to distinct investigation surfaces. The API uses PostgreSQL only; no search engine was introduced.

### Bidder network

Routes:

- `GET /api/v1/bidders/{bidID}`
- `GET /api/v1/bidders/{bidID}/co-bidders`
- `GET /api/v1/bidders/{bidID}/network?depth=2&limit=40`
- `GET /api/v1/networks/pair?left=...&right=...`

The UI presents participation counts, organization diversity, observed co-bidders, tender history and a bounded depth-two network neighborhood. Bidder records are explicitly described as participant observations rather than legal identities.

### Company intelligence

`GET /api/v1/companies/{entityId}/intelligence` exposes the existing Phase 5 `intel.Service.Company` result. The UI shows candidate vendor methods, GST ambiguity, contractor-on-record exposure, extracted award evidence and available financial/market context without relabelling exposure as revenue.

### Tender intelligence

The existing tender detail route remains the source of tender detail data. The redesigned UI presents actual participants separately from extracted award evidence and links each participant to its bounded bidder investigation.

### Analyst review

Routes:

- `GET /api/v1/review/candidates`
- `GET /api/v1/review/candidates/{candidateID}`
- `GET /api/v1/review/candidates/{candidateID}/history`
- `POST /api/v1/review/candidates/{candidateID}/decision`

The UI displays initial/latest evidence, relationship endpoints and candidate generation method. Decisions require a rationale and submit an explicit operator-supplied actor identifier. The existing PostgreSQL transaction, history and outbox semantics remain authoritative.

### Operational context

The workspace adds:

- `GET /api/v1/projection/status`
- `GET /api/v1/projection/list`
- `GET /api/v1/system/data-status`
- `GET /api/v1/system/health`

The health route currently reports PostgreSQL readiness and marks HydraDB as not checked; this avoids turning a UI health card into an unbounded graph probe. Existing graph health tooling remains available separately.

## Frontend redesign

The old dashboard shell and page navigation were replaced with a restrained editorial research interface:

- navy rail and paper canvas;
- Source Serif display hierarchy with DM Sans body text and DM Mono identifiers;
- explicit fact/candidate/warning badges;
- dense evidence tables with bounded scrolling;
- responsive layouts for desktop and narrow screens;
- visible focus states and reduced-motion handling;
- loading, empty and unavailable states;
- no invented KPI values or fake graph records;
- no emoji iconography.

The bidder network is intentionally rendered as a bounded, inspectable list of graph nodes rather than a decorative full-graph visualization. This keeps the actual Bid-node semantics visible and avoids implying direct company-to-company edges.

## Files created or modified

Created:

- `api/internal/srv/handlers_workspace.go`
- `docs/phase8/PHASE_8_REPORT.md`

Modified:

- `api/main.go`
- `api/internal/srv/server.go`
- `frontend/src/App.tsx`
- `frontend/src/api.ts`
- `frontend/src/styles.css`

The API now opens the configured HydraDB client when available and serves a built frontend from either `api/dist` or the sibling `frontend/dist` directory with SPA fallback.

## Validation performed

Passed:

```text
cd api && go test ./...
cd api && go vet ./...
cd frontend && npm run build
cd api && go test -tags integration ./... -count=1
```

Live API checks against development PostgreSQL:

```text
GET /api/health                                      -> {"status":"ok"}
GET /api/v1/system/health                            -> PostgreSQL ready
GET /api/v1/search?q=technology                     -> actual company/tender records
GET /api/v1/companies/{real-id}/intelligence         -> actual entity and financial records
GET /api/v1/review/candidates?status=PENDING&limit=1 -> actual candidate evidence
```

The frontend build produced a Vite production bundle successfully. A browser automation suite was not present in the repository, so browser-level E2E claims are deliberately not made.

## Startup

Backend:

```bash
cd /home/berzz/vyuha-dash/api
set -a; . .env; set +a
go run .
```

Frontend development:

```bash
cd /home/berzz/vyuha-dash/frontend
npm install
npm run dev
```

The Vite server proxies `/api` to `http://localhost:8091`. For a served build, run `npm run build` and start the Go API from `api/`; it detects `../frontend/dist`.

## Known limitations and remaining work

1. No authentication system exists. Analyst decisions are clearly marked operator-supplied, but the browser does not establish analyst identity.
2. The review UI uses a simple rationale prompt. A proper accessible confirmation dialog and evidence-reference editor should follow before production use.
3. The current first slice does not yet provide a full interactive canvas graph, pair comparison screen, organization investigation page, announcement timeline or projection-apply controls.
4. The system health endpoint does not invoke a HydraDB graph probe. Use the existing `graphhealth` command and HydraDB admin endpoints for operational health until a bounded, cached API probe is designed.
5. Existing Phase 7.5 runtime limitations around relationship-property readback and historical reconciliation remain unchanged.
6. No automated browser E2E tests or screenshot capture were available in this repository.

## Recommendation

The Phase 8 foundation is ready for iterative product review, not for a final production sign-off. The next implementation increment should add the accessible review dialog, pair comparison, tender organization views and browser E2E coverage while retaining the current evidence semantics. Phase 9 has not been started.
