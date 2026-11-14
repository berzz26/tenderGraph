# TenderGraph Phase 7 Report — Graph Evolution & Analyst Feedback

**Date:** 2026-11-14  
**Status:** implemented and validated against the development PostgreSQL control plane; HydraDB remains intentionally untouched.

## 1. Problem addressed

Phases 2–6 produced source-backed candidate relationships and graph analysis,
but did not provide a durable way to distinguish:

1. what PostgreSQL source records say;
2. what deterministic system matching suggests; and
3. what an analyst has explicitly concluded.

Phase 7 adds a PostgreSQL-first review workflow. Candidate evidence remains
source/system evidence. Analyst decisions are separate assertions with an
explicit rationale, evidence references, version and audit history.

## 2. Actual candidate availability

The existing Phase 5 generator was rechecked against the current database.
The additive refresh generated **46** `VENDOR_CORPORATE_ENTITY` candidates:

| Relationship type | Candidates | Status |
|---|---:|---|
| `VENDOR_CORPORATE_ENTITY` | 46 | `PENDING` |
| `TENDER_ANNOUNCEMENT` | 0 | no source candidates available |

The vendor/entity candidates include unique GSTIN matches, ambiguous GSTIN
matches and normalized-name matches. The current database still has no rows in
`tender_announcement_matches`, so no tender/announcement candidates were
fabricated. The generator supports that relationship type when source mapping
rows become available.

One real pending example was inspected but not decided:

- candidate ID `7937540005892414247`;
- Vendor `VENDOR:cp:1429`;
- CorporateEntity `ENTITY:v:6d545c6a-e303-4b97-a843-e79a9f2048cd`;
- method `GSTIN_EXACT_AMBIGUOUS`;
- GSTIN source evidence `GST:v:13591`;
- the GSTIN maps to two entities.

No analyst decision was invented for this candidate. The live control plane has
zero decisions, zero history rows and zero outbox rows after test cleanup.

## 3. Schema and migration

Created and applied additively in development:

`api/migrations/003_tendergraph_analyst_feedback.sql`

It creates:

### `tg_relationship_candidates`

Stores deterministic candidate identity, source/target object types and IDs,
source GIDs, generation method, initial evidence, latest evidence, generator
version and current review state. The logical key is `candidate_gid`, while the
numeric `candidate_id` is the deterministic non-negative Phase 2 FNV-1a-derived
ID of that complete GID.

Refreshing a candidate updates `latest_evidence` and evaluation timestamps only.
It never changes `current_status` or `current_version`, so analyst decisions are
not overwritten by source refresh.

### `tg_relationship_decisions`

Contains one current decision per candidate. It stores status, optional analyst
identifier, rationale, evidence references, decision version, request ID and
timestamps. Analyst identity is nullable because no reliable authenticated
identity context exists in the current CLI/application.

### `tg_relationship_decision_history`

Append-only decision events store previous status, new status, actor, rationale,
evidence references, version, request ID and timestamp. Normal decision
operations never update or delete history rows.

### `tg_graph_projection_outbox`

Stores durable desired graph state without performing a HydraDB mutation. Each
candidate decision version produces a deterministic projection record:

- `CONFIRMED` → `UPSERT_CONFIRMED_RELATIONSHIP`;
- `REJECTED` or `NEEDS_REVIEW` → `NO_CONFIRMED_RELATIONSHIP`.

The latter is an explicit desired state and does not delete source-derived graph
edges. Projection provenance includes the candidate GID, decision status/version,
decision/history IDs and submitted evidence references.

## 4. Candidate-generation behaviour

`ReviewStore.RefreshCandidates` is a single PostgreSQL transaction and is
idempotent.

For `VENDOR_CORPORATE_ENTITY` it uses:

- exact contractor GSTIN ↔ `entity_gst.gstin` matches;
- `GSTIN_EXACT_UNIQUE` when one entity maps to the GSTIN;
- `GSTIN_EXACT_AMBIGUOUS` when multiple entities map to the GSTIN;
- conservative normalized contractor business-name ↔ entity-name matches;
- `MULTI_EVIDENCE` when one logical pair has more than one generator method.

Evidence includes source tables, source primary keys, source GIDs, GSTIN and
ambiguity count. A normalized participant name is never used to create a
Vendor candidate.

For `TENDER_ANNOUNCEMENT`, only existing rows in
`tender_announcement_matches` are imported as candidates. Their source match
status is evidence, not an analyst decision. No date proximity or unsupported
announcement inference is performed.

