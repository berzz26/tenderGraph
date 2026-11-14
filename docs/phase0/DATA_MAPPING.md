# DATA_MAPPING.md — PostgreSQL → HydraDB field-level mapping (Phase 0)

Conventions (see GRAPH_ONTOLOGY.md §3 — authoritative):
- **Identity**: `gid` string (`<TYPE>:<origin>:<key>`) is the canonical application identity;
  the HydraDB vertex `id` is a **versioned 63-bit hash of the complete gid string**
  (non-negative integer; string ids are not documented for HydraDB and are not used).
- Timestamps → `*_ms` epoch-millis ints (+ ISO string where a display prop exists).
- **Money (two representations per field — DECISIONS D13)**: `<field>_exact str` = canonical
  **exact decimal string**, preserving source precision (procurement columns parsed from
  varchar with `sqlf.NumValExpr` semantics, unit **rupees**; financial/market columns already
  `numeric(20,2)`, unit **Rs crore**); `<field>_inr` / `<field>_crore` float = derived
  analytics copy. Floats are never claimed exact; exact arithmetic stays in PostgreSQL or Go
  decimal code. No silent unit conversion anywhere.
- Null handling: absent/empty/`''` → property **omitted** (not written as null; HydraDB
  null cells have no `value` key — omitting avoids that class entirely).
- Dedup: nodes MERGE/upsert by numeric `id` (verified semantics); re-runs are replay-safe and
  **at-least-once**, not exactly-once. Relationship uniqueness is **not** assumed from
  HydraDB — row-based edges are idempotent via deterministic replay; multi-observation facts
  are node-based (`CoBidObservation`). Ambiguity is never resolved silently — multi-candidates
  stay separate `IDENTIFIED_AS` CANDIDATE edges, and **no rule auto-confirms identity**
  (analyst-only transitions, DECISIONS D6).

Dependency order (parents before children; label batch always before edge batch — the
HydraDB "labels-first" rule):

```
1. GovernmentOrganization        (derived from tenders.organization)
2. CorporateEntity               (entities)
3. Alias, GstRegistration        (entity_aliases, entity_gst)
4. Vendor                        (contractor_v2)
5. Tender                        (tenders + tender_extracted_meta folded)
6. Award                         (tender_extracted_meta award evidence)
7. Bid                           (tender_participants)
7b. CoBidObservation             (derived: same-tender participant pairs)
8. BOQItem                       (boq_items via tender_files)
9. FinancialStatement, MarketObservation, CorporateAnnouncement, CorporateEvent
10. GemContract, GeMCategory     (seller_details, gem_categories)
11. IdentityCandidate/Evidence   (tg_* control tables; dashboard mapping CTEs are used
                                 for validation only — never as confirmation)
12. Edges in that same order (parents→children), identity edges last
```

## 1. Procurement core

### 1.1 `tenders` (+ `tender_extracted_meta` folded, 1:1 verified) → `Tender`
| Source col | Target property | Type | Transform / null rule |
|---|---|---|---|
| `tenders.id` | `id` (versioned 63-bit hash of the complete gid `TENDER:v:{id}`), `src_pk` | int / str | direct |
| `tenders.tender_id` | `tender_id` | str | direct (external id) |
| `tenders.organization` | `organization` | str | trim; also drives `ISSUED_BY` |
| `tenders.lifecycle_stage` | `lifecycle_stage` | str | direct (open/financial_evaluation/aoc verified) |
| `tenders.website_url` | `website_url` | str | omit if empty |
| `tenders.created_at/updated_at` | `created_at_ms/updated_at_ms` | int | epoch-ms |
| meta.`title`, `ref_no`, `category`, `product_category`, `location`, `description`, `contract_type`, `pincode`, `sub_category`, `form_of_contract` | same-named props | str | omit NULL/'' |
| meta.`tender_value` | `tender_value_exact` str + `tender_value_inr` float | str/float | `NumValExpr` (rupees); `_exact` keeps the canonical decimal string, float is analytics only |
| meta.`emd_value` | `emd_value_exact` str + `emd_value` num | str/int | int column, rupees; exact string of the integer value |
| meta.`published_date`, `bid_opening_date`, `bid_submission_start_date`, `bid_submission_end_date`, `financial_bid_opening_actual_date`, `financial_evaluation_date` | `*_ms` + `*_iso` | int/str | epoch-ms; `aoc_date` excluded (100% NULL verified) |
| meta.`contract_date` | `contract_date` | str ISO date | 16,589/26,124 set |
| meta.`no_of_covers`, `bid_validity_days`, `period_of_work_days` | same | str | varchar passthrough |

