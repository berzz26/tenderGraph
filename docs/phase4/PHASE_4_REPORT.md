# TenderGraph Phase 4 Report — Procurement Network Intelligence

**Date:** 2026-11-14  
**Status:** implemented analytical slice; live graph reads verified; write-backed integration gate is runtime-limited.

## 1. Implemented capabilities

The new `internal/intel` service and `graphintel` CLI provide:

- bidder participation profiles by source Bid ID;
- distinct tender and government-organization counts;
- first/most-recent observed participation dates;
- bounded participation history with rank, bid value, status and winner flag;
- direct co-bidder lookup through HydraDB;
- bounded two-hop/three-hop-style breadth-first network exploration;
- pairwise shared-tender history;
- Jaccard similarity and per-side overlap coverage;
- tender-level competition analysis and historical pair statistics;
- participation, organization-diversity and distinct-co-bidder rankings;
- organization-level tender summaries; and
- inclusive/exclusive time-window filtering for bidder profiles.

The service uses `participant_normalized` only as an observed participant key.
It never turns that key into a Vendor or CorporateEntity identity and never
infers collusion, fraud or wrongdoing.

## 2. CLI

From `api/`:

```bash
go run ./cmd/graphintel bidder 1 --limit 25
go run ./cmd/graphintel bidder 1 --from 2026-06-01T00:00:00Z --to 2026-08-01T00:00:00Z
go run ./cmd/graphintel co-bidders 3223 --limit 25
go run ./cmd/graphintel pair 1 5 --limit 25
go run ./cmd/graphintel network 3223 --depth 2 --limit 25
go run ./cmd/graphintel tender 16219 --limit 25
go run ./cmd/graphintel rankings --by participation --limit 25
go run ./cmd/graphintel rankings --by organizations --limit 25
go run ./cmd/graphintel rankings --by co-bidding --limit 25
go run ./cmd/graphintel organization 'DG Sashastra Seema Bal,MHA' --limit 25
```

All output is structured JSON. The positional IDs are PostgreSQL source IDs;
responses also include canonical graph GIDs.

## 3. Analytical definitions

The unit of participation is a distinct `tender_participants.tender_id`, not a
raw Bid row. A repeated source record for one tender therefore does not inflate
the tender count.

For two observed participant keys A and B:

```text
shared_tenders = |Tenders(A) ∩ Tenders(B)|
union_tenders  = |Tenders(A) ∪ Tenders(B)|
Jaccard        = shared_tenders / union_tenders
coverage(A)    = shared_tenders / |Tenders(A)|
coverage(B)    = shared_tenders / |Tenders(B)|
```

Empty denominators return zero. Results include the formula text and source
evidence. No arbitrary risk or anomaly score is produced.

Co-bidding observations are counted per tender. A tender with `n` participant
records contributes `n(n-1)/2` unordered observations. The graph traversal
uses `Bid -[:CO_BID_IN]-> CoBidObservation <-[:CO_BID_IN]- Bid`; it does not
use a direct Bid-to-Bid relationship.

## 4. Data sources and provenance

PostgreSQL supplies exact source-backed analytical facts:

- `tender_participants`: Bid IDs, participant names/normalized keys, dates,
  rank, bid value, technical/financial status and winner flag;
- `tenders`: Tender IDs, external tender IDs, organizations and tender dates;
- Phase 3's ingested graph projection: bounded co-bid traversal and canonical
  graph connectivity.

Responses preserve `source_table`, `source_pk`, and canonical GIDs for Bid and
Tender evidence. Pair histories include both participant source IDs and the
tender source ID. Dates and missing statuses are returned as available; no
missing source fact is manufactured.

The service intentionally does not link a Bid to Vendor or CorporateEntity.
The identity note in every bidder profile states that the grouping key is an
observed source normalization, not a confirmed legal identity.

## 5. Query architecture

PostgreSQL performs exact distinct-set calculations, grouped rankings, source
joins and evidence retrieval. HydraDB performs bounded structural traversal for
interactive direct co-bidder and network queries. Go owns result shaping,
identity/GID formatting, formulas, limits, ambiguity handling and BFS visited
state.

The verified runtime's native `algo.degree` procedure was tested against the
populated Bid graph with `CO_BID_IN`, `minDegree: 1`, and `limit: 5`; it did not
return within the 30-second query budget. The implementation therefore uses a
bounded, sequential BFS over direct HydraDB queries. It caps depth at three and
requires an explicit result limit. Unrestricted path expansion and unmeasured
community detection are not implemented.

## 6. Real investigations

These examples were executed against the live development data before the
runtime entered read-only/recovery state:

### Bidder profile: source Bid 1

The observed key `amar enterprises` covered four distinct tenders and one
government organization, with two co-bid tenders. The response returned source
Bid IDs 1, 7, 1510 and 3223, their dates, ranks, values, statuses and GIDs.

### Pair: source Bids 1 and 5