Candidate IDs and GIDs are deterministic. Running refresh twice produced 46
rows both times, with 46 distinct candidate GIDs.

## 5. Decision lifecycle

Supported statuses are:

- `CONFIRMED`;
- `REJECTED`;
- `NEEDS_REVIEW`.

An untouched candidate starts as `PENDING`. A decision requires a non-empty
rationale and valid JSON evidence references. The current implementation allows
different-state supersession with optimistic version checking; the same state
cannot be written twice as a new decision. `NEEDS_REVIEW` is the explicit
reopen state.

Examples of valid transitions:

```text
PENDING       -> CONFIRMED
PENDING       -> REJECTED
PENDING       -> NEEDS_REVIEW
CONFIRMED    -> NEEDS_REVIEW or REJECTED
REJECTED     -> NEEDS_REVIEW or CONFIRMED
NEEDS_REVIEW -> CONFIRMED or REJECTED
```

Every decision, candidate update, history insert and outbox insert occurs in one
PostgreSQL transaction. `--expected-version` prevents stale concurrent actions.
`--request-id` makes retries idempotent; a repeated request returns an
idempotent result instead of appending another history event.

## 6. CLI commands

From `api/`:

```bash
# Refresh source-derived candidates; does not touch HydraDB.
graphintel refresh-candidates

# Default queue is pending, with bounded pagination.
graphintel review-queue --type VENDOR_CORPORATE_ENTITY --status PENDING --limit 25 --offset 0

graphintel candidate <candidate-id>
graphintel history <candidate-id>

graphintel decision <candidate-id> \
  --status NEEDS_REVIEW \
  --reason "GSTIN is ambiguous across multiple entities" \
  --evidence-json '[{"source_table":"entity_gst","source_pk":"13591"}]' \
  --expected-version 0 \
  --request-id review-2026-11-14-001
```

The CLI opens only PostgreSQL for review commands. It does not initialize a
HydraDB writer and does not attempt graph mutation. Candidate and decision JSON
always display source/target GIDs, generation method, evidence and state
separately.

## 7. Graph projection semantics

The desired future graph effect is:

```text
confirmed Vendor–CorporateEntity
  -> eventual analyst-provenance IDENTIFIED_AS relationship

rejected or needs-review candidate
  -> no confirmed analyst relationship
```

The outbox is not a broker and has no worker in this phase. It is a durable
desired-state table that a later explicitly approved projection operation could
consume once HydraDB writes are available. Candidate edges are never represented
as confirmed graph edges. Existing source-derived relationships are not changed
by review decisions.

## 8. Test coverage and actual results

Passed:

```text
go test ./...
go vet ./...
go test -tags integration ./internal/intel -count=1 -v
```

Unit tests cover:

- deterministic candidate GIDs and IDs;
- non-negative candidate identity;
- valid and invalid decision transitions;
- exact state separation rules through the decision service.

The live PostgreSQL integration test uses a clearly marked `TEST:CANDIDATE`
control-plane fixture, not a source record or analyst finding. It verified:

- pending → confirmed;
- idempotent retry with the same request ID;
- confirmed → needs review;
- needs review → rejected;
- stale optimistic-version conflict;
- three immutable history events;
- three desired-state projection records;
- cleanup of only the test control-plane rows.

No HydraDB writes were attempted. Existing Phase 2–6 unit and read-oriented
tests remained passing.

## 9. Known limitations

- No authenticated analyst identity is available; `analyst_id` is optional text
  and must be supplied by a future authenticated application context.
- There is no frontend or HTTP review API in scope; the CLI is the operational
  interface.
- Candidate refresh does not automatically delete or reject candidates that
  disappear from source evidence. Existing candidates retain auditability and
  require an explicit future stale-candidate policy.
- `TENDER_ANNOUNCEMENT` remains empty until the existing source mapping table
  receives suitable rows; no heuristic date matching is implemented.
- No HydraDB mutations or outbox worker were implemented because the known
  LocalFileSystem `PutMode::Update` failure remains unresolved.
- The outbox records desired state but does not yet reconcile against live graph
  state.
- Candidate generation currently covers the two approved Phase 7 relationship
  types only.

## 10. Recommended next phase

After review, the next engineering step should be a controlled outbox consumer
and reconciliation design that can apply analyst-confirmed relationships only
after HydraDB mutation support is repaired and verified in an isolated graph.
That work should preserve the same candidate/decision/history separation and
must not turn analyst assertions into unqualified source facts.

Phase 8 was not started.
