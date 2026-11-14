# TenderGraph Phase 3 Validation Report

**Date:** 2026-11-14  
**Validation scope:** live development PostgreSQL and HydraDB, approved graph ontology only  
**Gate status:** partially passed; source-derived relationship coverage is complete for the tested mappings, but HydraDB node-inventory and relationship-provenance limitations remain open.

## 1. Scope and safety

This validation treated “full ingestion” as complete ingestion of the seven
approved ontology mappings, not a copy of every PostgreSQL table. PostgreSQL
remained authoritative.

Before validation, Git status was inspected and existing user changes were
preserved. No PostgreSQL source table or source record was modified. No graph
data was deleted. The only graph writes were replay-safe selected ingestion
runs against already-owned development records; a disposable three-node/one-
edge HydraDB probe was created and completely removed to verify relationship
deletion behavior.

The runtime was HydraDB `0.1.0+916637477f3f`, graph `default`, cell `cell-0`,
HTTP `127.0.0.1:8443`. The verified runtime does not bind relationship
variables in `RETURN`, so relationship properties cannot be read back.

## 2. Dataset completeness matrix

Counts below are from live PostgreSQL. Expected graph counts apply the approved
projection rules; they are not raw-table-to-node comparisons.

| Dataset | Source rows | Eligible/projected source rows | Expected graph content | Live result | Watermark |
|---|---:|---:|---|---|---|
| `contractor_v2` | 3,257 | 3,257 | 3,257 `Vendor` nodes | 3,257 projected; contractor reconciliation: 0 missing/invalid | `2026-10-31 06:11:51.706787`, PK 3257 |
| `entities` | 5,973 | 5,973 | 5,973 `CorporateEntity` nodes | 5,973 projected | `2026-06-29 12:34:20.483420`, UUID `ffcf6a6b-fd6a-47aa-8236-b0af2241c74e` |
| `entity_aliases` | 6,889 | 6,889 | 6,889 `Alias` nodes and `HAS_ALIAS` edges | 6,889 projected; 6,889/6,889 edges | `2026-06-29 12:48:41.035548`, UUID `ffaa72f4-f418-4490-acef-82dc53f6b388` |
| `entity_gst` | 16,134 | 16,134 | 16,134 `GstRegistration` nodes and `HAS_GST_REGISTRATION` edges | 16,134 projected; 16,134/16,134 edges | `2026-07-16 03:20:45.814782`, PK 16180 |
| `tenders` | 26,125 | 26,125 | 26,125 `Tender` nodes; normalized `GovernmentOrganization` nodes; 26,125 `ISSUED_BY`; 6,656 contractor assignments | Tender reconciliation: 0 missing/invalid; 26,125/26,125 `ISSUED_BY`; 6,656/6,656 `CONTRACTOR_ON_RECORD` | `2026-10-31 14:14:17.574375`, PK 31901 |
| `tender_participants` | 22,865 | 22,865 | 22,865 `Bid`; 78,201 `CoBidObservation`; 22,865 `SUBMITTED_BID`; 156,402 `CO_BID_IN` | Relationships: 22,865/22,865 and 156,402/156,402; Bid node inventory timed out | `2026-10-31 14:14:19.147863`, PK 22873 |
| `tender_extracted_meta` | 26,124 | 20,329 | 20,329 evidence-backed `Award`; 20,329 `HAS_AWARD`; 3,927 `WON` | Relationships: 20,329/20,329 and 3,927/3,927; Award node inventory timed out | `2026-10-31 14:14:19.147863`, PK 31901 |

The participant pair count is independently confirmed by
`sum(n*(n-1)/2) = 78,201` across 5,154 tenders. The award eligibility rule
skips rows with neither awardee nor winning-bid evidence. The winner count is
3887 bid-number matches plus 40 normalized awardee-name matches. The reference
view reports 30 name matches and 3,887 bid matches; the ten-row difference is
documented as a source/view normalization discrepancy, not silently treated as
proof that the view and source projection are identical.

All seven control-plane watermarks are `SUCCEEDED` and equal the live source
high-watermark time/PK pairs. The earlier failed award, participant, alias and
injected contractor runs remain recorded in `tg_sync_runs`/`tg_sync_batches`;
they were not erased or reset.

## 3. Tender reconciliation

The original full label/edge scan was interrupted because broad HydraDB graph
reads exceeded the 30-second HTTP query budget. Investigation showed that the
dominant issue was not PostgreSQL scanning or application comparison: label-wide
counts and broad relationship scans were slow or timed out, while bounded
endpoint queries completed.

The reconciliation implementation was hardened to:

