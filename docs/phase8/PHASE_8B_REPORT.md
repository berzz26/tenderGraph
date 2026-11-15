# TenderGraph Phase 8B Report

## Scope and gap analysis

Phase 8B completed the missing graph-investigation slice without replacing the existing Go intelligence services or graph runtime.

| Issue | Existing behaviour | Correction | Validation |
|---|---|---|---|
| Network UI | Bounded nodes rendered as cards; no edge model | Cytoscape canvas with typed nodes, edges and inspection panel | Production build and live Bid `1760` API response |
| Network API | Returned non-root Bid nodes only | Returns root Bid, bounded Bid nodes, CoBidObservation nodes, CO_BID_IN edges, truncation metadata and evidence | `go test`, live API |
| Participant labels | Empty names fell through to unnamed/undefined presentation | Explicit `Participant record #<id>` fallback and source-identity note | Live profile/network response; UI fallback paths |
| Relationship evidence | No frontend relationship detail | Edge DTO includes observation GID, tender GID and source references | Live response contains `tender_participants` and `tenders` evidence |
| Pair investigation | Backend existed but no product surface | Bidder A/B selectors and shared-tender comparison | Existing Go pair implementation and build validation |
| Large identifiers | New graph UI could rely on numeric IDs | `node_id` is a string and is used for graph element identity/navigation | API response and frontend type model |

## Graph visualization choice

Cytoscape.js was selected because the workspace needs a bounded graph canvas, typed node/edge styling, pan/zoom, stable event handling, selection and layout without introducing a server-side graph export. The graph is intentionally capped by the existing Go `depth` and `limit` parameters; the frontend never submits Cypher or requests the whole graph.

The frontend dependency is:

```text
cytoscape
@types/cytoscape
```

The current layout uses Cytoscape's bounded `cose` layout. Fit, zoom, reset, node-type filters and selected-node/edge detail are implemented. A future iteration can add a persisted layout or a more specialized layout for very dense neighborhoods.

## Graph data model

The backend network response now preserves the actual procurement graph shape:

```text
Bid (source participant record)
  | CO_BID_IN
CoBidObservation (one tender + participant pair observation)
  | CO_BID_IN
Bid (source participant record)
```

`NetworkNode` now includes:

- `node_id` string for client identity and navigation;
- `gid` and `node_type`;
- source table and source PK;
- observed participant key when present;
- tender identifiers for source context;
- explicit participant fallback naming.

`NetworkEdge` includes:

- deterministic analytical edge identity;
- source and target graph-node IDs;
- `CO_BID_IN` type;
- observation GID;
- tender GID and tender ID;
- source evidence references for both participant rows and the tender row.

The edge is rendered as an actual observation edge. The UI explicitly explains that it is not a direct legal-company relationship and not evidence of collusion.

## Backend changes

`api/internal/intel/service.go` was extended so `Network`:

1. Loads and emits the root Bid.
2. Performs the existing bounded HydraDB breadth-first traversal.
3. Hydrates participant names, observed keys and tender IDs from PostgreSQL.
4. Creates tender-specific CoBidObservation nodes from source participant joins.
5. Returns two CO_BID_IN edges for each Bid pair and observation.
6. Returns `truncated` when the bounded Bid-node limit is reached with remaining frontier work.

The existing `/api/v1/bidders/{bidID}/network` endpoint remains the route. No raw Cypher is accepted from the browser.

The existing `/api/v1/networks/pair` endpoint is now used by the frontend. Pair calculations remain in Go/PostgreSQL.

## Participant identity correction

The source/API flow was traced. `loadBid` already reads `participant_name` and `participant_normalized` from `tender_participants`; the network traversal, however, initially emitted only graph-derived IDs and then performed best-effort hydration. The frontend additionally used generic fallbacks and displayed the graph GID without guarding missing values.

The correction is deliberately not an identity inference:

- source name present: display the source participant name;
- source name absent: display `Participant record #<source id>`;
- normalization present: display it as an observed key;
- normalization absent: display `Observed key unavailable`;
- no legal entity is inferred from either value.

