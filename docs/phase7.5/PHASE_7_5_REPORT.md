# TenderGraph Phase 7.5 Report — Reliability, Persistence & End-to-End Reconciliation

**Date:** 2026-11-14  
**Status:** Implemented with disposable-runtime validation. The populated legacy HydraDB process still requires a controlled restart with the corrected binary before this gate can be called clean.

## 1. Scope and safety

This phase was implemented against the approved TenderGraph ontology. PostgreSQL source tables and source records were not changed. The populated HydraDB store at `/home/berzz/hydradb/.hydradb` was not deleted, reset, or used for destructive experiments.

Mutation tests used a disposable HydraDB store under `/tmp/tendergraph-phase75-hydra.oh7z3X` and unique PostgreSQL control-plane fixtures. Cleanup removed only those test fixtures. No analyst decision was fabricated for a real candidate.

## 2. Repositories and runtime inspected

Inspected:

- TenderGraph: `/home/berzz/vyuha-dash`.
- HydraDB source: `/home/berzz/hydradb`.
- Phase 0–7 reports and compatibility documentation.
- Projection, ingestion, reconciliation, analyst review, outbox, CLI, migration and integration-test code.
- HydraDB object-store, writer-lease, HTTP error, query and mutation paths.

HydraDB source HEAD is `9166374` (described locally as `v0.3.0-10-g9166374`). The development graph is `default`, cell `cell-0`; HTTP query is `127.0.0.1:8443`, Bolt is `127.0.0.1:7687`, and the current admin endpoint is `127.0.0.1:19091`. Disposable validation used HTTP `18443`, Bolt `17687`, admin `19092`.

| Issue | Classification | Correction / result |
|---|---|---|
| Local writes failed on `PutMode::Update` | Confirmed defect | Added a single-writer local adapter with conditional metadata checking and atomic overwrite delegation. |
| HTTP returned generic internal errors | Confirmed defect | Preserved error class/status and logged operation context without exposing backend paths. |
| Endpoint changes could leave old edges | Confirmed limitation | Added bounded exact-ID stale-edge reconciliation for affected endpoints. |
| Reconciliation progress was not durable per batch | Confirmed limitation | Added cursor, record-count and checkpoint timestamps. |
| Phase 7 outbox had no consumer | Missing capability | Added explicit dry-run/apply/reconcile consumer and CLI. |
| History immutability was application-only | Missing protection | Added a PostgreSQL append-only trigger. |
| Analyst authentication was absent | Known limitation | Added explicit actor ID and actor kind; CLI actors are `OPERATOR_SUPPLIED`. |
| Candidate evidence could become stale | Known limitation | Added `STALE`, same-transaction observation time and initial/latest evidence separation. |

## 3. HydraDB write failure and correction

The original failure was reproduced from the running node:

```text
Operation `put_opts` with mode `PutMode::Update`
not yet implemented by LocalFileSystem(...)
```

The failure is in `object_store 0.14.2`'s LocalFileSystem implementation. HydraDB requests conditional update semantics from persistence paths including writer lease, scope directory and index storage. LocalFileSystem deliberately returns `NotImplemented` for that mode, so the failure occurs during persistence.

Added `src/engine/local_store.rs`, selected only for `CLOUD_PROVIDER=local` from `src/engine.rs`. The adapter:

1. serializes same-process operations by path;
2. reads current metadata while holding the path lock;
3. compares requested ETag/version preconditions;
4. returns a precondition error for stale or missing objects; and
5. delegates successful replacement to LocalFileSystem's staged atomic overwrite.

This is correct for the existing single graph-writer lease model. It does not claim correctness for multiple independent local writer processes. Remote stores retain their native conditional-update behavior.

## 4. Disposable HydraDB write/read/restart matrix

A disposable graph node was exercised through the actual HTTP query API. Passed:

| Operation | Result |
|---|---|
| Create/read/update node | properties persisted correctly |
| Replay node MERGE | no duplicate logical node |
| Create relationship | endpoint traversal returned it |
| Replay relationship MERGE in separate requests | one endpoint relationship remained |
| Delete isolated test node | deletion persisted |
| Restart graph node | surviving state remained; deleted state remained absent |

The disposable analyst outbox integration also created a Vendor–CorporateEntity edge, verified it, changed desired state to `NEEDS_REVIEW` and then `REJECTED`, verified the confirmed edge was absent, and cleaned the test-owned nodes.

