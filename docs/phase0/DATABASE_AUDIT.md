# DATABASE_AUDIT.md — PostgreSQL `vyuha` schema audit (Phase 0)

Source: live `pg_dump --schema-only` (PostgreSQL 16.14) taken 2026-11-14, plus read-only
row counts and data-quality probes. Full dump retained at `/tmp/opencode/vyuha_schema.sql`
during the session; the authoritative copy is the live DB.

Legend: **PK** primary key · **FK** foreign key (verified in `pg_constraint`, not inferred) ·
**Src** source fact (ingested from scraping) vs **Derived** (computed by the Vyuha repo or its migration) ·
**Phase-1?** required in the initial graph projection.

Extensions installed in the DB: `pg_trgm`, `vector` (pgvector). Views present:
`vw_vyuha_l1_intelligence`, `latest_entity_financials`, `latest_entity_market_data`.

## 1. Row-count snapshot (verified)

| Table | Rows | Table | Rows |
|---|---|---|---|
| tenders | 26,125 | entities | 5,973 |
| tender_extracted_meta | 26,124 | entity_aliases | 6,889 |
| tender_parsed_meta | **0** | entity_gst | 16,134 |
| tender_participants | 22,865 | contractor | **0** |
| tender_files | 45,097 | contractor_v2 | 3,257 |
| tender_file_texts | 38,641 | location_meta | **0** |
| tender_states | 26,125 | entity_financials | 62,585 |
| boq_items | 356,048 | entity_quarterly_financials | 70,381 |
| announcements_raw | 148 | entity_market_data | 41,976 |
| announcement_documents | 139 | market_daily_prices | **0** |
| company_events | **0** | market_data_ingestions | **0** |
| tender_announcement_matches | **0** | seller_details | 4,078 |
| gem_categories | 19 | product_details | **0** |
| gem_basket | **0** | delivery_history | **0** |
| vw_vyuha_l1_intelligence | 5,317 | | |

## 2. Procurement domain

### 2.1 `tenders` — Src, Phase-1: YES
- **PK** `id integer`. **Unique** `tender_id varchar(100)` (external GeM/eproc id).
- Columns: `website_url, organization, lifecycle_stage ('open'|'financial_evaluation'|'aoc')`,
  `contractor_id int FK→contractor(id)`, `contractor_v2_id int FK→contractor_v2(id)`,
  `discovery_scrape_id uuid FK→scrapes(id)`, timestamps.
- Indexes: `tenders_pkey`, `tenders_tender_id_key`.
- Cardinality: 1 tender → N `tender_participants` (FK `tender_participants_tender_id_fkey`,
  ON DELETE CASCADE), 1:1 `tender_extracted_meta` (FK, CASCADE), 1:1 `tender_parsed_meta`
  (FK, CASCADE), 1:1 `tender_states` (FK, CASCADE), 1:N `tender_files` (FK, CASCADE).
- **Key insight**: `organization` is free text on the tender, not an FK — buyers are
  string-identified only. `contractor_v2_id` links 6,656 tenders to a contractor record —
  **a contractor-of-record assignment on the tender row; it is not bid participation and not
  winner evidence** (see §3.5 and DECISIONS D10).

### 2.2 `tender_extracted_meta` — Src (scraper-extracted), Phase-1: YES
- **PK** `id integer`; **Unique FK** `tender_id integer → tenders.id` (verified 1:1).
- Key columns: `org_name`, `title`, `ref_no`, `category`, `product_category`, `location` (text!),
  `published_date/bid_opening_date/bid_submission_*` (timestamp), `tender_value varchar(100)`,
  `contract_type`, `pincode`, `awardee_name varchar(100)`, `awardee_normalized text` (Derived —
  populated by the scraper pipeline using the Vyuha normalization convention),
  `awarded_value varchar(100)`, `contract_date date`, `winning_bid_number varchar(100)`,
  `financial_bid_opening_actual_date`, `financial_evaluation_date`, `aoc_date` (timestamp, all NULL
  in production), `emd_value integer`, `extras jsonb`.
- **Money is varchar** (`tender_value`, `awarded_value`) — parse with `sqlf.NumValExpr`.
- Indexes: pkey, `tender_extracted_meta_tender_id_key`.

