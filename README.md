# Vyuha — Financial Evaluation Entity Mapping & Intelligence Dashboard

Procurement intelligence that reconstructs the mapping between companies appearing
in government financial evaluations / awarded-tender records and Vyuha's listed-company
`entities` master — derived entirely from PostgreSQL, with the matching methodology
fully exposed in the dashboard.

## Pipeline

```
Original name (tender_participants / tender_extracted_meta)
        ↓ stored *_normalized values (validated to ~99.9% by shared SQL expression)
        ↓ Entity resolution (deterministic, priority-ordered, no fuzzy matching)
        ↓ Canonical listed company (entities)
        ↓ NSE / BSE / ISIN
        ↓ Financial data (entity_financials, latest_entity_financials)
        ↓ Market capitalization (latest_entity_market_data)
        ↓ Procurement intelligence (materiality, coverage, data quality)
```

## Matching priority (never fuzzy)

| Priority | Financial evaluation | Awards | Confidence |
|---|---|---|---|
| 1 | `participant_normalized` = `entities.normalized_name` | `awardee_normalized` = `entities.normalized_name` | highest |
| 2 | = `entity_aliases.normalized_alias` | same | high |
| 3 | = normalized(`canonical_name`) | normalized(`awardee_name`) = normalized(`canonical_name`) | high |
| 4 | = normalized(`entity_gst.company_name`) | exact GSTIN: `contractor_v2.gst` = `entity_gst.gstin` | secondary |
| 5 | unresolved | normalized name = `entity_gst.company_name` | secondary |
| — | — | unresolved | |

* A record matching **more than one entity at the winning priority** is reported
  `ambiguous` with every candidate visible — never silently resolved.
* `entity_gst.company_name` matching is a secondary candidate that never overrides
  a direct or alias match.
* Award GST identity additionally uses the tender's `contractor_v2` assignment by
  exact GSTIN equality; contractor names alone are never trusted as proof of the
  listed parent.
* The normalization expression (validated against production data:
  5973/5973 entities, ≈99.8% of participants/awardees) lives in
  `api/internal/sqlf/sqlf.go`. Stored normalized columns always take precedence.

## Data facts discovered (used by the SQL)

* `tender_extracted_meta` is 1:1 with `tenders` (unique FK) — no join multiplication.
* `aoc_date` is empty in production; `contract_date` is the award date and fiscal
  year (April–March, `FY2027` etc).
* `awarded_value` / `financial_bid_value` are varchar with Indian/western
  comma grouping — parsed by stripping commas; 9 negative bid values parsed.
* Money: procurement values are **rupees**; financials/market cap are **Rs crore** —
  materiality ratios normalize before dividing, and stay NULL for missing/zero denominators.
* Financial rank L1 buckets verified across variants (`L1`, `1`, `1ST`).
* `contractor` (v1) is empty; `seller_details` has no GSTIN and is unused.

## Run

```bash
# 1. API (env via .env or environment)
cd api
cp .env .env   # already contains DATABASE_URL / PORT
go run .       # listens on PORT (default 8091)

# 2. Dashboard
cd ../frontend
npm install
npm run dev    # http://localhost:5173 (proxies /api -> :8091)

# production build (served by the API when ./dist exists next to its CWD)
npm run build
```

## API

```
GET /api/v1/overview/kpis
GET /api/v1/financial-evaluation            ?page&page_size&sort&dir&q&mapping_status&method&winner&lifecycle&rank&entity_id
GET /api/v1/financial-evaluation/summary
GET /api/v1/financial-evaluation/leaderboard ?q&sort&dir
GET /api/v1/awards                          ?page&page_size&sort&dir&q&mapping_status&method&fiscal_year&buyer_org&lifecycle&entity_id
GET /api/v1/awards/summary
GET /api/v1/awards/leaderboard              ?q&fiscal_year&sort&dir
GET /api/v1/mappings/summary
GET /api/v1/mappings/financial-evaluation   (= financial-evaluation list)
GET /api/v1/mappings/awards                 (= awards list)
GET /api/v1/mappings/unresolved             ?dataset=fe|awards|all
GET /api/v1/mappings/ambiguous              ?dataset=fe|awards|all
GET /api/v1/companies
GET /api/v1/companies/{entityId}
GET /api/v1/tenders/{tenderId}              (tender + meta + contractor + mapping outcome, participants, files, BOQ)
GET /api/v1/materiality                     ?q&has_financials&sort&dir
GET /api/v1/data-quality
```

All filtering, sorting, pagination and aggregation happen server-side; every user
input reaches PostgreSQL only through parameter placeholders.

## Headline results (current dataset)

* Financial evaluation: 13,815 records / 2,844 tenders / 6,084 unique names
  → 89 mapped (63 entities, 2,019 cr bid value), 1 ambiguous, 13,725 unresolved.
* Awards: 18,222 records
  → 391 mapped-listed (177 entities; ≈ Rs 95,967 cr of awarded value), 7 ambiguous.
* Mapping methods: direct 72 + GST-name 17 (feval); direct 298 + alias 4 +
  GST-contractor 34 + GST-name 61 (awards).