The current HydraDB dialect still does not bind relationship variables in `RETURN`. Live verification therefore checks relationship type/endpoints, not relationship properties. The current dialect also rejects some labelled relationship-delete forms; the implementation uses the verified anonymous endpoint plus deterministic relationship-ID form.

## 5. Error handling

`src/client/http.rs` now maps internal failures to explicit classes such as `storage_failure`, `kernel_failure`, `graph_corruption` and `graph_failure`. Storage/routing/fencing failures use HTTP 503; other internal failures use HTTP 500. Client messages do not expose filesystem paths or secrets, while server logs retain the underlying error and class.

A focused HTTP error test was added. The clean feature-enabled HydraDB test build was started after reclaiming generated build artifacts; its final result is reported in Section 10.

## 6. Graph synchronization corrections

`HydraWriter.ReconcileEdges` and `DeleteEdges` now support bounded stale-edge cleanup. `graphsync` invokes this before writing a changed source batch and scopes it to affected source endpoints:

- tender organization and contractor assignment;
- participant submission and co-bid;
- award and winner;
- alias and GST-registration relationships.

The cleanup reconstructs the old deterministic relationship ID from endpoint GIDs and source provenance PK, then deletes only that exact relationship. It never deletes all edges of a type or performs graph-wide cleanup.

Co-bid updates additionally inspect the other incoming edge of an affected old observation so a participant move does not leave the other bid attached to an obsolete observation. Obsolete observation nodes are not mass-deleted; they remain detectable by reconciliation.

Hard source deletions remain detection-only. The inspected schema has no reliable tombstone stream. A missing row in a bounded scan is not treated as deletion. Safe deletion requires a future source manifest/tombstone or an explicitly invoked complete reconciliation/repair operation.

## 7. Durable reconciliation progress

`tg_reconciliation_runs` now records `cursor_time`, `cursor_pk`, `records_scanned` and `checkpoint_at`. Reconciliation checkpoints after every keyset source batch and closes the run with counts and examples.

This provides durable progress evidence and safe at-least-once reruns. The current reconciler still reconstructs expected maps from the beginning after restart; it does not persist the full expected inventory. That limitation is explicit.

Relationship properties remain unavailable for live read-back, so reconciliation verifies endpoint/type coverage and node provenance while relationship provenance is write-time/unit-test verified only.

## 8. Phase 7 projection outbox consumer

Added `api/internal/intel/projection_outbox.go` and explicit commands:

```bash
graphintel projection status
graphintel projection list --status PENDING --limit 25
graphintel projection apply --limit 10 --dry-run
graphintel projection apply --limit 10 --status FAILED
graphintel projection reconcile --limit 100
```

The consumer:

- claims bounded rows with `claimed_by`/`claimed_at`;
- checks current candidate state/version under a PostgreSQL row lock;
- marks older deliveries `SUPERSEDED`;
- applies only `CONFIRMED` desired state;
- treats `REJECTED` and `NEEDS_REVIEW` as `NO_CONFIRMED_RELATIONSHIP`;
- removes only earlier analyst projection IDs for the same candidate;
- never targets source-derived relationships;
- verifies endpoint/type visibility after writes; and
- leaves failures retryable with error and attempt metadata.

Dry-run does not initialize HydraDB. No worker or broker was introduced.

## 9. Analyst workflow hardening

Migration `api/migrations/004_phase75_reliability.sql` was applied additively to development. It adds:

- `STALE` candidate status;
- `actor_id` and `actor_kind` to decisions/history;
- `SUPERSEDED` outbox status and claim columns;
- a database append-only history trigger; and
- reconciliation checkpoint columns.

Candidate refresh obtains its observation boundary with PostgreSQL `SELECT now()` inside the same transaction. It updates `latest_evidence`, preserves `initial_evidence`, does not overwrite decisions, and marks unseen version-zero candidates stale. A later refresh can restore an untouched stale candidate to pending when evidence reappears; reviewed candidates are not silently reopened.

There is no authenticated analyst identity in the application. Every decision requires an actor identifier and records `OPERATOR_SUPPLIED`, `AUTHENTICATED` or `SYSTEM`; the CLI uses `OPERATOR_SUPPLIED`.

## 10. Tests and actual results

Passed in TenderGraph:

