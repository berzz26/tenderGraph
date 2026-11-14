# HydraDB graph integration (`internal/graph`)

Go integration between the Vyuha API and the running HydraDB instance.
Established in Phase 1B against **HydraDB `0.1.0+916637477f3f` (commit
`916637477f3f`)**; every contract below was verified against the installed
build and its source (`/home/berzz/hydradb/src/...`), not assumed.

The existing Vyuha API (`api/main.go`) is **not** wired to HydraDB — the app
keeps working when HydraDB is down. This package is used stand-alone
(`cmd/graphhealth`) today and can be adopted incrementally later.

## Files

| File | Purpose |
|---|---|
| `config.go` | `HYDRADB_*` env resolution, token-file support, validation, redacting `String()` |
| `log.go` | secret redaction helpers + `graph:`-prefixed logging (repo convention) |
| `result.go` | decoded `Result` + typed cell envelope decoding |
| `hydrahttp.go` | HTTP query client (verified request/response contract) |
| `bolt.go` | Bolt client via the official Neo4j Go driver (Bolt client only) |
| `health.go` | reachable-vs-ready health checks |
| `graph.go` | `Open(cfg)` facade returning Bolt + HTTP + health |
| `*_test.go` | unit tests (mocked) |
| `integration_test.go` | live integration tests (`-tags integration`) |

## Configuration

See `api/graph.env.example`. Defaults: Bolt `127.0.0.1:7687`, HTTP
`http://127.0.0.1:8443`, admin `http://127.0.0.1:19091`, database/namespace
`default`, cell `cell-0`. The token comes from `HYDRADB_AUTH_TOKEN` or
(preferred) `HYDRADB_AUTH_TOKEN_FILE`. Tokens are never logged; all package
error strings and `Config.String()` are covered by redaction tests.

## Bolt vs HTTP: what each interface supports (verified)

| Operation | Bolt (neo4j-go-driver v5.28.6) | HTTP (`POST /v1/graphs/{graph}/query`) |
|---|---|---|
| Parameterized reads (`MATCH … RETURN`) | ✅ | ✅ |
| Bare `RETURN 1` | ❌ row engine requires `MATCH … RETURN` | ❌ same |
| `count(*)` | ✅ | ✅ (`count(n)` rejected) |
| Writes | transport works, but server only accepts **UNWIND batch mutation form** | same; single-shot write patterns are rejected |
| Node upsert | `UNWIND $rows AS row MERGE (n {id: row.id}) SET n:Label, …` (labels via `SET`, pattern matches only id) | identical |
| Edge upsert/replay | `UNWIND $rows AS row MATCH (a:Label {id: row.src}), (b:Label {id: row.dst}) MERGE (a)-[r:TYPE {id: row.rid}]->(b) SET r.prop = row.prop` (verified across separate requests in Phase 2/3; endpoints need exactly one label) | identical |
| Delete | `UNWIND $rows AS row MATCH (n {id: row.id}) DETACH DELETE n` | identical |
| Inline lists (`UNWIND [1,2,3]`) | ❌ batch input must be a parameter | ❌ |
| Pagination / continuation | driver cursor (PULL/DISCARD) | body fields `page_size` + `cursor`; continuation re-sends the same body with `query_id` + `cursor` (= `next_cursor`) |
| NDJSON streaming | n/a | `Accept: application/x-ndjson` (**not implemented in this client yet**) |
| Namespace routing | database name | `x-graph-namespace` header |
| Auth | Bolt `bearer` scheme, same token | `Authorization: Bearer <token>` |
| Error shapes | driver `Neo4jError` | 400/401/403/500 → `{"error":{code,message}}` (`APIError`); **422 is plain text** |

Why both exist: HTTP is the primary, fully-verified path (all health, read and
write flows are tested over it). Bolt is exercised end-to-end for
connectivity, auth and parameterized reads so future phases can use it where
streaming/cursor semantics help.

Known runtime quirks (documented as Phase 0 deviations):

- A relationship read immediately after a batch edge write was observed empty
  once (visible on retry) — the integration test retries for up to 3 s.
- Batched relationship `CREATE` is not replay-safe across separate requests in
  the verified build; use deterministic relationship ids with the batch
  `MERGE` form above. This is runtime evidence, not a claim of a general
  relationship uniqueness constraint.
- Unknown graph → `403` envelope; unknown cell → `500` envelope.
- `POST /healthz` → 405 (GET only); admin `/readyz` → 200 with empty body.

## Health (`health.go`)

`HealthChecker.Check(ctx)` probes:

1. `GET {admin}/readyz` — node readiness (200, empty body).
2. `GET {admin}/metrics` — must return a `graph_*` metric body.
3. `GET {http}/healthz` — must return `{"status":"ok"}`.
4. Bolt: driver handshake **plus** a real parameterized read
   (`MATCH (n:GraphHealthProbe) RETURN count(*) AS c`).

Report: `healthy` (all four ok, `Ready=true`), `degraded` (reachable but not
fully ready), `unavailable` (nothing answered). Network failures are never
reported as `APIError`; a TCP answer alone never yields `healthy`.

## Running

Standalone health CLI (exits 0 only when healthy):

```sh
cd api
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphhealth
```

## Tests

Unit tests (no HydraDB needed):

```sh
cd api && go test ./internal/graph/
```

Integration tests (need the running instance; token via env or token file;
writes use label `IntegrationProbe` and disposable ids 977000..977999 with
verified cleanup):

```sh
cd api
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token \
  go test -tags integration ./internal/graph/
```

They skip automatically when HydraDB is unconfigured, so CI without the
dependency stays green.

## Usage sketch

```go
cfg := graph.LoadGraphConfig()
clients, err := graph.Open(cfg) // validates config; no network I/O for Bolt yet
if err != nil { ... }
defer clients.Close(ctx)

rep := clients.Health.Check(ctx) // reachable vs ready
res, err := clients.HTTP.DoQuery(ctx,
    "MATCH (n:MyLabel {id: $id}) RETURN n.id AS id",
    map[string]any{"id": int64(42)})
```