The root heading now falls back to `BID:v:<id>` and `Participant record #<id>` rather than `UNDEFINED`.

Graph node navigation uses string `node_id` values, preserving the identifier representation in the frontend even though the legacy DTO also contains an integer `bid_id` for compatibility.

## Interactive investigation workflow

The bidder page now provides:

- actual graph canvas as the primary relationship workspace;
- zoom in/out, fit and reset layout controls;
- Bid and CoBidObservation filters;
- bounded-result and truncation context;
- selected node detail with GID, source provenance and observed key;
- selected edge detail with relationship type, observation GID, tender and evidence references;
- explicit expand request for a selected Bid;
- navigation from Bid nodes to bidder investigations;
- navigation from observation nodes/edges to tender investigation;
- participation history table below the graph.

Pair comparison provides:

- Bidder A/B selectors from the returned Bid records;
- shared tender count;
- Jaccard similarity;
- coverage for each side;
- shared tender history and source-reference counts;
- direct navigation to each shared tender.

The interface labels these as descriptive overlap statistics. It does not expose a collusion or fraud score.

## Real-data validation

The live development graph was queried through the existing `graphintel` service and API configuration.

### Bid `1760`

The bounded network response returned:

- root Bid `BID:v:1760`;
- real source participant name `Morrisons Lifecare Pvt Ltd`;
- depth-one Bid records including `BID:v:1710`, `BID:v:1711`, `BID:v:1712` and `BID:v:1713`;
- CoBidObservation GIDs such as `COBID:v:16813:1713:1760`;
- source tender `TENDER:v:16813`;
- external tender `2026_NIMHN_894353_2`;
- source evidence references from `tender_participants` and `tenders`;
- bounded truncation when the limit was set to five Bid records.

### Bid `1`

The network returned the root Bid and no edges. This is preserved as a valid empty graph state: PostgreSQL profile data reports co-bid history for the observed participant, but the current graph projection does not contain a traversable CO_BID_IN neighborhood for that specific Bid record. The UI reports the bounded graph state rather than manufacturing edges from the profile aggregate.

## Tests and checks

Passed:

```text
cd api && go test ./...
cd api && go vet ./...
cd api && go test -tags integration ./... -count=1
cd frontend && npm run build
```

The live `graphintel network 1760 --depth 2 --limit 5` check returned the typed graph payload and source evidence described above. The live API route was also exercised for bidder profile, network and pair comparison.

No source PostgreSQL records were modified. No HydraDB writes or graph resets were performed.

Frontend build note: Cytoscape increases the production bundle to approximately 624 kB before gzip. This is acceptable for the first graph slice but should be code-split with a lazy-loaded graph route before production deployment.

## Files changed

Created:

- `frontend/src/components/GraphExplorer.tsx`
- `docs/phase8/PHASE_8B_REPORT.md`

Modified:

- `api/internal/intel/service.go`
- `frontend/src/App.tsx`
- `frontend/src/styles.css`
- `frontend/package.json`
- `frontend/package-lock.json`

## Known limitations

1. There is no browser automation framework in the repository yet, so Playwright E2E tests and screenshots were not added in this increment.
2. The current graph supports Bid and CoBidObservation nodes because those are the network records returned by the existing bounded service. Company, tender and corporate expansion remain route-level investigations rather than one cross-domain canvas.
3. “Expand this bidder” reissues a bounded network request rooted at the selected Bid; a future graph session model can merge expansions into the current canvas instead of replacing it.
4. The existing HydraDB runtime limitation around relationship-property readback remains. Equivalent source evidence is supplied from PostgreSQL for the displayed CO_BID_IN observations.
5. The production bundle should lazy-load Cytoscape and add browser E2E coverage before the final Phase 8 acceptance gate.

## Remaining work before full Phase 8 acceptance

- Add Playwright browser tests for search → bidder → graph → edge → tender → participant → pair.
- Capture actual screenshots from the running application.
- Add merged graph-session expansion and a richer edge evidence drawer.
- Add company candidate decision-state/history panels to the company page.
- Add explicit unavailable-data explanations and freshness to the landing surface.
- Code-split the graph library.
