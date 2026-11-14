# TenderGraph

TenderGraph is a PostgreSQL-to-HydraDB procurement intelligence projection.
PostgreSQL remains the authoritative source; HydraDB stores a deterministic,
rebuildable graph projection for procurement structure, provenance and bounded
network queries.

The repository contains:

- A Go 1.22 API in 'api/' using pgx/v5 and the standard HTTP library.
- A React/Vite dashboard in 'frontend/' for the existing Vyuha views.
- HydraDB integration in 'api/internal/graph/'.
- Projection primitives in 'api/internal/projection/'.
- The bounded synchronization engine in 'api/internal/ingest/'.
- Operational CLIs in 'api/cmd/graphproject/' and 'api/cmd/graphsync/'.

The dashboard is not required to run ingestion and is not rebuilt in the
current ingestion phase.

## Architecture

~~~text
PostgreSQL source tables
        |
        | read-only, keyset-paginated scans
        v
TenderGraph projection primitives
        |
        | deterministic node/relationship MERGE batches
        v
HydraDB projection
        |
        +--> graph reads and reconciliation

PostgreSQL tg_* control tables
        ^
        |
watermarks, runs, batches and reconciliation records
~~~

The pipeline is sequential per HydraDB cell. It uses no Kafka, RabbitMQ, Redis,
second graph database or separate vector database. PostgreSQL pgvector remains
the semantic-similarity system.

“Full ingestion” means complete projection of the approved graph ontology and
validated source mappings below, not a copy of every PostgreSQL table.

## Graph coverage

The approved ingestion registry currently contains:

| Dataset | Graph projection | Cursor |
|---|---|---|
| 'contractor_v2' | Vendor nodes | 'updated_at, id' |
| 'entities' | CorporateEntity nodes | 'updated_at, id' |
| 'entity_aliases' | Alias nodes and HAS_ALIAS edges | 'created_at, id' |
| 'entity_gst' | GstRegistration nodes and HAS_GST_REGISTRATION edges | 'updated_at, id' |
| 'tenders' | Tender, GovernmentOrganization, ISSUED_BY, CONTRACTOR_ON_RECORD | 'updated_at, id' |
| 'tender_participants' | Bid, SUBMITTED_BID and CoBidObservation graph | 'updated_at, id' |
| 'tender_extracted_meta' | Award, HAS_AWARD and evidence-backed WON edges | 'updated_at, id' |

Important semantics:

- 'CONTRACTOR_ON_RECORD' is not bid participation.
- Actual participation comes from 'tender_participants'.
- 'is_winner' alone never creates 'WON'.
- Co-bidding is represented by per-tender 'CoBidObservation' nodes.
- GSTIN and names create candidate evidence only; no identity is confirmed
  automatically.
- Every node and relationship retains source provenance.

Excluded datasets include seller identity modeling, tender files/text,
financial statements, market prices, BOQ intelligence and empty/schema-only
tables. Their reasons are documented in the Phase 0 audit and Phase 3 report.

Secrets are never committed or logged. The Phase 3 migration is additive and
creates only 'tg_*' control-plane tables.

## Synchronization CLI

Run commands from 'api/'.

Inspect the registry:

~~~sh
go run ./cmd/graphsync -op list
~~~

Preview a selected dataset without graph or control-plane writes:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op initial -dataset contractor_v2 -dry-run
~~~

Run the complete initial projection in dependency order:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op initial -batch-size 500 -verify
~~~

Run one dataset:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op initial -dataset tenders -batch-size 500
~~~

Run incremental synchronization. Mutable datasets use a bounded one-hour
lookback and a compound timestamp/primary-key cursor; replay is intentional and
idempotent:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op incremental
~~~

Retry a failed dataset from its durable checkpoint:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op retry -dataset tender_participants
~~~

Inspect durable state:

~~~sh
go run ./cmd/graphsync -op status
go run ./cmd/graphsync -op failed -dataset tender_participants
~~~

Reconcile without deleting anything:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op reconcile -dataset tender_participants -reconcile-mode detect
~~~

Repair is explicit. It replays the selected dataset; stale graph records are
reported but are not automatically deleted:

~~~sh
HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token go run ./cmd/graphsync -op repair -dataset tender_participants
~~~

The Phase 2 vertical-slice tool remains available:

~~~sh
go run ./cmd/graphproject -tender-id 16234
~~~

## Existing dashboard

Run the existing API:

~~~sh
cd api
go run .
~~~

Run the frontend:

~~~sh
cd frontend
npm install
npm run dev
~~~

The Vite development server proxies '/api' to port 8091. A production frontend
build is served by the Go API when 'api/dist' exists.

## Tests

Unit/build checks:

~~~sh
cd api
go test ./...
go vet ./...
~~~

Live integration checks require the development HydraDB token and PostgreSQL
connection:

~~~sh
cd api
set -a
. .env
set +a
export HYDRADB_AUTH_TOKEN_FILE=/path/to/auth-token
go test -tags integration ./...
~~~

Graph integration tests use bounded disposable probe records where cleanup is
appropriate; ingestion recovery tests use approved projection records and do
not delete the graph projection. No integration test modifies PostgreSQL
source tables.

## Documentation

- Phase 0 audit and ontology: 'docs/phase0/'
- HydraDB application integration: 'docs/phase1/PHASE_1B_REPORT.md'
- Projection foundation: 'docs/phase2/PHASE_2_REPORT.md'
- Full ingestion and synchronization: 'docs/phase3/PHASE_3_REPORT.md'
- Control-plane migration: 'api/migrations/002_tendergraph_sync.sql'

The HydraDB compatibility documents distinguish verified runtime behavior from
documentation-only or still-unverified assumptions. In particular, the
verified replay form is batch relationship 'MERGE' with a deterministic
relationship id; relationship 'CREATE' is not used for replay-safe writes.
