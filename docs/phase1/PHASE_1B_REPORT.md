# Phase 1B Report — Application Foundation & HydraDB Integration

**Status:** complete (pending review)
**Date:** 2026-11-14
**Scope:** documentation-only prerequisite met in Phase 0; this phase built the
Go application-side integration with the running HydraDB instance. No
ingestion, ontology, projection, frontend, or PostgreSQL-schema work.

---

## 1. Files created / modified

### Created (all in `api/`)

| File | Contents |
|---|---|
| `internal/graph/config.go` | `HYDRADB_*` env resolution (incl. `HYDRADB_AUTH_TOKEN_FILE`), validation, timeouts, token-redacting `String()` |
| `internal/graph/log.go` | secret-redaction helpers (`redact`, `redactURL`) + `graph:`-prefixed stdlib logging per repo convention |
| `internal/graph/result.go` | decoded `Result` (columns/rows/cursor/bookmark/epoch) + typed cell-envelope decoder |
| `internal/graph/hydrahttp.go` | HTTP query client: verified endpoint, bearer auth, namespace header, context + per-request timeout, envelope + error decoding, `QueryPage` continuation |
| `internal/graph/bolt.go` | Bolt client via official Neo4j Go driver (Bolt client only); driver init, verify, parameterized read/write, value flattening, close |
| `internal/graph/health.go` | reachable-vs-ready health checker (4 probes, aggregate status) |
| `internal/graph/graph.go` | `Open(cfg)` facade returning Bolt + HTTP + Health; never blocks startup on HydraDB |
| `internal/graph/config_test.go`, `log_test.go`, `hydrahttp_test.go`, `health_test.go` | unit tests (mocked, no live dependency) |
| `internal/graph/integration_test.go` | live integration tests (`//go:build integration`), disposable ids 977000..977999, verified cleanup |
| `internal/graph/README.md` | Bolt-vs-HTTP capability matrix, config, health, run/test instructions, verified quirks |
| `cmd/graphhealth/main.go` | standalone health CLI (JSON report, exit 0 only when healthy) |
| `graph.env.example` | example env without real secrets |

### Modified

| File | Change |
|---|---|
| `api/go.mod`, `api/go.sum` | added direct dep `github.com/neo4j/neo4j-go-driver/v5 v5.28.6` (used strictly as a Bolt client, per the fixed architecture) |

### Explicitly untouched

- `api/main.go` — no diff (verified). The existing Vyuha app does not import
  `internal/graph` and cannot be made unavailable by HydraDB.
- PostgreSQL: no migrations, no roles, no schema or data changes. The proposed
  additive `tg_*` migration design remains a Phase-0+ proposal only.
- HydraDB runtime: nothing started, stopped, or reconfigured.

Repository state at time of report: only the files above are new/modified;
no unrelated changes were touched.

---

## 2. Actual HydraDB version and connection details (verified, not assumed)

- **Build:** `0.1.0+916637477f3f` (commit `916637477f3f34766b4905b270bd4292109d77e6`,
  branch `main`, `dirty=false`, debug build) — read from `/metrics`
  (`graph_build_info`).
- **Process:** `graph-node` (pid 19511 at time of testing), running from
  `/home/berzz/hydradb` (Phase 1A deployment).
- **Endpoints verified live:**
  - Bolt: `127.0.0.1:7687`
  - HTTP query API: `http://127.0.0.1:8443` (`/healthz` → `{"status":"ok"}`, GET-only)
  - Admin: `http://127.0.0.1:19090` (`/readyz` → 200 empty body; `/metrics`)
- **Deviation from the phase brief:** the brief's prose listed admin port
  `9090`, but the brief's own env block and the live process env
  (`GRAPH_ADMIN_ADDR=127.0.0.1:19090`) both use **19090**; port 9090 is
  occupied by an unrelated process (404 on `/readyz`). The package therefore
  defaults to **19090**.
- Graph routing: database/namespace `default`, cell `cell-0` (verified: every
  HTTP query requires `cell_id`; namespace is sent as header
  `x-graph-namespace`, confirmed in `crates/protocol/src/http.rs`).

---

## 3. Verified HTTP contract (ground truth: live probes + installed source)

Request/response shapes were verified with live `curl` probes **and** by
reading the installed source (`src/client/http.rs`, `src/client/service.rs`,
`crates/bolt-server/src/session.rs`, `crates/protocol/src/http.rs`).

- Endpoint: `POST /v1/graphs/{graph}/query`.
- Request body: `{cell_id (required), query, parameters?, query_id?,
  bookmark?, read_epoch?, timeout_ms?, page_size?, cursor?, consistency?}` —
  `page_size`/`cursor` are **body** fields, not URL parameters (confirmed in
  `src/client/http.rs`; contradicts an early Phase 0 example).
- 200 response: `{query_id, columns, rows, read_epoch, next_cursor,
  bookmark, statuses?}`; every cell is a typed envelope
  `{"type":"vertex_id|integer|signed_integer|float|boolean|string|list|null","value":…}`
  (`HttpQueryValue` in `src/client/http.rs`). All are decoded by the client.