### 2.3 `tender_participants` — Src, Phase-1: YES
- **PK** `id integer`. **Unique** `(tender_id, bid_number)` (`uq_tender_bid_number`).
- FK `tender_id → tenders.id` CASCADE.
- Columns: `bid_number`, `participant_name`, `participant_normalized text` (Derived),
  `submitted_date`, `technical_status/remarks/updated_on`, `financial_status/remarks/updated_on`,
  `financial_rank varchar(20)`, `financial_bid_value varchar(100)`, `is_winner boolean`,
  `extras jsonb`.
- Indexes: `ix_tender_participants_tender_id`, `idx_tender_participant_rank`,
  `idx_tender_participant_name (participant_normalized)`.
- Cardinality: max observed 85 participants per tender; 733 tender pairs share ≥2 participants
  (sampled 78,201 co-bid rows in a 500k cap) — **the co-bid signal is real and needs care**
  (modeled as `CoBidObservation` nodes so per-tender evidence survives dedupe — DECISIONS D12).

### 2.4 `tender_files` / `tender_file_texts` — Src, Phase-1: files NO (provenance only), texts NO
- `tender_files`: PK `id`; FK `tender_id → tenders.id` CASCADE; `file_type`, `cloud_file_name`
  (MinIO key), `pdf_parse_state`, `is_boq_parsed_success`, `pdf_keywords jsonb`.
- `tender_file_texts`: PK `id`; FK `tender_file_id → tender_files.id` CASCADE; **Unique**
  `(tender_file_id, page_number)`; `plain_text`, `page_classification`, `extraction_method`.
- These are document pipelines, not graph entities. Recommended: exclude from initial
  projection; optionally expose as file provenance on Tender nodes later.

### 2.5 `tender_parsed_meta` — Src pipeline table, Phase-1: NO
- PK `id`; Unique FK `tender_id → tenders.id`; `plain_text_nit/boq`, `makes_discovered`,
  `work_scope`. **0 rows in production** — cannot be projected; revisit if populated.

### 2.6 `tender_states` — pipeline state, Phase-1: NO (but useful for change detection)
- PK `id`; Unique FK `tender_id → tenders.id`; `state`, `error`, `title_desc_embedding_state`,
  `work_scope_generator_state`, `location_state` (defaults `'pickup'/'waiting'`). Only 2
  distinct `state` values observed. Synchronization watermark candidate.

### 2.7 `boq_items` — Src, Phase-1: YES (price intelligence)
- PK `id integer`; FK `tender_file_id → tender_files.id` CASCADE; FK
  `embedding_batch_id → embedding_batch.id` SET NULL.
- Columns: `sl_no`, `item_description` (NOT NULL), `quantity double precision`,
  `units`, `rate`, `rrr` (estimated rate), `awardee_rate`, `awardee_amount`, timestamps,
  `item_description_embedding vector(1536)` (only 1,200 of 356,048 rows have embeddings).
- **No FK to tender directly** — reach a tender via `tender_files.tender_id`.
- Data quality: 34,351 rows without `quantity`, 184,849 without `rate`, 34,431 without
  `awardee_rate`. `awardee_rate` presence is the award-price signal.

## 3. Corporate identity domain

### 3.1 `entities` — Src (listed-company master), Phase-1: YES (as CorporateEntity)
- **PK** `id uuid`. **Unique** `business_standard_id bigint`, `isin varchar(12)` (exists but
  NULL for all 5,973 rows). Indexes on `normalized_name`, `ticker_nse`, `ticker_bse`.
- Columns: `canonical_name`, `normalized_name` (NOT NULL, Derived), `ticker_nse/ticker_bse`,
  `exchange_status ('LISTED')`, `exchange`, `sector`, `industry`, `website`, `is_listed boolean`.
- All 5,973 rows are `is_listed=true`; no duplicate `normalized_name` (verified by GROUP BY).

### 3.2 `entity_aliases` — Src, Phase-1: YES
- PK `id uuid`; FK `entity_id → entities.id`; **Unique** `(entity_id, normalized_alias)`;
  index on `normalized_alias`.
- Columns: `alias_name`, `normalized_alias`, `source`.
- 6,889 aliases / 5,973 entities.

### 3.3 `entity_gst` — Src, Phase-1: YES
- PK `id bigint`; FK `entity_id → entities.id` CASCADE; **Unique** `(entity_id, gstin)`
  (`uq_entity_gstin`); index on `gstin`.
- Columns: `company_name` (name as returned by GST search), `state`, `gstin`.
- **Critical**: the unique constraint permits the same GSTIN under *multiple* entities —
  verified 733 GSTINs map to >1 `entity_id`, and 502 company names map to >1 entity. GSTIN
  equality is evidence, never proof.