Dedupe: `tenders.tender_id` unique in DB; PK merge. **Dependency**: GovernmentOrganization first.

### 1.2 `tender_extracted_meta` (award evidence only) → `Award`
Created only when award evidence exists (`awardee_name`/`awarded_value`/`winning_bid_number` set — 20,329 rows verified).
| Source col | Target property | Transform |
|---|---|---|
| `id` | `id` via `AWARD:v:{id}`, `src_pk` | direct |
| `awardee_name`, `awardee_normalized` | `awardee_name`, `awardee_normalized` | direct / normalized |
| `awarded_value` | `awarded_value_exact` str + `awarded_value_inr` float | `NumValExpr` (rupees); exact string canonical, float analytics |
| `winning_bid_number` | `winning_bid_number` | trim; empty → omit |
| `contract_date`, `contract_updated_date` | ISO str / `*_ms` | — |
| (view) `outcome_classification` | `award_stage` | recompute at projection time using the view's exact CASE logic (`BID_NUMBER_MATCH`/`AWARDEE_NAME_MATCH` + `aoc_date`) — not read from the view |

### 1.3 `tender_participants` → `Bid`
| Source col | Target | Transform |
|---|---|---|
| `id` | `id` via `BID:v:{id}`, `src_pk` | direct |
| `bid_number` | `bid_number` | direct |
| `participant_name` / `participant_normalized` | `participant_name` / `participant_normalized` | direct (norm present in 22,865/22,865 rows — verified) |
| `financial_rank` | `financial_rank` | direct; NULL for 3,871 rows → omit |
| `financial_bid_value` | `financial_bid_value_exact` str + `financial_bid_value_inr` float | `NumValExpr` (exact string canonical; float preserves the 9 negative values for analytics) |
| `is_winner` | `is_winner` | bool |
| `technical_status`, `financial_status` | same | direct |
| `submitted_date`, `technical_status_updated_on`, `financial_status_updated_on` | `*_ms` | epoch-ms |
| `extras` | omitted (jsonb, unbounded) | — |

Edges: `SUBMITTED_BID (Bid→Tender)` FK-backed; co-bidding is **not** a `Bid→Bid` edge —
each same-tender pair of participant rows gets one derived `CoBidObservation` node
(`COBID:v:<tender_pk>:<min_pk>:<max_pk>`) with two `CO_BID_IN` edges (`Bid→Obs` from each
participant). Cap 85 participants/tender verified; pairs generated in batches. A direct
pair-edge would be collapsed by the documented HydraDB batch dedupe on `(src,dst,type)`,
destroying per-tender evidence (DECISIONS D12).

### 1.4 `boq_items` → `BOQItem`
| Source col | Target | Transform |
|---|---|---|
| `id` | `id` via `BOQ:v:{id}` | direct |
| `sl_no`, `item_description`, `units` | same | direct |
| `quantity`, `rate`, `rrr`, `awardee_rate`, `awardee_amount` | money: `<f>_exact` str + `<f>_inr` float | floats for analytics; NULL → omit (34k–185k NULLs verified); exact decimal string canonical |
| `tender_file_id` | — | resolved through `tender_files.tender_id` at projection (no direct FK to tenders — verified) |

Edge: `CONTAINS_BOQ_ITEM (Tender→BOQItem)`, props `via_tender_file_pk`. Skip rows of
`is_deleted=true` files. Embedding column excluded (pgvector stays in PostgreSQL).

## 2. Corporate identities

### 2.1 `entities` → `CorporateEntity`
| Source col | Target | Transform |
|---|---|---|
| `id` uuid | `id` via `ENTITY:v:{uuid}`, `src_pk` | uuid string |
| `canonical_name`, `normalized_name` | same | direct |
| `ticker_nse`, `ticker_bse`, `exchange_status`, `exchange` | same | direct |
| `is_listed` | `is_listed` | bool |
| `sector`, `industry`, `website`, `isin` | same | omitted today (100% NULL/empty verified) |
| `business_standard_id` | `business_standard_id` | int64 → float-safe int |

### 2.2 `entity_aliases` → `Alias` + edge `HAS_ALIAS`
Props: `alias_name`, `normalized_alias`, `source`; edge from `entity_id`.

