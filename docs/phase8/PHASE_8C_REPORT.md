# TenderGraph Phase 8C Report

## Scope

Phase 8C focused on the investigation workspace: readable bounded graph exploration, source evidence inspection, safe participant identity handling, additive graph expansion, and a real-data investigation.

## Implemented

- Retained Cytoscape as the graph renderer selected in Phase 8B. It already supports bounded node/edge interaction and is integrated with the existing React application.
- Preserved the graph model Bid -> CoBidObservation <- Bid and evidence references returned by the Go service.
- Kept node and edge selection in the graph inspector, including GIDs, source references, observation/tender identifiers and the explicit non-collusion interpretation.
- Kept graph IDs string-safe in the frontend path and selection flow.
- Made graph expansion merge new nodes and edges into the current graph state, deduplicating by node and deterministic edge IDs.
- Added a first shadcn-style component foundation (Button, Badge, Card, cn) and configured Tailwind/PostCSS dependencies.
- Replaced the inline application SVG icon map with lucide-react icons.
- Replaced silent landing-page count dashes with Loading and Unavailable states when KPI data is unavailable.

## Evidence model

Bid records remain participant records, not legal companies. Observed participant names and keys remain separate from Vendor and CorporateEntity identity. Shared tender edges are tender-specific observations, not evidence of collusion.

The inspector exposes source references available from the API. Relationship evidence is enriched in the Go service from PostgreSQL because relationship-property read-back is not relied on for this workflow.

## Real-data validation

The development API was exercised against PostgreSQL and HydraDB. Bid 1760 is Morrisons Lifecare Pvt Ltd; Bid 1710 is 3M INDIA LIMITED. Their pair response reports:

- Bid 1760: 1 distinct tender.
- Bid 1710: 3 distinct tenders.
- Shared tenders: 1.
- Jaccard similarity: 0.3333333333.
- Coverage of Bid 1760: 1.0.
- Coverage of Bid 1710: 0.3333333333.
- Shared tender: internal ID 16813, external ID 2026_NIMHN_894353_2.
- Organization: National Institute of Mental Health and Neuro Sciences.
- Evidence: participant source PKs 1710 and 1760 plus tender source PK 16813.

The tender endpoint returned both participant records with financial values (18024891 and 2114700), financial rank 1, and is_winner=false. It returned no extracted Award record, so the UI does not present a winner or award conclusion.

## Verification executed

    cd api
    go test ./...                         PASS
    go vet ./...                          PASS
    go test -tags integration ./... -count=1 PASS

    cd frontend
    npm run build                         PASS

The integration run used the configured development PostgreSQL and HydraDB endpoints: HTTP 127.0.0.1:8443, Bolt 127.0.0.1:7687, and admin 127.0.0.1:19091.

## Known limitations

- Browser-level E2E execution and screenshot capture were not available in this run, so visual acceptance is not claimed complete.
- The graph remains Cytoscape-based rather than the D3 KnowledgeGraph interface mentioned in the request. Cytoscape was retained to avoid introducing a second renderer while the existing bounded graph interaction works.
- Current graph payloads expose Bid and CoBidObservation semantics; broader node types require corresponding API payloads.
- The production bundle still reports a large Cytoscape-containing chunk.
- The application remains a trusted development environment; operator-supplied analyst identity is not authentication.

## Files changed in this increment

- frontend/src/App.tsx
- frontend/src/styles.css
- frontend/package.json
- frontend/package-lock.json
- frontend/tailwind.config.ts
- frontend/postcss.config.js
- frontend/src/lib/utils.ts
- frontend/src/components/ui/button.tsx
- frontend/src/components/ui/badge.tsx
- frontend/src/components/ui/card.tsx
- api/internal/intel/service.go
- api/internal/intel/network_test.go

No PostgreSQL source records or HydraDB graph data were modified.