- Errors: 400/401/403/500 → `{"error":{"code","message"}}` envelope
  (`APIError` type); **422 body-deserialization failures are plain text** —
  both shapes handled and unit-tested.
- Auth: `Authorization: Bearer <token>`; missing/invalid → 401 envelope.
- Continuation: the same body is re-sent with `query_id` and
  `cursor = next_cursor` (verified in source tests; the client's `QueryPage`
  implements this).
- NDJSON streaming exists only via `Accept: application/x-ndjson` — **not
  implemented in this client** (limitation, §8).

### Verified server dialect constraints (deviations from Phase 0 doc examples)

| Constraint | Live behavior |
|---|---|
| Reads | must be `MATCH … RETURN`; bare `RETURN 1 AS x` rejected |
| Aggregation | `count(*)` works; `count(n)` rejected |
| Writes | **only** the UNWIND batch mutation form; single-shot `CREATE/MERGE` is rejected ("write query is not executable by the mutation engine") |
| Node upsert | `MERGE (n {id: row.id})` pattern matches **only id**; labels applied via `SET n:Label` |
| Inline lists | `UNWIND [1,2,3]` rejected; batch input must be a parameter |
| Edge create | endpoints must carry **exactly one label**; relationship properties must include `id: row.<field>` |
| Delete | `UNWIND $rows AS row MATCH (n {id: row.id}) DETACH DELETE n` |
| `UNWIND … MATCH … RETURN` | rejected as "one-hop relationships only" (batch write path) — cleanup verification therefore uses `MATCH … WHERE n.id IN $ids RETURN count(*)` |
| Unknown graph | 403 envelope (`permission_denied`); unknown cell → 500 envelope (`internal`) |

---

## 4. Architectural decisions

1. **Flat single package** `api/internal/graph/` with one file per
   responsibility (config / log / result / http / bolt / health / facade),
   matching the repo's flat `internal/<pkg>` style (`cfg`, `db`, `srv`,
   `repo`) rather than nested `client/bolt/http` subpackages.
2. **Raw driver objects do not leave the package.** Bolt results are decoded
   into plain Go values (scalars; nodes/rels flattened to
   element-id/labels/props maps) in `decodeBoltValue`.
3. **Bolt auth = bearer scheme.** Verified in
   `crates/bolt-server/src/session.rs`: schemes `none|bearer|basic`; with
   `basic`, an empty principal is an authentication error (found live).
   `neo4j.BearerAuth(token)` is used — same token as HTTP.
4. **Health distinguishes reachable vs ready.** HTTP probes: transport
   answered (`reachable`) vs 200 + correct body (`ok`). Bolt probe = driver
   handshake **plus** a real parameterized read, so a TCP-answering-but-broken
   Bolt never reports healthy. Aggregate: `healthy | degraded | unavailable`.
5. **Zero coupling of the existing app.** `main.go` untouched; the package is
   consumed stand-alone via `cmd/graphhealth`. HydraDB unavailability cannot
   break Vyuha (also verified in `TestIntHydraDBUnavailable`).
6. **Config defaults** match the live instance, admin on 19090 (§2).
7. **Timeouts:** driver `ConnectionAcquisitionTimeout`/`SocketConnectTimeout`,
   per-request HTTP context deadline, per-probe health timeout — all
   env-overridable (`HYDRADB_*_TIMEOUT_SEC`).
8. **Never log credentials:** `Config.String()` and all error paths redact;
   covered by dedicated tests (`TestConfigStringRedactsToken`,
   `TestAPIErrorNeverContainsToken`, `TestIntInvalidQueryError` token-leak
   check, CLI log line shows `auth=***redacted***`).

---

## 5. Commands executed

```sh
# dependency
go get github.com/neo4j/neo4j-go-driver/v5@v5.28.6 && go mod tidy

# build / vet (whole module)
go build ./...
go vet ./...                     # clean

# unit tests (no HydraDB needed)
go test ./internal/graph/        # ok  2.011s

# integration tests (live instance)
HYDRADB_AUTH_TOKEN_FILE=/home/berzz/hydradb/.hydradb/auth-token \
  go test -tags integration ./internal/graph/   # ok  0.175s

# CLI against the live instance
go build -o /tmp/opencode/graphhealth ./cmd/graphhealth
HYDRADB_AUTH_TOKEN_FILE=... /tmp/opencode/graphhealth   # exit 0, healthy
HYDRADB_HTTP_ADDR=http://127.0.0.1:9 /tmp/opencode/graphhealth  # exit 1, degraded

# post-test cleanliness
#   MATCH (n:IntegrationProbe) RETURN count(*)  → 0
git status --short              # only phase files; main.go untouched
```

---

## 6. Test results (all actually executed)

### Unit tests — PASS (`go test ./internal/graph/`, 20 tests)

