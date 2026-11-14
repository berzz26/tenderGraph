# TenderGraph Phase 3 Report — Full Graph Ingestion & Synchronization

**Date:** 2026-11-14  
**Status:** implementation complete for the approved registry; production deployment and destructive repair remain out of scope.

## 1. Outcome

Phase 3 adds a bounded, sequential PostgreSQL-to-HydraDB synchronization engine.
It reuses the Phase 2 projection and HydraDB clients, adds durable `tg_*`
control-plane metadata, supports initial and incremental scans, and exposes
operational commands through `api/cmd/graphsync`.

“Full ingestion” here means complete ingestion of the approved graph ontology
and its validated source mappings. It does not mean copying every PostgreSQL
table into HydraDB. PostgreSQL remains authoritative, and unsupported or
ambiguous tables remain excluded.

PostgreSQL source tables are read-only. The only database schema change is the
additive migration `api/migrations/002_tendergraph_sync.sql`, which creates
control-plane tables and seeds dataset metadata.

## 2. Architecture

The implemented path is:

```text
PostgreSQL source tables
  -> SourceScanner (parameterized keyset queries)
  -> projection.Batch (deterministic GIDs, IDs, provenance)
  -> HydraWriter (nodes first, relationship MERGE second)
  -> bounded read-after-write verification
  -> checkpoint and watermark advancement in PostgreSQL tg_* tables
```

Writes are sequential per HydraDB cell. Each source batch is bounded to at
most 1,000 rows, with a default of 500. HydraDB requests are also capped at
1,000 items and normally use the configured batch size. No broker, scheduler,
second graph database or vector database was introduced.

The engine deliberately treats the sequence of HydraDB requests as
non-transactional. Replay safety comes from deterministic node IDs, collision
checks, relationship IDs, batch `MERGE`, read-after-write verification and
durable source cursors.

## 3. Dataset coverage

| Dataset | Projection | Cursor strategy | Dependencies | Deletion policy |
|---|---|---|---|---|
| `contractor_v2` | `Vendor` | `COALESCE(updated_at, created_at), id` | none | reconciliation only |
| `entities` | `CorporateEntity` | `COALESCE(updated_at, created_at), id` | none | reconciliation only |
| `entity_aliases` | `Alias`, `HAS_ALIAS` | `created_at, id` | entities | reconciliation only |
| `entity_gst` | `GstRegistration`, `HAS_GST_REGISTRATION` | `COALESCE(updated_at, created_at), id` | entities | reconciliation only |
| `tenders` | `Tender`, `GovernmentOrganization`, `ISSUED_BY`, `CONTRACTOR_ON_RECORD` | `COALESCE(updated_at, created_at), id` | contractor registry | reconciliation only |
| `tender_participants` | `Bid`, `SUBMITTED_BID`, `CoBidObservation`, `CO_BID_IN` | `COALESCE(updated_at, created_at), id` | tenders | reconciliation only |
| `tender_extracted_meta` | evidence-backed `Award`, `HAS_AWARD`, `WON` | `COALESCE(updated_at, created_at), id` | tenders and participants | reconciliation only |

The initial registry contains the seven mappings above. `seller_details` is
excluded because its seller fields do not establish the approved Vendor
identity semantics. Participant names do not create Vendor nodes. Financial,
market, BOQ, announcement and identity-confirmation pipelines remain excluded
until their source mappings and lifecycle rules are separately approved.

The Phase 2 semantic distinctions are preserved: contractor assignment is not
participation; `is_winner` alone does not create `WON`; and co-bidding is stored
through one observation node per tender and participant pair.

## 4. Control plane and checkpoints

Migration `002_tendergraph_sync.sql` creates:

- `tg_datasets`: registry metadata and enabled/batch configuration.
- `tg_sync_watermarks`: one compound cursor and status per dataset.
- `tg_sync_runs`: run mode, lifecycle, counters and terminal error.
- `tg_sync_batches`: cursor-before/after, row count, attempts and failure.
- `tg_reconciliation_runs`: bounded comparison results and examples.