### 3.4 `contractor` (v1) — Src, Phase-1: NO (empty)
- PK `id int`; Unique `email`, `gst`; FK `location_id → location_meta.id` SET NULL. **0 rows.**

### 3.5 `contractor_v2` — Src, Phase-1: YES (as Vendor / contractor registry)
- Same shape as v1: PK `id int`; Unique `email` (all NULL!), Unique `gst` (all 3,257 have GST);
  FK `location_id → location_meta.id` SET NULL.
- **One GSTIN per contractor row (enforced)**, and 45 distinct contractor GSTINs also appear
  in `entity_gst` — the exact-GSTIN identity evidence bridge the dashboard uses.
- `tenders.contractor_v2_id` (6,656 tenders) is the only link between tenders and contractor
  records; it is the award-side contractor-of-record assignment — **NOT proof of bid
  participation, NOT proof of winner identity, and never winner evidence** (DECISIONS D10).

### 3.6 `location_meta` — Src lookup, Phase-1: NO (empty)
- PK `id int`; columns `circle_name, region_name, division_name, office_name, pincode,
  office_type, delivery, district, state_name, latitude, longitude`. **0 rows.** Both
  `contractor(_v2).location_id` and `tender_extracted_meta.location` (free text) currently
  cannot be resolved to this table.

## 4. Financial domain

### 4.1 `entity_financials` — Src, Phase-1: YES
- PK `id bigint`; FK `entity_id → entities.id` CASCADE; **Unique** `(entity_id, fiscal_year)`.
- Columns: `fiscal_year varchar(10)` (values `FY2003`..`FY2026` **plus `TTM`**),
  `revenue, ebitda, net_profit, equity_capital, reserves, net_worth, borrowings,
  other_liabilities, fixed_assets, cwip, investments, other_assets, total_assets`
  — all `numeric(20,2)`, **units are Rs crore** (per README), `source`, `source_date`.
- 62,585 rows; only 4 rows lack `revenue`.

### 4.2 `entity_quarterly_financials` — Src, Phase-1: YES (lighter)
- PK `id bigint`; FK `entity_id` CASCADE; **Unique** `(entity_id, quarter_end_date)`.
- Columns: `quarter_end_date date`, `revenue`, `net_profit`, `source`, `source_date`.

### 4.3 `entity_market_data` — Src, Phase-1: YES
- PK `id bigint`; FK `entity_id` CASCADE; **Unique** `(entity_id, snapshot_date)`.
- Columns: `snapshot_date date`, `market_cap`, `current_price` (numeric, Rs crore), `shares_outstanding bigint`.
- Backed by views `latest_entity_market_data` (DISTINCT ON latest snapshot per entity; 11
  distinct snapshot dates observed).

### 4.4 `market_daily_prices` / `market_data_ingestions` — Src, Phase-1: NO (0 rows)
- Composite PK `(instrument_type, instrument_code, exchange, trading_date)`; nullable FK
  `entity_id → entities.id`; CHECKs: EQUITY requires entity_id, INDEX forbids it.
- `market_data_ingestions`: PK `id bigint`, FK `entity_id`, status
  (`RUNNING|SUCCEEDED|FAILED`), source default `tejhq`. Both empty — the daily-price pipeline
  exists only as schema. Market projection should use `entity_market_data`.

## 5. Corporate disclosures domain

### 5.1 `announcements_raw` — Src, Phase-1: YES
- PK `id bigint`; **Unique** `seq_id text`; index on `source_id`; FK `entity_id → entities.id`
  (nullable, 42/148 NULL); index on `isin`.
- Columns: `symbol`, `company_name`, `isin`, `category`, `title`, `announcement_time`
  (timestamp, no tz), `attachment_url`, `has_xbrl boolean`, `source`, `raw_json jsonb`.
- Only 148 rows, all 2026 — early-stage dataset.

### 5.2 `announcement_documents` — Src, Phase-1: NO (provenance)
- PK `id bigint`; FK `announcement_id → announcements_raw.id` CASCADE; `storage_path`,
  `pdf_text`, `status`, `extracted_at`. 139 rows.