### 2.3 `entity_gst` → `GstRegistration` + edges `HAS_GST_REGISTRATION`
Props: `gstin`, `company_name`, `state`. `GST_LINKS (Vendor/GstRegistration → CorporateEntity)`
only from the exact-GSTIN join (45 bridging GSTINs verified); always `status='CANDIDATE'`.

### 2.4 `contractor_v2` → `Vendor`
| Source col | Target | Transform |
|---|---|---|
| `id` | `id` via `VENDOR:cp:{id}` | direct |
| `business_name` | `business_name` | direct |
| `gst` | `gstin` | direct (3,257/3,257 present) |
| `email` | omitted (100% NULL verified) | |
| `address`, `legal_status`, `poc` | same | omit NULL |
| `created_at`/`updated_at` | `*_ms` | |

Edges: `CONTRACTOR_ON_RECORD (Vendor→Tender)` from `tenders.contractor_v2_id` (6,656 tenders) —
**contractor-of-record assignment only; not bid participation, not winner evidence**
(DECISIONS D10); bidder participation derives solely from `tender_participants`;
`location_id` excluded (location_meta empty — verified).

## 3. Financials / market

### 3.1 `entity_financials` → `FinancialStatement`
| Source col | Target | Transform |
|---|---|---|
| `id` bigint | `id` via `FIN:v:{id}` | int64-as-string for src_pk |
| `fiscal_year` | `fiscal_year` | str, values `FY2003..FY2026` + `TTM` (verified) |
| `revenue, ebitda, net_profit, net_worth, total_assets, equity_capital, reserves, borrowings, other_liabilities, fixed_assets, cwip, investments, other_assets` | `<f>_exact` str + `<f>_crore` float | numeric(20,2) → exact decimal string canonical + float analytics; Rs crore, no conversion |
| `source`, `source_date` | `source`, `source_date_iso` | — |

Edge: `HAS_FINANCIAL_STATEMENT`. Dedupe key `(entity_id, fiscal_year)` (DB-unique).

### 3.2 `entity_quarterly_financials` → `FinancialStatement` (kind=`QUARTERLY`)
Props `quarter_end_date_iso`, `revenue_exact`/`revenue_crore`, `net_profit_exact`/`net_profit_crore`, `source`; edge
`HAS_QUARTERLY_FINANCIALS`. Distinct label rule: `kind` property distinguishes annual vs
quarterly (single label to keep label-anchored counts complete).

### 3.3 `entity_market_data` → `MarketObservation`
Props `snapshot_date_iso`, `market_cap_exact`/`market_cap_crore` (Rs crore),
`current_price_exact`/`current_price` (Rs/share), `shares_outstanding`; edge
`HAS_MARKET_OBSERVATION`. Use base table (not `latest_entity_market_data` view) — the view is
a projection of the same rows; validation only.

### 3.4 `market_daily_prices`, `market_data_ingestions` — **excluded** (0 rows verified). Revisit when populated; dashboard uses live TejHQ proxy meanwhile.

## 4. Disclosures

### 4.1 `announcements_raw` → `CorporateAnnouncement`
| Source col | Target | Transform |
|---|---|---|
| `id` bigint | `id` via `ANN:v:{id}` | — |
| `seq_id`, `title`, `category`, `company_name`, `symbol`, `isin`, `source` | same | direct |
| `announcement_time` | `announcement_at_ms` + `_iso` | timestamp no tz → epoch-ms (UTC assumed; verify with pipeline owner) |
| `has_xbrl` | `has_xbrl` | bool |
| `entity_id` | — | drives `ANNOUNCED` edge only when set (42/148 NULL verified) |
| `raw_json`, `attachment_url` | `attachment_url` prop; raw_json excluded | — |

### 4.2 `company_events` → `CorporateEvent` (0 rows; projection ships schema-ready)
Props `event_type`, `event_subtype`, `event_date_iso`, `importance_score`,
`extracted_data` (JSON string). Edges `HAS_EVENT` (announcement→event, FK),
`EVENT_OF` (event→entity, nullable FK).

### 4.3 `tender_announcement_matches` → `MATCHED_TO` edges (0 rows)
Edge props: `match_status` (`CANDIDATE/CONFIRMED/REJECTED` — preserved verbatim),
`match_method`, `reviewed_by`, `reviewed_at_ms`, `src_pk`. Unique `(tender_id, announcement_id)`.

## 5. GeM