Config: defaults, token-file, validation, redaction; URL userinfo redaction;
bolt scheme stripping. HTTP client: correct path/auth/namespace/cell-id,
typed decoding (vertex_id/signed/float/bool/string/list/null, incl. large
u64), API error envelope (400/401), plain-text 422, context timeout,
unreachable (network error ≠ APIError), malformed JSON. Health: all-healthy,
reachable-but-not-ready, unreachable, bolt-unconfigured, metrics-body
mismatch. Logging: truncate, token never in APIError.

### Integration tests — PASS (`go test -tags integration`, 10 tests, live)

| Test | What it proves |
|---|---|
| `TestIntBoltConnect` | Bolt handshake + bearer auth against live instance |
| `TestIntHTTPConnectAndHealth` | all four health probes healthy/ready live |
| `TestIntParameterizedReadAndDecode` | batch node upsert; parameterized read; correct decoding of vertex_id/integer/float/bool/string; envelope fields present |
| `TestIntBoltReadQuery` | HTTP-seeded data read back parameterized **via Bolt** |
| `TestIntBoltRelationshipWriteReadAndCleanup` | batch edge create (verified form), typed relationship read, retry-on-lag |
| `TestIntInvalidQueryError` | 400 `invalid_request` envelope; no token echo |
| `TestIntContextCancellation` | pre-cancelled + expired-deadline contexts on HTTP and Bolt fail fast |
| `TestIntHydraDBUnavailable` | closed-port behavior: Bolt/HTTP fail cleanly, health reports `unavailable`, no `APIError` misclassification, no token leak |
| `TestIntHealthReportAgainstLive` | live report is `healthy`, `ready=true` |
| `TestIntTokenNeverLogged` | redaction invariant on real config |

### Cleanup guarantee

Every writing test registers `cleanupProbe` (batch `DETACH DELETE` **then a
verified re-read returning count 0**). Post-run live check: `count = 0` for
the `IntegrationProbe` label; disposable ids confined to 977000..977999.
(An earlier failed run leaked nothing — the failed verify ran after a
successful delete.)

---

## 7. Acceptance criteria status

| Criterion | Status |
|---|---|
| Bolt connectivity works | ✅ (handshake + parameterized read, live) |
| HTTP communication works | ✅ (read + batch write + errors, live) |
| Parameterized queries work | ✅ both interfaces |
| Errors/timeouts handled | ✅ unit + live tests |
| Health distinguishes reachable vs ready | ✅ degraded/unavailable demonstrated via CLI and tests |
| Tests pass against running instance | ✅ 10/10 integration, 20/20 unit |
| Existing Vyuha functionality unbroken | ✅ `main.go` untouched; whole module builds; app has no new runtime dependency |
| No PostgreSQL source/schema changes | ✅ none performed |
| No unapproved infra changes | ✅ none performed (read-only against running HydraDB) |

---

## 8. Known limitations

1. **No NDJSON streaming client** — the server supports it
   (`Accept: application/x-ndjson`); the current client only speaks the JSON
   envelope. Add when a bulk path justifies it.
2. **No HTTP pagination loop** — `QueryPage` implements one continuation hop
   (verified contract); no auto-paging iterator yet.
3. **Relationship-read visibility lag** — once (live), a relationship read
   immediately after a batch edge write returned empty and was visible on
   retry; integration test retries ≤ 3 s. Watch in later phases; may be
   bookmark/epoch behavior rather than lag.
4. **Dialect guardrails are server-enforced** — reads must be
   `MATCH…RETURN`, writes batch-only; the package documents this but does not
   rewrite queries.
5. **`consistency` / `read_epoch` / `bookmark` fields** are transported but
   not exercised in tests beyond presence.
6. **Bolt write path untested against the live node** — Bolt reads/auth are
   verified live; all write flows were tested over HTTP (the server accepts
   the same batch forms over Bolt, but this was not separately proven).
7. **Integration tests require the dev token via env/file**; they skip
   otherwise (by design).

## 9. Deviations from the Phase 0 architecture docs

- Admin port default: **19090**, not 9090 (§2).
- `page_size`/`cursor` are body fields (Phase 0 example showed a URL param).
- Bare `RETURN`, `count(n)`, single-shot writes, inline UNWIND lists, and
  label-bearing `MERGE (n:Label {id})` patterns shown in Phase 0 examples are
  all rejected by this build; corrected forms are documented in the README
  matrix and encoded in tests.
- Bolt basic-auth with empty principal fails; bearer scheme is the verified
  auth shape.

These corrections are recorded in `api/internal/graph/README.md`; Phase 0
documents were left unmodified in this phase.

---

## 10. Out of scope (untouched, per the brief)

Ontology, PostgreSQL→HydraDB projection, bulk ingestion, GID hashing /
identity resolution, watermark sync, workers/schedulers, bidder algorithms,
contract-to-capital, BOQ intelligence, analyst APIs, frontend, production
deployment, new auth surfaces, `tg_*` migration execution — none implemented.
No Phase 2 work was started.