### 5.3 `company_events` — Derived (extracted from announcements), Phase-1: YES (empty today)
- PK `id bigint`; FKs: `announcement_id → announcements_raw.id` CASCADE,
  `entity_id → entities.id` (nullable); `event_type` NOT NULL, `event_subtype`,
  `event_date`, `importance_score integer`, `extracted_data jsonb`. **0 rows** — schema ready,
  projection should be implemented but will be empty until the extraction pipeline runs.

### 5.4 `tender_announcement_matches` — Derived/analyst, Phase-1: YES (the MATCHED_TO source)
- PK `id bigint`; FKs `tender_id → tenders.id`, `entity_id → entities.id`,
  `announcement_id → announcements_raw.id`, `reviewed_by → auth_user.id`;
  **Unique** `(tender_id, announcement_id)`; `match_status CHECK IN ('CANDIDATE','CONFIRMED','REJECTED')`,
  `match_method`, `review_notes`, `reviewed_at`. **0 rows.** Created by
  `api/migrations/001_vyuha_intelligence.sql:76-90`.

## 6. GeM marketplace domain

### 6.1 `seller_details` — Src, Phase-1: YES (secondary scope)
- PK `id uuid`; **Unique** `contract_no varchar(100)`; FK `category → gem_categories(category_code)`.
- Columns: `contract_no`, `contract_date timestamptz`, `contract_status`, `org_type`,
  `org_name` (buyer), `ministry`, `department`, `buying_mode`, `contract_value double precision`,
  `company_name` (seller), `gem_seller_id`, `gst_in`, `pan`, `msme_registration_number`,
  buyer_* fields, `file_url`, scraper state columns.
- **No FK/link to `tenders`** — GeM contract data is an island; `contract_no` formats do not
  overlap tender ids (verified probe). Dedupe contract rows by `contract_no`.
- **Seller identity probe (2026-11-14 correction pass, live data)**: **all seller identity
  fields are empty — 0 non-null of 4,078 rows** for `gem_seller_id`, `gst_in`, `pan`, and
  `company_name` is also 0 non-null (probe checked non-empty values, distinct counts, and
  multi-name/multi-GSTIN overlaps: all zero). A `seller_details` row therefore carries **no
  usable seller identity key**; it is contract-level information only
  (`contract_no` unique). This grounds the `GemContract`-only baseline (DECISIONS D11) —
  the earlier per-row `Seller`-node interpretation was inaccurate.
- 4,078 rows.

### 6.2 `product_details` — Src, Phase-1: NO (0 rows)
- PK `id uuid`; FK `contract_id → seller_details.id` CASCADE; product/brand/hsn fields.

### 6.3 `gem_categories` — Src lookup, Phase-1: YES (small)
- PK `id int`; **Unique** `category_code`; FK `basket_id → gem_basket.id` (basket empty).
- 19 rows; referenced by `seller_details.category`.

### 6.4 `gem_basket`, `delivery_history` — Phase-1: NO (0 rows)
- `gem_basket`: PK `id uuid`, `name`. `delivery_history`: CRM delivery log, FKs to
  `customer_presets`, `campaigns`, `auth_user`, `seller_details` — belongs to the CRM system,
  not procurement intelligence.

## 7. Supporting tables NOT relevant to TenderGraph (verified, listed for completeness)

`alembic_version`, `api_keys`, `auth_user` (except as reviewer FK), `campaigns`,
`captcha_attempts`, `failed_captchas`, `customer_presets`, `customers`, `enquiries`,
`embedding_batch`, `embeddings_title_desc` (FK→tender_states.tender_id), `phone_number`,
`presets`, `profiles`, `scrapes` (except as `tenders.discovery_scrape_id` provenance),
`sources`, `tender_view`, `views`.

## 8. The view `vw_vyuha_l1_intelligence` — VERIFIED in live DB

- Single SELECT over: L1 participants (normalized `financial_rank ∈ {'1','L1','1ST'}`) →
  two-priority entity resolution CTEs (priority 1 `entities.normalized_name`,
  priority 2 `entity_aliases.normalized_alias`) → ambiguous-safe single-candidate mapping
  (`candidate_count`, `mapped_entity_id` NULL when ambiguous) → winner-evidence
  classification (`BID_NUMBER_MATCH` / `AWARDEE_NAME_MATCH` / `WINNER_FLAG_ONLY` /
  `NO_WINNER_EVIDENCE`) → `outcome_classification` (`AOC_CONFIRMED`,
  `L1_SELECTED_AOC_PENDING`, `L1_NOT_SELECTED`, `L1_AWAITING_DECISION`) →
  `entity_mapping_status` (`MATCHED|AMBIGUOUS|UNRESOLVED`) → numeric money casts and
  `days_financial_eval_to_aoc`.