### 5.1 `seller_details` → `GemContract` nodes (no `Seller` nodes in the baseline)
- **A `seller_details` row is a contract, not a seller identity** (DECISIONS D11). The safe
  baseline is one `GemContract` node per row (`GEMC:v:{uuid}`):
  `contract_no` (DB-unique — verified constraint, business dedup key), `contract_date_iso`,
  `contract_status`, `buying_mode`, `contract_value_exact` str + `contract_value` float
  (rupees — GeM contract values are INR), buyer fields (`org_name`, `ministry`, `department`).
- Seller attributes (`seller_company_name`, `seller_gstin` from `gst_in`, `seller_pan`,
  `gem_seller_id`, `msme_registration_number`) stay **on the contract node** as a snapshot.
  **All seller identity fields are verified empty in live data** (0 non-null of 4,078 rows
  for `gem_seller_id`, `gst_in`, `pan`, `company_name`) — they are normally omitted per the
  null rule. There is therefore **no stable seller identity key today**: not `gem_seller_id`
  (all empty), not GSTIN/PAN/company name (all empty), not the UUID row id (per-contract),
  and `contract_no` identifies the contract, not the seller.
- A separate `Seller` node may be introduced only when data supports a stable seller identity
  model (documented key, normalization, duplicate and ambiguity handling, evidence linking
  contracts to one seller). Sellers are **never** merged on GSTIN-only or name-only equality.
- Edges: `IN_CATEGORY (GemContract→GemCategory)` from the `seller_details.category` FK.
  No `SELLS_VIA_CONTRACT` edge (no Seller endpoint exists).
- **No link to tenders** (verified: no FK, no contract_no overlap). GeM stays a separate
  island component of the graph; identity merging between GeM sellers and
  Vendor/CorporateEntity is explicitly out of scope for the initial projection.

### 5.2 `gem_categories` → `GemCategory`
Props `category_code` (unique), `category_name`.

## 6. `vw_vyuha_l1_intelligence` role decision

**Role: (a) validation source + (b) supplementary analytical projection; NOT an ingestion
source.** Rationale:
- It covers only L1 participants (5,317 rows vs 22,865 participants) and its entity mapping
  is single-candidate-only (`mapped_entity_id` NULL when ambiguous) — using it as the source
  would bake a coarser identity model into the graph.
- Its award-stage classification (`AOC_CONFIRMED` etc.) and numeric casts are recomputed by
  the projection engine using the same expressions (kept in `api/internal/sqlf`), then
  **reconciled row-by-row against the view** as an automated projection test:
  counts of `outcome_classification` and `entity_mapping_status` per fiscal year must match.

## 7. Cross-cutting transformation rules

| Rule | Detail |
|---|---|
| Money parse | `NULLIF(REGEXP_REPLACE(TRIM(col), '[, ]', '', 'g'), '')::numeric` (`sqlf.NumValExpr`); invalid → NULL → omit property; keep negatives |
| Fiscal year | `sqlf.FYExpr(contract_date)` for award-year rollups (April–March) |
| Name normalization | `sqlf.NormExpr` for any name not already stored normalized; stored `*_normalized` columns always take precedence (README contract) |
| GID hashing | one **versioned 63-bit hash of the complete gid string**, implemented once in Go (Phase 1; not implemented in Phase 0); pre-write collision check — an existing node at the same numeric `id` must carry the same `gid`, else the batch aborts and the pair is logged (`tg_id_collisions`-style additive table); never a silent merge; `id_algo_v` property recorded on nodes |
| Batching | ≤1000 rows per `UNWIND` request (HydraDB cap 1024 — verified) |
| Idempotency | every batch is replay-safe and **at-least-once** (not exactly-once); a re-run from the last watermark is safe; the checkpoint advances only after the graph write is verified (ARCHITECTURE §2.6) |
| Relationship identity | row-based edges replay deterministically; HydraDB relationship-MERGE-by-`id` uniqueness is under-specified and **not assumed**; multi-observation facts are `CoBidObservation` nodes because the documented batch dedupe on `(src,dst,type)` collapses multi-edges by design |
| Label coverage | labels written in the same node batch (labels-first rule — verified HydraDB caveat) |

## 8. Excluded from initial projection (with reason)

`tender_files`, `tender_file_texts` (document pipeline), `tender_parsed_meta` (empty),
`tender_states` (pipeline state; watermark source only), `location_meta` (empty),
`contractor` (empty), `market_daily_prices` + `market_data_ingestions` (empty),
`product_details` (empty), `gem_basket` (empty), `delivery_history` (CRM domain),
`embedding_batch`, `embeddings_title_desc`, `boq_items.item_description_embedding`
(pgvector stays in PostgreSQL), CRM/auth tables.
