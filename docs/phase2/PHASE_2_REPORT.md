# TenderGraph Phase 2 Report — Graph Ontology & Projection Foundation

**Date:** 2026-11-14  
**Status:** complete  
**Scope:** deterministic projection primitives plus one isolated real-data vertical slice; no full backfill or synchronization worker.

## 1. Reconnaissance summary

The repository was clean before implementation. Phase 1's 'api/internal/graph'
package is the single HydraDB integration used by Phase 2. PostgreSQL is
PostgreSQL 16.14, and the development HydraDB instance is build
'0.1.0+916637477f3f' at Bolt '127.0.0.1:7687', HTTP '127.0.0.1:8443', admin
'127.0.0.1:19090'.

The existing Vyuha code remains useful for:

- 'internal/db.Connect' and the existing 'pgxpool' conventions.
- 'internal/sqlf' normalization and money SQL semantics.
- 'internal/repo/mapping.go' and 'awards.go' as dashboard mapping and validation
  references.
- 'vw_vyuha_l1_intelligence' as a validation/reference projection.

No legacy endpoint or frontend was rebuilt or removed. Phase 2 does not require
the dashboard.

## 2. Finalized Phase 2 ontology

### Nodes

| Label | Business meaning | Canonical GID | Source |
|---|---|---|---|
| 'Tender' | Government procurement opportunity | 'TENDER:v:<tenders.id>' | 'tenders.id' |
| 'GovernmentOrganization' | Normalized issuing-organization label, not independently verified legal identity | 'GOVORG:org:<normalized organization>' | 'tenders.organization' |
| 'Bid' | One actual participant/bid record | 'BID:v:<tender_participants.id>' | 'tender_participants.id' |
| 'Vendor' | Procurement contractor registry identity | 'VENDOR:cp:<contractor_v2.id>' | 'contractor_v2.id' |
| 'Award' | Extracted award record | 'AWARD:v:<tender_extracted_meta.id>' | 'tender_extracted_meta.id' |
| 'CoBidObservation' | One unordered pair of bids appearing in one tender | 'COBID:v:<tender>:<min participant>:<max participant>' | Derived from 'tender_participants' |

'CorporateEntity', 'Alias', and 'GstRegistration' remain in the broader
ontology but are not projected by the Phase 2 vertical slice. No participant
name is converted into a Vendor, and Vendor is never merged with
CorporateEntity.

### Relationships

| Type | Direction | Evidence/meaning |
|---|---|---|
| 'ISSUED_BY' | Tender → GovernmentOrganization | Normalized organization recorded by the tender |
| 'SUBMITTED_BID' | Bid → Tender | Actual 'tender_participants.tender_id' participation |
| 'CONTRACTOR_ON_RECORD' | Vendor → Tender | 'tenders.contractor_v2_id' assignment only |
| 'HAS_AWARD' | Tender → Award | Structural extracted-award relationship |
| 'WON' | Bid → Award | Positive bid-number or awardee-name evidence |
| 'CO_BID_IN' | Bid → CoBidObservation | Membership in one shared-tender observation |
| 'IDENTIFIED_AS' | Vendor or Bid → CorporateEntity | Evidence-backed candidate edge; Phase 2 constructor always uses 'CANDIDATE' |

'WON' is not emitted from 'is_winner' alone. The winner evidence priority is
bid-number match, then normalized awardee-name match. 'CONTRACTOR_ON_RECORD'
never becomes participation or winner evidence. Co-bidding is an observation,
not a collusion conclusion.

## 3. Source mapping

| Source | Phase 2 fields | Null/normalization policy | Graph result |
|---|---|---|---|
| 'tenders' | 'id', 'tender_id', 'organization', 'website_url', 'lifecycle_stage', timestamps, 'contractor_v2_id' | Empty strings omitted from optional properties; organization normalized with the repository convention | 'Tender', 'GovernmentOrganization', 'ISSUED_BY', optional 'CONTRACTOR_ON_RECORD' |
| 'tender_participants' | 'id', 'tender_id', bid number/name, stored normalized name, ranks/statuses, bid value, winner flag, timestamps | Stored normalized value wins; missing optional values omitted; money parsed read-only | 'Bid', 'SUBMITTED_BID', derived co-bid observations |
| 'contractor_v2' | 'id', 'business_name', 'gst' | Only a real tender FK loads a Vendor; no participant-name Vendor is created | 'Vendor' and 'CONTRACTOR_ON_RECORD' |
| 'tender_extracted_meta' | 'id', 'tender_id', awardee fields, awarded value, winning bid number, contract date, award metadata | Award node requires awardee or winning-bid evidence; outcome classification is copied only as validation context | 'Award', 'HAS_AWARD', conditional 'WON' |
| 'vw_vyuha_l1_intelligence' | 'winner_evidence', 'outcome_classification' | Reference/validation only; never used as a replacement source | Winner evidence and award-stage validation |
| 'entities', 'entity_aliases', 'entity_gst' | Reviewed for identity policy and source mapping | No ambiguous identity link is generated in the selected slice | Candidate primitive only; full R1–R5 pipeline is out of scope |

All SQL in 'internal/projection/source.go' is parameterized and read-only.

## 4. Deterministic identity

Every node contains 'gid', numeric 'id', and 'id_algo_v=1'.

The implemented algorithm is 'fnv1a64-mask63':

1. Hash the UTF-8 bytes of the complete GID using FNV-1a 64-bit.
2. Mask with '0x7fffffffffffffff'.
3. Store the result as a non-negative signed 'int64'.

Fixed vectors are tested for the empty string, Tender, Bid, CoBidObservation and
GovernmentOrganization GIDs. The algorithm version is a code constant and must
be incremented before any future algorithm change.