- compare source-derived node IDs/GIDs in bounded OR-by-ID reads;
- compare source-derived relationship endpoints in bounded source-ID groups;
- consume HydraDB HTTP continuation cursors rather than only the first page;
- report explicit limitations instead of claiming a global inventory; and
- preserve detect mode as write-free.

Complete tender detect reconciliation was then executed with 1,000-row source
batches. It scanned all 26,125 tenders and reported:

```text
missing_nodes=0
missing_edges=0
stale_nodes=0 (bounded source-derived check)
stale_edges=0
invalid_provenance=0 (node provenance)
duplicate_logical_edges=0
ISSUED_BY                 expected=26125 actual=26125 missing=0
CONTRACTOR_ON_RECORD      expected=6656  actual=6656  missing=0
```

The run took approximately four minutes. It did not establish a global stale
node/edge inventory: HydraDB label-wide scans remained impractical, and
relationship properties could not be returned. Thus this is complete
source-derived tender coverage, not proof that deleted-source graph records do
not exist.

The source scan itself is keyset-bounded, but the current reconciliation
control row is written at completion rather than after every comparison batch.
An interrupted reconciliation can be safely rerun from the beginning, but it
does not yet resume from a durable reconciliation cursor.

## 4. Global relationship reconciliation

The bounded endpoint inventory produced these results:

| Relationship | Expected | Actual | Missing | Duplicate logical | Provenance read-back |
|---|---:|---:|---:|---:|---|
| `ISSUED_BY` | 26,125 | 26,125 | 0 | 0 | unavailable in runtime |
| `CONTRACTOR_ON_RECORD` | 6,656 | 6,656 | 0 | 0 | unavailable in runtime |
| `SUBMITTED_BID` | 22,865 | 22,865 | 0 | 0 | unavailable in runtime |
| `HAS_AWARD` | 20,329 | 20,329 | 0 | 0 | unavailable in runtime |
| `WON` | 3,927 | 3,927 | 0 | 0 | unavailable in runtime |
| `HAS_ALIAS` | 6,889 | 6,889 | 0 | 0 | unavailable in runtime |
| `HAS_GST_REGISTRATION` | 16,134 | 16,134 | 0 | 0 | unavailable in runtime |
| `CO_BID_IN` | 156,402 | 156,402 | 0 | 0 | unavailable in runtime |

Alias, GST and tender reconciliation commands completed successfully. The
participant and award commands completed the relationship checks with zero
missing/duplicate endpoints, then failed during large `Bid`/`Award` node-ID
verification queries when HydraDB timed out. Their reports therefore remain
`FAILED` rather than being promoted to full reconciliation passes. This is an
important distinction: the relationship results are evidence-backed, while the
corresponding node inventory was not fully read back.

The runtime also prevents checking relationship `id`, `src_table`, `src_pk`,
or derivation properties after the write. Those fields are still written and
were verified by projection/unit tests, but not by live relationship-property
read-back. A complete stale-edge inventory for hard-deleted source rows is not
currently feasible without a faster global edge scan or relationship-property
support.

## 5. Incremental synchronization correctness

The compound cursor implementation uses timestamp plus type-aware primary-key
ordering. Numeric IDs use numeric comparison; UUIDs use lexical comparison.
Rows sharing timestamps are therefore not skipped. The one-hour lookback is
replay-safe and intentionally reprojects an overlap.

The live control plane confirms the high-watermark pairs exactly match the
source maxima. Replaying selected contractor ingestion twice produced no
collision or duplicate logical relationship error. The existing integration
failure injection also demonstrated graph-write success followed by checkpoint
failure and successful replay.

Important limits remain:

- A source update whose reliable timestamp does not advance is invisible to
  timestamp synchronization and requires reconciliation.
- There are no source tombstones. A missing row in a partial scan is never
  interpreted as deletion.
- Endpoint-changing updates currently add the new derived relationship but do
  not safely remove the old one. This affects contractor reassignment,
  participant tender changes, award evidence changes and co-bid membership.
  Detect reconciliation can expose the inconsistency, but `repair` currently
  reprojects and does not mass-delete stale edges.
- A participant update can affect all observations for its tender; incremental
  scanning regenerates pairs touching changed participants, but a full tender
  recomputation is still the safe periodic-reconciliation path for deletions or
  endpoint changes.

These limitations prevent claiming full update/deletion correctness or
transactional semantics. HydraDB has no multi-request transaction; recovery is
at-least-once and idempotent, not atomic.

## 6. Failure recovery

Validated or observed scenarios:

- A graph node write followed by an injected failure before checkpoint
  advancement: failed batch persisted, watermark did not advance, replay
  succeeded.
