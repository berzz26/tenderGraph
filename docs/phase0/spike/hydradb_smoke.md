# HydraDB smoke-test spike — deployment recipe (NOT EXECUTED in Phase 0)

Status: **NOT EXECUTED**. At Phase 0 audit time this machine had no Rust toolchain
(`cargo`/`rustc` absent), no `libcypher-parser` or `libgraphblas` system libraries, and no
prebuilt HydraDB image or running node. Building from source installs a full toolchain — an
environment change that was not made without approval. Nothing below has been run; it is the
verified-from-docs recipe for the first Phase-1 task.

Two options:

1. **Build from source** (docs: README.md §Prerequisites / §Run a local server)
2. **`scripts/runtime_smoke.sh`** in the HydraDB repo (builds graph-node and exercises
   Bolt + HTTP on a throwaway store; needs Python `neo4j`).

## Option 1 prerequisites

```bash
sudo apt-get update && sudo apt-get install -y \
  build-essential clang libclang-dev cmake pkg-config \
  libcypher-parser-dev libgraphblas-dev curl git
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
cargo install just --locked
git clone https://github.com/hydra-db/hydradb.git /tmp/opencode/hydradb
cd /tmp/opencode/hydradb && just native-check
```

## Runtime configuration (single plaintext dev node, disposable store)

All values taken from the official README §Run a local server (verified quotes):

```bash
mkdir -p /tmp/opencode/hydra-smoke/store /tmp/opencode/hydra-smoke/cache
printf '%s\n' 'local-development-token-32-bytes' > /tmp/opencode/hydra-smoke/auth-token

export CLOUD_PROVIDER=local
export LOCAL_PATH=/tmp/opencode/hydra-smoke/store
export GRAPH_NAMESPACE=default
export GRAPH_ID=default
export GRAPH_CELL_ID=cell-0
export GRAPH_CELLS=cell-0
export GRAPH_NODE_ID=node-0
export GRAPH_BOLT_NODE_ADDRESSES=node-0=127.0.0.1:7687
export GRAPH_ADVERTISED_BOLT_ADDR=127.0.0.1:7687
export GRAPH_DATA_CACHE_DIR=/tmp/opencode/hydra-smoke/cache
export GRAPH_AUTH_TOKEN_FILE=/tmp/opencode/hydra-smoke/auth-token
export GRAPH_ALLOW_PLAINTEXT=true
export RUST_MIN_STACK=33554432   # required: async query futures overflow default stack

cargo run --locked --features server-runtime --bin graph-node
```

Endpoints: Bolt 7687 · HTTP 8443 · Admin 9090 (`/readyz`, `/metrics`; `/healthz` is on 8443).

## Smoke test script (`hydradb_smoke.sh`)

Runs the 8 mandated steps against IDs `999901/999902` (disposable range), then deletes them:

```bash
#!/usr/bin/env bash
set -euo pipefail
TOKEN='local-development-token-32-bytes'
Q=http://127.0.0.1:8443/v1/graphs/default/query
run() { curl -sS -X POST "$Q" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' --data "$1"; echo; }

run 'GET /readyz' 2>/dev/null || curl -sS http://127.0.0.1:9090/readyz -o /dev/null -w 'readyz:%{http_code}\n'

# 1-3. connect + two test nodes + typed relationship (single MERGE is idempotent)
run '{"cell_id":"cell-0","query":"MERGE (a:SmokeTest {id: 999901, name: $na})-[:SMOKE_LINK {weight: 7}]->(b:SmokeTest {id: 999902, name: $nb})","parameters":{"na":"alpha","nb":"beta"}}'

# 4. retrieve the relationship with MATCH
run '{"cell_id":"cell-0","query":"MATCH (a:SmokeTest {id: 999901})-[:SMOKE_LINK]->(b) RETURN b.id AS id, b.name AS name"}'

# 5. bounded path query (2-hop VLP; also exercises the batch upsert + edge batch)
run '{"cell_id":"cell-0","query":"UNWIND $rows AS row CREATE (a {id: row.src})-[:SMOKE_LINK]->(b {id: row.dst})","parameters":{"rows":[{"src":999902,"dst":999903}]}}'
run '{"cell_id":"cell-0","query":"MATCH (a:SmokeTest {id: 999901})-[:SMOKE_LINK*1..2]->(v) RETURN DISTINCT v.id AS id ORDER BY id"}'

# 6. native path procedure (verify argument syntax against cypher-compat.md)
run '{"cell_id":"cell-0","query":"CALL algo.SPpaths({sourceNode: 999901, targetNode: 999903, relTypes: [\"SMOKE_LINK\"], relDirection: \"outgoing\", maxLen: 2, pathCount: 1}) YIELD path, pathWeight, pathCost RETURN path, pathWeight, pathCost"}'

# 7. property verification (label-anchored, exact types)
run '{"cell_id":"cell-0","query":"MATCH (n:SmokeTest {id: 999901}) RETURN n.name AS name"}'

# 8. delete / isolate test data
run '{"cell_id":"cell-0","query":"UNWIND $ids AS id MATCH (n {id: id}) DETACH DELETE n","parameters":{"ids":[999901,999902,999903]}}'
```