The observed keys were `amar enterprises` and `mohammad umar contractor`.
They shared two tenders; the respective participation totals were four and
three tenders:

```text
Jaccard       = 2 / (4 + 3 - 2) = 0.4
coverage(A)   = 2 / 4 = 0.5
coverage(B)   = 2 / 3 = 0.666666...
```

Both shared Tender GIDs and both source participant IDs per tender were
returned.

### Tender 16219

The tender contained five participant records and ten unordered co-bid
observations. Historical pair results were returned for the ten pairings,
including their shared-tender counts and overlap formulas.

### HydraDB network: source Bid 3223

The direct graph lookup returned source Bid IDs 3222, 3224 and 3225 with
observed keys `aman upadhyay`, `mohammad umar contractor` and `neer solutions`.
The result came from the graph's CoBidObservation traversal and was enriched
from `tender_participants` without identity merging.

## 7. Performance measurements

Measurements used live development data, sequential CLI processes, result
limit 10 and the existing HydraDB HTTP client. Wall times include Go process
startup, so they are operational CLI measurements rather than isolated
function latency. Five samples were collected per operation; p99 is not
reported because five samples do not support a meaningful p99.

| Operation | Samples (seconds) | Empirical p50 | Empirical p95 |
|---|---|---:|---:|
| bidder profile, Bid 1 | 9.12, 0.37, 0.50, 0.33, 0.33 | 0.37 | 9.12 |
| pair, Bids 1/5 | 0.75, 0.97, 0.59, 0.48, 0.66 | 0.66 | 0.97 |
| direct co-bidders, Bid 3223 | 0.48, 0.38, 0.41, 0.37, 8.15 | 0.41 | 8.15 |
| network, Bid 3223 depth 2 | 2.93, 1.74, 0.51, 0.67, 0.65 | 0.67 | 2.93 |
| tender 16219 | 0.49, 0.36, 0.48, 0.39, 0.39 | 0.39 | 0.49 |

Five concurrent direct co-bid CLI reads completed in 6.74 seconds with
approximately 158 MiB peak process RSS. The measurements include process
startup and are not a production capacity claim. The sequential BFS is chosen
for correctness and HydraDB writer/read-runtime safety; concurrency should be
revisited with a larger in-process benchmark.

## 8. Tests executed

Passed:

```text
go test ./...
go vet ./...
go test ./internal/intel ./cmd/graphintel
go test -tags integration ./internal/intel -count=1 -v
```

The new unit tests cover Jaccard, coverage, empty denominators and inclusive
lower/exclusive upper time windows. The live CLI examples above exercised
PostgreSQL source joins and HydraDB traversal against real records.

The targeted Phase 4 integration test passed against the live PostgreSQL and
HydraDB read path, covering a real bidder profile, pair calculation, tender
analysis and bounded network traversal.

The full tagged integration command was also executed with the runtime's
actual admin address, `HYDRADB_ADMIN_ADDR=http://127.0.0.1:19091`. Health and
read checks passed, but the existing graph/ingestion integration suites failed
because the HydraDB process was in read-only/recovery state: graph reads
worked, while write probes returned `500 internal query execution error`. This
is reported as an environment/runtime limitation, not a Phase 4 test pass.

## 9. Known limitations

- `participant_normalized` is an observed grouping key, not a legal identity.
- Empty normalized participant names are not grouped into a bidder profile for
  pair analysis; the selected source Bid remains individually addressable.
- Pair history currently supports all-time windows; profile history supports
  `--from`/`--to` windows.
- Direct graph traversal returns Bid records, not canonical companies.
- HydraDB native degree timed out on this populated graph, so no native global
  degree ranking or community detection is claimed.
- Network BFS is bounded and may omit paths after the result limit. It is not a
  full graph export or community algorithm.
- Historical results inherit Phase 3 source limitations, including no automatic
  identity confirmation and no relationship-property read-back.
- Performance samples are small and include CLI startup; they should not be
  used as production SLOs.

## 10. Files

Created:

- `api/internal/intel/formulas.go`
- `api/internal/intel/formulas_test.go`
- `api/internal/intel/service.go`
- `api/internal/intel/integration_test.go`
- `api/cmd/graphintel/main.go`
- `docs/phase4/PHASE_4_REPORT.md`

Modified:

- `api/internal/graph/config.go`
- `api/internal/graph/config_test.go`
- `api/internal/graph/health.go`
- `api/internal/graph/README.md`

These changes align the default HydraDB admin endpoint with the live runtime's
`127.0.0.1:19091` port. No PostgreSQL source data, HydraDB graph data,
frontend code or Phase 3 synchronization code was changed.

## 11. Ontology deviations

No ontology deviations were introduced. The implementation works with Bid,
Tender and CoBidObservation exactly as defined in Phase 2. It intentionally
does not add a canonical Bidder node, Vendor identity edge, risk score or
collusion classification.
