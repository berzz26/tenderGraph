# TenderGraph Phase 5 Report — Contract-to-Capital Intelligence

**Date:** 2026-11-14  
**Status:** PostgreSQL-backed intelligence implemented and validated; new HydraDB writes blocked by the current runtime state.

## 1. Scope and actual data inspected

Phase 5 was implemented as an analytical layer over the approved PostgreSQL
sources. Source records were read only. The following live counts were checked:

| Source | Current availability |
|---|---:|
| `entities` | 5,973 records observed in Phase 3 audit |
| `entity_aliases` | 6,889 records observed in Phase 3 audit |
| `entity_gst` | 15,357 non-empty GSTIN rows; 733 GSTINs map to multiple entities |
| `tenders` | 26,125 records |
| `tender_participants` | 22,865 records |
| `tender_extracted_meta` | 20,329 award-evidence rows in the approved projection |
| `contractor_v2` | 3,257 records; 45 GSTINs intersect entity GST registrations |
| `announcements_raw` | 148 records; 106 have an explicit `entity_id`, 42 do not |
| `announcement_documents` | 139 records; 50 have extracted PDF text |
| `entity_financials` | 62,585 records across 5,880 entities |
| `entity_market_data` | 41,976 snapshots across 5,973 entities |
| `market_daily_prices` | empty |
| `company_events` | empty |
| `tender_announcement_matches` | empty |

The previously reported data limitations remain materially present. Only 20
announcement rows have an ISIN, announcement mappings are not complete, and
market data is snapshot data rather than a historical price series.

## 2. Implemented capabilities

The existing `internal/intel` service and `graphintel` CLI now provide:

- company reports combining entity identity, candidate vendor links,
  contractor-on-record exposure, financial snapshots, latest market snapshot
  and explicitly mapped announcements;
- bounded procurement exposure by candidate Vendor identity;
- exact award-value parsing without a `float64` conversion;
- candidate identity classification for unique GSTIN, ambiguous GSTIN and
  normalized-name evidence;
- award inspection with tender, award and participant evidence;
- explicit missing-data and interpretation warnings in JSON output;
- deterministic source GIDs for Entity, Vendor, Tender, Award, Announcement
  and Bid evidence.

The implementation does not automatically confirm a corporate identity and does
not convert a contractor assignment into proof of bid participation, winning,
revenue or cash receipt.

## 3. Identity-linkage rules

`LinkCandidates` returns `status: CANDIDATE` for every match. It emits:

| Method | Meaning | Automatic confirmation |
|---|---|---|
| `GSTIN_EXACT_UNIQUE` | Vendor GSTIN maps to exactly one entity GST registration | No |
| `GSTIN_EXACT_AMBIGUOUS` | GSTIN matches, but maps to multiple entities | No |
| `NAME_NORMALIZED_EXACT` | Conservative normalized-name equality | No |

Each result includes the supporting source table, source key and GID. Vendor
and CorporateEntity nodes are not merged. Participant names are not promoted to
legal identities. The empty `tender_announcement_matches` table means there are
currently no analyst-confirmed procurement/announcement associations to expose.

## 4. Financial calculations and units

Procurement award values are retained as exact rupee strings. Values with
grouping separators are normalized, while malformed values are marked invalid
and excluded from totals. Totals use rational arithmetic and are formatted to
two decimal places; no binary floating-point conversion is used.

`entity_financials` values are reported in **INR crore**, with fiscal year,
source and source date. `entity_market_data.market_cap` is reported in **INR
crore** and `current_price` in **INR/share**, with snapshot date. No award value
is labelled revenue, and no conversion is made without displaying units.

Historical returns, event studies and announcement-driven market movements are
not implemented because `market_daily_prices` and `company_events` are empty.

## 5. Provenance model

Responses preserve source table and source key evidence objects. Examples:

- procurement exposure: `tenders` plus `tender_extracted_meta` evidence;
- candidate identity: `entity_gst` evidence and the entity GID;
- announcements: `announcements_raw` source key and `ANN:v:<id>` GID;
- award inspection: `tender_participants` evidence for bid-number or awardee
  name winner evidence when present;
- financial and market observations: source entity, fiscal/snapshot date,
  source and explicit units.

## 6. CLI examples using live records

From `api/`:

```bash
graphintel company 923567ef-718c-44b5-93c6-b584cbe01df3 --limit 2
graphintel exposure 923567ef-718c-44b5-93c6-b584cbe01df3 --limit 5
graphintel announcements 19babccf-613d-41bd-b933-3544744bbb76 --limit 2
graphintel link-candidates 923567ef-718c-44b5-93c6-b584cbe01df3 --limit 5
graphintel award 1
```