For timestamp-backed datasets, the cursor is `(timestamp, primary key)`, not a
timestamp alone. Rows sharing a timestamp are ordered by the primary key. An
incremental run applies the configured one-hour lookback for mutable datasets;
the replayed range is intentional and idempotent. `entity_aliases` uses its
available `created_at` cursor and has no update timestamp.

The cursor comparator is type-aware for the approved sources: numeric primary
keys compare numerically, while UUID/text keys compare lexically. This was
validated during the live award scan when tied timestamps exposed the numeric
ordering case.

The watermark advances only after graph write, optional read-after-write
verification and successful batch checkpoint persistence. If the process exits
after a graph write but before checkpoint advancement, the next run reprojects
that range. A failed batch is retained with its attempt count and can be
replayed with `-op retry`.

The engine does not infer deletion from an incomplete scan. No reliable source
tombstone was found in the approved tables, so reconciliation reports stale
graph nodes without deleting them during ordinary synchronization.

## 5. Source mapping and provenance

All source queries are parameterized and read-only. Source primary keys are
preserved as `src_pk`; source tables and derivation joins are retained in
`src_table`. Every graph node includes `gid`, deterministic non-negative `id`,
`id_algo_v`, `src_table`, `src_pk` and `synced_at_ms`. Every edge includes the
same provenance fields plus relevant source join identifiers. Co-bid edges
record both participant endpoint provenance through their observation node.

Award projection reads `tender_extracted_meta` and uses
`vw_vyuha_l1_intelligence` only as a validation/reference projection for
positive winner evidence. An Award node is skipped when both awardee and
winning-bid evidence are absent. `WON` is emitted only for bid-number or
normalized-awardee matches.

Money remains exact as a string with rupee semantics and may carry a numeric
analytics copy. No source unit is silently converted.

## 6. Reconciliation

`graphsync -op reconcile -dataset <name>` performs bounded source scans and
compares expected node IDs/GIDs with label-scoped graph reads. It reports:

- missing nodes;
- missing relationships detected during batch verification;
- stale graph nodes;
- invalid node provenance; and
- bounded examples.

The comparison is resumable at the source-batch level, and results are stored
in `tg_reconciliation_runs`. `-op repair` is explicit and replays the selected
dataset. It does not mass-delete stale graph records. Duplicate logical edges
are detectable through the relationship verification row-count check, but a
separate global edge inventory is not yet implemented.

## 7. CLI

From `api/`:

```sh
go run ./cmd/graphsync -op list
go run ./cmd/graphsync -op initial -dataset tenders -batch-size 500
go run ./cmd/graphsync -op incremental
go run ./cmd/graphsync -op retry -dataset tender_participants
go run ./cmd/graphsync -op status
go run ./cmd/graphsync -op failed -dataset tender_participants
go run ./cmd/graphsync -op reconcile -dataset tender_participants -reconcile-mode detect
go run ./cmd/graphsync -op repair -dataset tender_participants
go run ./cmd/graphsync -op initial -dataset contractor_v2 -dry-run
```

All commands return a non-zero exit status on operational failure. Dry runs
scan and project in memory without graph or control-plane writes.

## 8. Verification and tests

Executed checks:

```text
go test ./...                         PASS
go vet ./...                          PASS
go test -tags integration ./...       PASS (including HydraDB replay and failure injection)
```

The Phase 3 source/control integration test connected to the live PostgreSQL
instance, scanned bounded records from all seven registered datasets, verified
compound cursors and control status rows, and did not modify source tables.

The live ingestion demonstration projected the real development datasets. It
also exercised a failed alias batch caused by an invalid verification query,
then replayed the dataset successfully after the Hydra verification path was
corrected. The failure remained visible in `tg_sync_runs` and the successful
replay created no duplicate logical graph records.