Before writes, the HydraDB writer reads 'n.gid' for each outgoing numeric ID.
A different existing GID returns an explicit 'CollisionError'; it never
overwrites the node. Duplicate IDs within an outgoing batch are checked too.

Relationship IDs are deterministic hashes of relationship type, endpoint GIDs
and source key. They are used with batch relationship 'MERGE'; no general
relationship constraint is assumed.

## 5. Provenance and properties

Every node carries:

'id', 'gid', 'id_algo_v', 'src_table', 'src_pk', 'synced_at_ms'.

Every edge carries:

'id', 'src_table', 'src_pk', 'synced_at_ms', plus source join keys or derivation
properties where applicable.

Identity candidates additionally carry 'status', 'method',
'evidence_gids' as a JSON string, and 'evidence_count'. No confidence score is
invented.

Money is preserved as '<field>_exact' plus a float analytics copy and an
explicit '_unit'. Procurement values in this slice are INR rupees. Invalid or
empty money values are omitted rather than fabricated.

## 6. Real-data vertical slice

Selected source record:

- 'tenders.id = 16234'
- external tender ID: '2026_NHIDC_909174_1'
- organization: 'National Highways and Infrastructure Development Corporation'
- contractor-of-record: 'contractor_v2.id = 1113', 'SUN SHINE OVERSEAS PVT LTD'
- four participant rows: IDs '79', '80', '81', '82'
- extracted award: 'tender_extracted_meta.id = 16234'
- winning bid number: '3421499'
- winner evidence: 'BID_NUMBER_MATCH'
- view outcome classification: 'L1_SELECTED_AOC_PENDING'

The resulting graph contains:

~~~text
Tender
 ├─ ISSUED_BY → GovernmentOrganization
 ├─ HAS_AWARD → Award
 ├─ CONTRACTOR_ON_RECORD ← Vendor
 └─ ← SUBMITTED_BID ← Bid (4)
                    └─ WON → Award (only bid 81)

Bid (pairs) ── CO_BID_IN → CoBidObservation (6)
~~~

The four participant rows produce six unordered pair observations and twelve
'CO_BID_IN' edges. No Vendor-to-Bid or identity edge was forced: the selected
participant rows did not establish a supported CorporateEntity mapping.

## 7. HydraDB behavior and queries

The writer uses the existing Phase 1 HTTP client and verified mutation shapes:

~~~cypher
UNWIND $rows AS row
MERGE (n {id: row.id})
SET n:Label, n.gid = row.gid, ...
~~~

~~~cypher
UNWIND $rows AS row
MATCH (a:FromLabel {id: row.src}), (b:ToLabel {id: row.dst})
MERGE (a)-[r:RELATIONSHIP {id: row.rid}]->(b)
SET r.src_table = row.src_table, ...
~~~

Verification uses label/id-anchored 'MATCH ... RETURN' queries. Cleanup uses
parameter-driven 'UNWIND ... DETACH DELETE' followed by per-ID count checks.

One runtime discrepancy was found: separate requests using batched 'CREATE'
duplicated the same relationship. A live probe against the running build showed
that batch relationship 'MERGE' with deterministic 'id' produced one edge across
separate requests. The compatibility document was updated and the writer uses
the verified 'MERGE' form.

## 8. Tests executed

Unit tests:

- GID and fixed hash vectors.
- Numeric ID range and version.
- Name normalization and money precision.
- Winner evidence rules, including rejection of winner-flag-only evidence.
- CoBidObservation determinism and pair count.
- Source-to-node conversion and provenance completeness.
- Identity candidate evidence/status policy.
- Collision detection.

Commands and results:

~~~text
go test ./...                         PASS
go vet ./...                          PASS
go test -tags integration ./...       PASS
~~~

The live integration test:

- reads the real PostgreSQL tender;
- writes nodes and relationships to the clean development graph;
- verifies node GIDs and provenance;
- verifies co-bid traversal and 'WON.evidence_kind';
- replays the complete batch through separate mutation requests;
- verifies no duplicate co-bid relationships;
- exercises a live numeric-ID collision;
- deletes only its own nodes and verifies cleanup.

The command 'go run ./cmd/graphproject -tender-id 16234 -write -cleanup'
was also executed successfully, reporting 14 nodes, 20 relationships,
'written=true', and 'cleaned=true'.

## 9. Remaining design questions

- Full R1–R5 candidate generation and PostgreSQL analyst decision tables.
- Whether identity candidates should be represented as graph edges only, control
  nodes plus edges, or both once 'tg_*' tables are approved.
- Source-deletion policy after broader runtime verification.
- Full-table watermark and reconciliation implementation.
- Pagination/NDJSON for larger projection reads.
- Production HydraDB TLS, object-store and operational deployment.
- Whether additional core node properties need a formal schema registry.

These are intentionally not implemented in Phase 2.

## 10. Files

Created:

- 'api/internal/projection/projection.go'
- 'api/internal/projection/hydra.go'
- 'api/internal/projection/source.go'
- 'api/internal/projection/projection_test.go'
- 'api/internal/projection/integration_test.go'
- 'api/cmd/graphproject/main.go'
- 'docs/phase2/PHASE_2_REPORT.md'

Modified:

- 'docs/phase0/HYDRADB_COMPATIBILITY.md' — records the verified cross-request
  relationship-MERGE behavior.

Removed: none.

## 11. Phase 0 deviations

The Phase 0 relationship-idempotency uncertainty is now narrowed by a live
Phase 2 probe: deterministic relationship 'MERGE' by 'id' works across separate
requests in the verified development build. The implementation still does not
claim a global relationship uniqueness constraint, and it retains observation
nodes for repeated facts.

No PostgreSQL source table, source record, role, migration or existing graph
data was modified outside the disposable vertical slice, which was fully
cleaned and verified.