## Additional steps required by the correction pass (ID / replay / consistency)

The corrected D5/D12/D6 assumptions introduced behaviors that the original 8 steps did not
exercise. Add the following steps (same disposable id ranges 999910–999930) before the
cleanup step:

```bash
# I1. string-id probe — verify string ids are rejected/accepted (docs: ids are non-negative
#     integers; TenderGraph must not rely on string ids either way)
run '{"cell_id":"cell-0","query":"MERGE (a:SmokeTest {id: \"gid-str-999910\"}) RETURN a.id AS id"}'
# I2. numeric-id precision — MERGE a node at a 63-bit masked id and read it back unchanged
run '{"cell_id":"cell-0","query":"MERGE (a:SmokeTest {id: 4611686018427387903}) SET a:SmokeTest RETURN a.id AS id"}'

# R1. relationship MERGE-by-id replay — run the same edge MERGE 3x, then count
run '{"cell_id":"cell-0","query":"UNWIND [1,2,3] AS i MATCH (a:SmokeTest {id: 999901}), (b:SmokeTest {id: 999902}) MERGE (a)-[r:SMOKE_RID {id: 555001}]->(b)"}'
run '{"cell_id":"cell-0","query":"MATCH (a:SmokeTest {id: 999901})-[r:SMOKE_RID]->(b:SmokeTest {id: 999902}) RETURN count(r) AS edge_count"}'
# EXPECT edge_count == 1 if MERGE-by-id provides replay uniqueness; >1 proves it does not.
# R2. batch (src,dst,type) collapse demo — same pair+type in two separate batches with
#     different intended observations; confirms dedupe collapses them (grounds D12):
run '{"cell_id":"cell-0","query":"UNWIND $rows AS row CREATE (a {id: row.src})-[:SMOKE_MULTI]->(b {id: row.dst})","parameters":{"rows":[{"src":999901,"dst":999902}]}}'
run '{"cell_id":"cell-0","query":"UNWIND $rows AS row CREATE (a {id: row.src})-[:SMOKE_MULTI]->(b {id: row.dst})","parameters":{"rows":[{"src":999901,"dst":999902}]}}'
run '{"cell_id":"cell-0","query":"MATCH (a:SmokeTest {id: 999901})-[r:SMOKE_MULTI]->(b) RETURN count(r) AS edge_count"}'
# EXPECT edge_count == 1 (documented dedupe) — the multi-observation evidence would be lost,
# which is why co-bids are CoBidObservation NODES (D12).

# B1. bookmark round trip — capture `bookmark` from a mutation response, pass it in the next
#     read's `bookmark` field, and confirm the read sees the write. Also try
#     {"consistency":"strong"} once. Gate: no read-your-writes reliance until this passes.

# D1. delete visibility — DETACH DELETE a node, then MATCH for it (bookmark-pinned and
#     default-consistency): confirm it is gone and that dependent edges are removed.
#     Gate: source-deletion handling in ARCHITECTURE §2.6 waits for this result.
```

Cleanup must include the extra ids: `999910`, `4611686018427387903`, and nodes reachable
from the R1/R2 edges.

Expected observations to record when run:
- Step 4 returns one row with `{"type":"vertex_id","value":999902}` (docs' verified shape —
  note `vertex_id`, not `integer`).
- Step 8 returns `columns: []`, `rows: []` — that is success for a mutation.
- If `MERGE` in step 3 is rejected in this build, fall back to the two-request recipe:
  node batch `UNWIND $rows MERGE (n {id: row.id}) SET n:SmokeTest, n.name = row.name` then
  the edge batch — and update HYDRADB_COMPATIBILITY.md accordingly.
- I1 result: if string ids are silently accepted or coerced, record it — the numeric-hash
  convention (D5) stays the decision either way, but the compatibility matrix must reflect
  actual engine behavior.
- I2 result: id must come back exactly `4611686018427387903` (no float coercion/precision
  loss in the HTTP path; repeat over Bolt in Phase 1).
- R1 result decides whether the projector may use MERGE-by-relationship-id or must rely
  purely on deterministic row-keyed replay (HYDRADB_COMPATIBILITY §3.2, §5).
- B1 result gates the §3 data-flow "verify" step and any read-your-writes claim
  (ARCHITECTURE §3, §6).
- D1 result gates the deleted-source-row handling decision (ARCHITECTURE §2.6).