The integration failure-injection test writes a contractor batch, fails after
the graph write and before checkpoint advancement, asserts a failed batch
checkpoint, then replays the selected dataset successfully. The executed
failure run recorded 500 failed records; its recovery run projected all 3,257
contractors successfully.

Live reconciliation of `contractor_v2` completed with zero missing nodes,
missing edges, stale nodes or invalid provenance. A larger `tenders`
reconciliation detect attempt was interrupted after several minutes because
of HydraDB graph-read latency; it performed no writes and produced no
completed reconciliation record.

## 9. Performance baseline

Environment: Linux 6.8, x86_64, 4 CPUs; live PostgreSQL 16.14 and HydraDB
development build `0.1.0+916637477f3f`; default graph HTTP endpoint; sequential
writer; batch size 500.

| Run | Rows | Verification | Elapsed | Approx. rows/s |
|---|---:|---|---:|---:|
| `contractor_v2` | 3,257 | on | 19.87 s | 164 |
| `entities` | 5,973 | on | 37.88 s | 158 |
| `entity_aliases` replay | 6,889 | on | 127.91 s | 54 |
| `entity_gst` | 16,134 | off | 139.24 s | 116 |
| `tenders` | 26,125 | off | 326.42 s | 80 |
| `tender_participants` | 22,865 | off | 1,498.11 s | 15 |
| `tender_extracted_meta` recovery | 12,252 | off | 165.73 s | 74 |
| `tenders` recovery, 1,000-row batches | 25,832 | off | 381.46 s | 68 |

The participant and award runs completed. Co-bid observation generation and
relationship writes dominate participant latency. Peak resident memory
observed for the active Go process was about 61 MiB (RSS about 24 MiB at the
sample). The principal bottleneck is HydraDB relationship mutation/read
latency, not PostgreSQL table scanning.

The first award run stopped after 15,000 rows; its durable cursor was replayed
by the successful 12,252-row recovery run. The combined scans covered the
approved 26,124-row source through the high watermark, with the expected
lookback overlap.

## 10. Known limitations

- Source hard deletions have no reliable tombstone path; reconciliation is
  detect-first and non-destructive.
- HydraDB has no transaction across node and relationship requests, so recovery
  is at-least-once and verification-backed rather than atomic.
- Reconciliation currently has stronger node inventory checks than global edge
  inventory checks.
- The participant scanner recomputes affected co-bid pairs by a bounded join;
  this is correct but relationship-heavy for large participant batches.
- The current source queries use the existing schema indexes; no source index
  or source table was changed in this phase.
- Control-table migrations must be applied by the normal deployment migration
  process in environments other than the development database.

## 11. Files

Created:

- `api/migrations/002_tendergraph_sync.sql`
- `api/internal/ingest/{registry,cursor,control,source,engine,reconcile}.go`
- `api/internal/ingest/{ingest_test,integration_test}.go`
- `api/internal/projection/approved.go`
- `api/cmd/graphsync/main.go`
- `docs/phase3/PHASE_3_REPORT.md`

Modified:

- `api/internal/projection/hydra.go`: bounded writes, collision gates,
  read-after-write verification and verified relationship MERGE.
- `api/internal/projection/projection.go`: exported reusable node references
  and approved-source projection types.
- `api/internal/graph/README.md`: corrected relationship MERGE compatibility
  statement.
- `docs/phase0/HYDRADB_COMPATIBILITY.md`: recorded the live compatibility
  result for cross-request relationship replay.
- `README.md`: rewritten around TenderGraph and the Phase 3 operational CLI.

Removed: none.

## 12. Deviations from Phase 0 and Phase 2

The Phase 2 relationship `MERGE` statement is now treated as verified for the
running HydraDB build, while relationship uniqueness constraints remain
unclaimed. Phase 3 adds `Alias` and `GstRegistration` projections because their
source mappings are direct and provenance-preserving; it does not add
automatic Vendor-to-CorporateEntity confirmation. No broker, worker fleet,
frontend rewrite, full identity pipeline or production deployment was added.