Representative live results:

- `AAA Technologies Ltd` (`ENTITY:v:923567ef-718c-44b5-93c6-b584cbe01df3`)
  produced one candidate Vendor, `VENDOR:cp:1429`, through
  `GSTIN_EXACT_AMBIGUOUS`; that GSTIN maps to two entities.
- The candidate reached one contractor-on-record tender, `TENDER:v:9202`,
  with one evidenced award, `AWARD:v:9202`, and exact award value
  `28762500` INR. This is explicitly exposed as contractor-on-record evidence,
  not confirmed corporate revenue or a confirmed winner-to-entity identity.
- `Bagmane Prime Office REIT` had explicitly entity-mapped NSE announcements;
  each returned `ANN:v:<id>`, source URL, announcement time and document count.
- Award `1` returned tender `1`, organization `Tata Memorial Centre`, awardee
  `BIOAPPS`, raw value `2,041,781.94` and exact normalized value
  `2041781.94` INR.

## 7. Graph ontology and HydraDB status

No new graph relationship was added in this phase. Phase 0 already defines the
broader FinancialStatement, MarketObservation, CorporateAnnouncement and
CorporateEvent ontology, but the current HydraDB process was checked before any
mutation attempt:

- graph health `127.0.0.1:8443/healthz`: HTTP 200;
- admin readiness `127.0.0.1:19091/readyz`: HTTP 200;
- read queries and bounded Phase 4 traversals: operational;
- mutation probes: HTTP 500 `internal query execution error`.

Therefore this phase does not claim that new financial, market or announcement
nodes/edges were persisted. PostgreSQL-backed analysis and deterministic GID
construction are implemented and tested. Graph projection should be added only
after the runtime accepts mutations, using the existing bounded, replay-safe
projection conventions.

## 8. Performance measurements

Measurements used a built `graphintel` binary, live development PostgreSQL,
HydraDB configured with admin port 19091, result limits of 5 and three samples
per command. Wall time includes process startup; RSS is the process maximum.

| Command | Samples | Approx. median | RSS range |
|---|---|---:|---:|
| `company` | 0.04, 0.04, 0.04 s | 0.04 s | 11,264–11,264 KiB |
| `exposure` | 0.03, 0.03, 0.03 s | 0.03 s | 11,264–11,520 KiB |
| `announcements` | 0.01, 0.01, 0.01 s | 0.01 s | 11,136–11,264 KiB |
| `award` | 0.01, 0.01, 0.01 s | 0.01 s | 11,136–11,264 KiB |

These are small-sample operational measurements, not capacity or p99 claims.
No cache was introduced.

## 9. Tests executed

Passed:

```text
go test ./...
go vet ./...
go test -tags integration ./internal/intel -count=1 -v
```

The targeted integration suite passed both existing graph-read investigations
and the new live PostgreSQL corporate-intelligence test. Unit coverage includes
exact decimal parsing, grouped rupee values, invalid monetary strings, exact
totals and deterministic conservative name normalization.

The requested full tagged command was also run:

```text
go test -tags integration ./... -count=1
```

The Phase 5 package passed, but the overall command failed in existing
`internal/graph` and `internal/ingest` mutation tests because HydraDB returned
HTTP 500 for write probes. This is an operational runtime limitation, not a
claimed successful graph-write test.

## 10. Files created or modified

Created:

- `api/internal/intel/corporate.go`
- `api/internal/intel/corporate_test.go`
- `docs/phase5/PHASE_5_REPORT.md`

Modified:

- `api/cmd/graphintel/main.go` — added `company`, `exposure`,
  `announcements`, `link-candidates` and `award` commands.
- `api/internal/intel/integration_test.go` — added live PostgreSQL corporate
  intelligence coverage.

The existing Phase 4 and HydraDB admin-port changes remain in the worktree and
were preserved. No PostgreSQL source tables or records were modified.

## 11. Known limitations and unfinished work

- HydraDB mutation recovery blocks live persistence of any new Phase 5 graph
  projection; no write success is claimed.
- `tender_announcement_matches` and `company_events` are empty, so candidate
  procurement-announcement matching and event intelligence are not populated.
- 42 announcements lack an explicit entity mapping.
- GSTIN and name matches remain candidate-only; ambiguous GSTINs are exposed,
  not resolved.
- Contractor-on-record exposure is not an award/winner/legal-entity fact.
- Awardee-name candidate evidence is returned where available, but no company
  identity is automatically established.
- Market snapshots cannot support historical returns or causality analysis.
- Exposure is bounded by the requested result limit; it is not a full company
  backfill or synchronization job.

No Phase 6 work was started.