- The award run stopped after 15,000 rows; the later recovery run replayed the
  durable cursor range and reached the source high watermark.
- Alias verification failure remained in the batch history and was replayed
  after the verification query was corrected.
- Repeated selected contractor runs completed with the same deterministic
  identities and no logical duplication.
- Orphaned running checkpoints were retained and explicitly marked failed
  during reconnaissance; no source or graph cleanup was performed.

PostgreSQL disconnection and relationship-write failure were not injected into
live development data during this gate. The code retries a failed batch, keeps
the watermark behind the unverified cursor, and relies on deterministic replay;
it does not claim rollback of a partially completed HydraDB request.

## 7. Participant performance profile

The Phase 3 baseline was 22,865 participant rows in 1,498.11 seconds with
read-after-write verification disabled: approximately 15.3 rows/s. This is not
verified end-to-end throughput.

The source-side combinatorics are correct: a tender with `n` participants
produces `n(n-1)/2` unordered observations and two `CO_BID_IN` edges per
observation. The scanner was hardened so initial/selected scans generate each
pair once, owned by the lower participant ID; incremental/retry scans include
pairs touching either endpoint so changed participants are not missed.

The main cost remains HydraDB relationship mutation and read latency, not
PostgreSQL scanning or FNV identity generation. The full participant
relationship reconciliation took about 11 minutes before a subsequent Bid node
verification query timed out; it still found 156,402/156,402 endpoints.

## 8. Raw versus verified throughput

A controlled replay of all 3,257 contractors at batch size 1,000 was run twice
against the existing graph:

| Mode | Wall time | Rows/s | Batch observations |
|---|---:|---:|---|
| raw (`-verify=false`) | 8.23 s | 395.7 | scan 1–7 ms; writes ~0.6–2.3 s |
| verified (`-verify=true`) | 24.72 s | 131.8 | verification ~0.39–1.87 s per batch |

Peak RSS reported by `/usr/bin/time` was approximately 157 MiB in both runs.
These metrics are explicitly separated: raw throughput does not establish
read-after-write correctness. The updated engine logs scan, write, verify and
checkpoint milliseconds per batch.

## 9. Repair safety review

`-op repair` is explicit and dataset-scoped. It reprojects deterministic source
records and can use verification. It does not mass-delete stale graph data,
does not modify PostgreSQL source tables, and retains normal collision checks.
The disposable probe confirmed relationship deletion is technically supported,
but no repair path was allowed to delete existing development graph data.

The repair command is therefore safe but incomplete for endpoint-changing
updates and hard deletions. Those inconsistencies are reported for review
rather than destructively “fixed.”

## 10. Tests executed

Executed after the validation changes:

```text
go test ./...                         PASS
go vet ./...                          PASS
go test -tags integration ./... -count=1  PASS
```

The integration suite connected to live PostgreSQL/HydraDB and passed the
existing replay, collision, control-plane and failure-injection tests. Live
development-data checks are reported separately above; they are not substituted
with unit-test claims.

## 11. Known limitations and gate decision

The approved source-derived relationships are complete under bounded endpoint
verification, and all seven source watermarks are successful. The gate is not
a clean full-consistency pass because:

1. HydraDB node inventory queries for large `Bid` and `Award` labels time out.
2. Relationship properties/provenance cannot be read in the verified runtime.
3. Global stale-node/edge detection for deleted source rows is unavailable.
4. Endpoint-changing updates can leave obsolete relationships until a safe
   relationship cleanup/reconciliation mechanism is implemented.
5. Participant and award reconciliation commands correctly fail rather than
   claiming success after node verification timeout.
6. Reconciliation progress is not checkpointed per source batch; retry is
   restart-safe but not cursor-resumable.

The next hardening step should be a runtime-supported, bounded node/edge
inventory path (or an indexed graph-read capability), followed by safe,
provenance-scoped stale-edge cleanup. No Phase 4 work was started.

## 12. Files modified in this validation

- `api/internal/graph/hydrahttp.go` — larger verified HTTP read pages.
- `api/internal/projection/hydra.go` — paginated bounded edge verification,
  batched collision checks and bounded node/edge inventory helpers.
- `api/internal/ingest/reconcile.go` — bounded relationship reports, explicit
  runtime limitations and failure details.
- `api/internal/ingest/source.go` — pair-generation optimization distinguishing
  full scans from incremental/retry recomputation.
- `api/internal/ingest/engine.go` — pair-mode selection and structured timing
  metrics with a corrected scan timer.
- `docs/phase3/PHASE_3_VALIDATION_REPORT.md` — this report.

No files were removed. Existing Phase 3 changes in the worktree were preserved.