```text
go test ./...
go vet ./...
go test -tags integration ./internal/ingest -count=1
go test -tags integration ./internal/intel -run 'TestLiveAnalystDecisionLifecycleUsesControlPlaneOnly|TestLiveAnalystProjectionOutboxAgainstDisposableGraph|TestLiveDecisionHistoryIsDatabaseAppendOnly' -count=1 -v
```

The focused live intel run passed with disposable HydraDB endpoints and token. It verified the PostgreSQL decision lifecycle, database-enforced history immutability, confirmed projection and rejected desired state.

The requested full tagged command was run:

```text
go test -tags integration ./... -count=1
```

It failed against the existing populated process: graph-write tests returned the old generic `hydradb http 500 internal: internal query execution error`. That process predates the corrected HydraDB binary and was intentionally not restarted against the populated store during this phase. The failure is recorded, not hidden.

HydraDB commands executed:

```text
cargo fmt --all -- --check
cargo check --locked --features http-api --tests
cargo build --locked --features server-runtime --bin graph-node
```

Results:

```text
cargo check --locked --features http-api --tests   # passed in 5m20s
cargo build --locked --features server-runtime --bin graph-node  # passed in 4m16s
~/.cargo/bin/just check                            # passed in 1m57s
cargo test --locked --features http-api phase75_tests::storage_failures_are_observable_without_exposing_backend_paths  # 1 passed
cargo test --locked engine::local_store::tests --lib                                                    # 2 passed
```

Existing warnings remain for unused code paths, including the dead-code delete-mode variants in `src/shard/write.rs`; no new warning was used to mask a failure.

## 11. Recovery and performance evidence

The disposable outbox integration completed in about 8.8 seconds including graph writes, verification and PostgreSQL transitions. This is a workflow smoke measurement, not a production throughput benchmark.

Phase 3 baselines remain the relevant full-dataset measurements: contractor raw projection about 395.7 rows/s, verified projection about 131.8 rows/s, and participant projection about 15.3 rows/s with read-after-write verification disabled. Phase 7.5 did not claim a new full-ingestion benchmark because the populated process was not restarted with the corrected binary.

Recovery remains at-least-once, not multi-request atomic: graph writes can precede checkpoint updates; retries replay deterministic identities; failed outbox rows remain retryable; older decision versions cannot overwrite newer candidate state; and synchronization watermarks do not advance past failed/unverified batches.

## 12. Files created or modified

TenderGraph:

- Added `api/migrations/004_phase75_reliability.sql`.
- Added `api/internal/intel/projection_outbox.go`.
- Modified `api/internal/intel/review.go`, `api/internal/intel/integration_test.go`, `api/cmd/graphintel/main.go`.
- Modified `api/internal/ingest/control.go`, `api/internal/ingest/engine.go`, `api/internal/ingest/reconcile.go`.
- Modified `api/internal/projection/hydra.go`.
- Updated `docs/phase0/HYDRADB_COMPATIBILITY.md`.
- Added this report.

HydraDB:

- Added `src/engine/local_store.rs`.
- Modified `src/engine.rs`, `src/client/http.rs`.

No source PostgreSQL table, source record, existing graph store or frontend was removed.

## 13. Remaining blockers and recommendation

Implemented and tested in isolation:

- local conditional persistence adapter;
- node/relationship update, replay, deletion and restart behavior;
- structured storage error mapping;
- bounded stale-edge cleanup for observed endpoint changes;
- durable reconciliation checkpoint writes;
- Phase 7 outbox consumer and CLI;
- actor/evidence/stale-candidate workflow protections.

Implemented but not fully validated against the populated graph:

- graphsync against the full graph with the corrected HydraDB binary;
- full tagged API integration suite after restarting the populated node;
- large-scale relationship-property provenance read-back.

Investigated but unresolved:

- automatic hard-deletion propagation without source tombstones;
- fully resumable reconciliation without rebuilding expected inventories;
- relationship-property read-back in the current runtime;
- safe in-place restart of the populated development process with the new binary.

Additional operational risk: the disposable tests covered process restart and
atomic staged replacement, but did not inject a host-level crash during an
fsync boundary. The local adapter is single-process-writer safe; multi-process
local-store concurrency remains unsupported.

The architecture is not ready for an unqualified Phase 8 start. Review should first approve a controlled restart/upgrade plan for the populated HydraDB process, rerun the full tagged suite and a selected graphsync replay, then decide whether the remaining runtime limitations are accepted.