- Joins in `latest_entity_financials` and `latest_entity_market_data` for the mapped entity.
- 5,317 rows.
- **Role in TenderGraph**: (a) *validation source* — projected award/participant/identity
  edges must reconcile against this view's `entity_mapping_status` and
  `outcome_classification`; (b) *supplementary analytical projection* for quick materiality
  columns at query time. It is **not** a replacement for the underlying tables: it only
  covers L1 participants, drops the non-L1 participant population, and its ambiguity rule
  (single-candidate-only) is coarser than what the identity engine will expose. Column list
  verified from the dump (lines 1653-2026).

## 9. Data-quality findings (verified probes; nothing was modified)

| # | Finding | Evidence |
|---|---|---|
| Q1 | **No FK exists on `tender_extracted_meta.awardee_*` to entities/contractor.** Award identity is purely name-normalization derived (`awardee_normalized`). | schema + live counts (20,329 awardee names, 0 FK) |
| Q2 | `awarded_value` present in 20,329 rows but `contract_date` only in 16,589 → 3,740 awards without an award date; `aoc_date` NULL in 100% of rows. | probes |
| Q3 | `awarded_value`/`tender_value`/`financial_bid_value` are varchar; all values parse under `NumValExpr` (probe found 0 non-numeric rows), including 9 negative bid values. | probes + README |
| Q4 | **733 GSTINs map to >1 `entities` row**; 502 GST `company_name`s map to >1 entity. GSTIN equality cannot be treated as unique identity. | probe |
| Q5 | `entities.isin` is NULL for all 5,973 rows despite a unique index; `sector`, `industry`, `exchange`, `website` also unpopulated. | sample + probe |
| Q6 | `contractor_v2.email` NULL in 3,257/3,257; `contractor` v1 table empty; `location_meta` empty → no geo resolution possible today. | probes |
| Q7 | Only 45 contractor GSTINs intersect `entity_gst.gstin` — the GST-based identity bridge is narrow; name-based mapping remains dominant. | probe |
| Q8 | 3,871/22,865 participants have NULL `financial_rank`; winner evidence relies on `winning_bid_number` (3,887 rows) and `awardee_normalized`. | probes |
| Q9 | BOQ: 34,351 items without quantity, 184,849 without `rate`, 34,431 without `awardee_rate`; only 1,200/356,048 have description embeddings. | probes |
| Q10 | `announcements_raw.entity_id` NULL in 42/148 rows; announcement coverage is tiny (148 rows, all 2026). | probes |
| Q11 | ID-type heterogeneity: `tenders.id` int32, `entities.id` uuid, `announcements_raw.id` int64, `seller_details.id` uuid, `boq_items.id` int32 — graph IDs must prefix/namespace these. | schema |
| Q12 | Units differ by domain: procurement values are rupees; financials and market cap are Rs crore. Any cross-domain ratio must normalize (README + view code). | README §Data facts |
| Q13 | `fiscal_year` includes the value `TTM` alongside `FY####` — a temporal property must not assume the FY pattern. | probe |
| Q14 | `tender_parsed_meta`, `market_daily_prices`, `market_data_ingestions`, `company_events`, `tender_announcement_matches`, `product_details`, `gem_basket`, `location_meta`, `contractor`, `delivery_history` are all empty — schema-only areas. | row counts |
| Q15 | 8,864 distinct normalized participant names; dashboard reports 13,725 unresolved financial-evaluation names — identity resolution coverage is currently ~0.6% of participants, 391 mapped awards. | probes + README §Headline results |

## 10. Recommended initial graph scope

Phase-1 projection should include: `tenders`, `tender_extracted_meta`,
`tender_participants`, `entities`, `entity_aliases`, `entity_gst`, `contractor_v2`,
`entity_financials`, `entity_quarterly_financials`, `entity_market_data`,
`announcements_raw`, `company_events` (schema-ready), `tender_announcement_matches`,
`boq_items` (award-price subset), `gem_categories`, `seller_details` (GeM scope).
Exclude until non-empty: `tender_parsed_meta`, `location_meta`, `contractor`,
`market_daily_prices`, `market_data_ingestions`, `product_details`, `gem_basket`,
`delivery_history`, `tender_file_texts` (large text, no graph need yet).
