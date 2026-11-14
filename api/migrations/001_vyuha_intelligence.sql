CREATE OR REPLACE VIEW public.vw_vyuha_l1_intelligence AS
WITH l1_participants AS (
  SELECT tp.*
  FROM tender_participants tp
  WHERE regexp_replace(upper(trim(coalesce(tp.financial_rank, ''))), '[^A-Z0-9]', '', 'g') IN ('1', 'L1', '1ST')
), entity_candidates AS (
  SELECT tp.id participant_id, e.id entity_id, 'canonical_normalized_name'::text match_method, 1 priority
  FROM l1_participants tp JOIN entities e ON e.normalized_name = tp.participant_normalized
  WHERE tp.participant_normalized IS NOT NULL
  UNION ALL
  SELECT tp.id, e.id, 'alias_normalized_name', 2
  FROM l1_participants tp JOIN entity_aliases ea ON ea.normalized_alias = tp.participant_normalized
  JOIN entities e ON e.id = ea.entity_id
  WHERE tp.participant_normalized IS NOT NULL
), best_priority AS (
  SELECT participant_id, min(priority) priority
  FROM entity_candidates GROUP BY participant_id
), entity_resolution AS (
  SELECT c.participant_id, count(DISTINCT c.entity_id) candidate_count,
         CASE WHEN count(DISTINCT c.entity_id) = 1 THEN (array_agg(DISTINCT c.entity_id))[1] END mapped_entity_id,
         array_agg(DISTINCT c.match_method) match_methods
  FROM entity_candidates c JOIN best_priority bp ON bp.participant_id = c.participant_id AND bp.priority = c.priority
  GROUP BY c.participant_id
), base AS (
  SELECT tp.id participant_id, tp.tender_id, tp.bid_number, tp.participant_name, tp.participant_normalized,
         tp.financial_rank, tp.financial_status, tp.financial_remarks, tp.financial_bid_value,
         tp.financial_status_updated_on, tp.is_winner, t.tender_id external_tender_id, t.organization,
         t.website_url, t.lifecycle_stage, m.title tender_title, m.ref_no, m.category, m.product_category,
         m.location, m.tender_value, m.awardee_name, m.awardee_normalized, m.winning_bid_number,
         m.awarded_value, m.contract_date, m.financial_bid_opening_actual_date, m.financial_evaluation_date,
         m.aoc_date, coalesce(m.financial_evaluation_date, tp.financial_status_updated_on) l1_identified_at,
         er.candidate_count, er.mapped_entity_id, er.match_methods, e.canonical_name listed_company_name,
         e.normalized_name entity_normalized_name, e.ticker_nse, e.ticker_bse, e.isin, e.exchange,
         e.exchange_status, e.sector, e.industry, e.is_listed, lem.snapshot_date latest_market_snapshot_date,
         lem.current_price latest_stock_price, lem.market_cap latest_market_cap, lef.fiscal_year,
         lef.revenue, lef.ebitda, lef.net_profit, lef.net_worth
  FROM l1_participants tp JOIN tenders t ON t.id = tp.tender_id
  LEFT JOIN tender_extracted_meta m ON m.tender_id = tp.tender_id
  LEFT JOIN entity_resolution er ON er.participant_id = tp.id
  LEFT JOIN entities e ON e.id = er.mapped_entity_id
  LEFT JOIN latest_entity_market_data lem ON lem.entity_id = e.id
  LEFT JOIN latest_entity_financials lef ON lef.entity_id = e.id
), numeric_values AS (
  SELECT b.*,
    CASE WHEN regexp_replace(coalesce(b.financial_bid_value, ''), '[^0-9.-]', '', 'g') ~ '^-?[0-9]+(\.[0-9]+)?$'
      THEN regexp_replace(b.financial_bid_value, '[^0-9.-]', '', 'g')::numeric END l1_bid_value_numeric,
    CASE WHEN regexp_replace(coalesce(b.awarded_value, ''), '[^0-9.-]', '', 'g') ~ '^-?[0-9]+(\.[0-9]+)?$'
      THEN regexp_replace(b.awarded_value, '[^0-9.-]', '', 'g')::numeric END awarded_value_numeric
  FROM base b
), winner_evidence AS (
  SELECT n.*,
    CASE
      WHEN nullif(trim(n.winning_bid_number), '') IS NOT NULL THEN
        CASE WHEN regexp_replace(upper(trim(n.winning_bid_number)), '[^A-Z0-9]', '', 'g') = regexp_replace(upper(trim(n.bid_number)), '[^A-Z0-9]', '', 'g') THEN 'BID_NUMBER_MATCH' ELSE 'BID_NUMBER_MISMATCH' END
      WHEN nullif(trim(n.awardee_normalized), '') IS NOT NULL THEN
        CASE WHEN n.awardee_normalized = n.participant_normalized THEN 'AWARDEE_NAME_MATCH' ELSE 'AWARDEE_NAME_MISMATCH' END
      WHEN n.is_winner THEN 'WINNER_FLAG_ONLY'
      ELSE 'NO_WINNER_EVIDENCE'
    END winner_evidence
  FROM numeric_values n
), classified AS (
  SELECT w.*,
    CASE
      WHEN w.winner_evidence IN ('BID_NUMBER_MATCH', 'AWARDEE_NAME_MATCH') AND w.aoc_date IS NOT NULL THEN 'AOC_CONFIRMED'
      WHEN w.winner_evidence IN ('BID_NUMBER_MATCH', 'AWARDEE_NAME_MATCH') THEN 'L1_SELECTED_AOC_PENDING'
      WHEN w.winner_evidence IN ('BID_NUMBER_MISMATCH', 'AWARDEE_NAME_MISMATCH') THEN 'L1_NOT_SELECTED'
      WHEN w.winner_evidence = 'NO_WINNER_EVIDENCE' THEN 'L1_AWAITING_DECISION'
      ELSE 'OUTCOME_UNVERIFIED'
    END outcome_classification,
    CASE WHEN coalesce(w.candidate_count, 0) = 0 THEN 'UNRESOLVED' WHEN w.candidate_count > 1 THEN 'AMBIGUOUS' ELSE 'MATCHED' END entity_mapping_status
  FROM winner_evidence w
)
SELECT c.*, CASE WHEN c.aoc_date IS NOT NULL AND c.financial_evaluation_date IS NOT NULL THEN c.aoc_date::date - c.financial_evaluation_date::date END days_financial_eval_to_aoc
FROM classified c;

CREATE TABLE IF NOT EXISTS public.tender_announcement_matches (
  id BIGSERIAL PRIMARY KEY,
  tender_id INTEGER NOT NULL REFERENCES tenders(id),
  entity_id UUID NOT NULL REFERENCES entities(id),
  announcement_id BIGINT NOT NULL REFERENCES announcements_raw(id),
  match_status TEXT NOT NULL DEFAULT 'CANDIDATE' CHECK (match_status IN ('CANDIDATE', 'CONFIRMED', 'REJECTED')),
  match_method TEXT,
  review_notes TEXT,
  reviewed_by INTEGER REFERENCES auth_user(id),
  reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now(),
  UNIQUE (tender_id, announcement_id)
);
CREATE INDEX IF NOT EXISTS idx_tender_announcement_entity ON public.tender_announcement_matches(entity_id);
CREATE INDEX IF NOT EXISTS idx_tender_announcement_status ON public.tender_announcement_matches(match_status);

CREATE TABLE IF NOT EXISTS public.market_daily_prices (
  instrument_type TEXT NOT NULL CHECK (instrument_type IN ('EQUITY', 'INDEX')),
  instrument_code TEXT NOT NULL,
  entity_id UUID REFERENCES entities(id),
  exchange TEXT NOT NULL,
  trading_date DATE NOT NULL,
  open NUMERIC(20,6), high NUMERIC(20,6), low NUMERIC(20,6), close NUMERIC(20,6), adjusted_close NUMERIC(20,6), volume BIGINT,
  source TEXT NOT NULL, source_symbol TEXT, created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (instrument_type, instrument_code, exchange, trading_date),
  CHECK (instrument_type <> 'EQUITY' OR entity_id IS NOT NULL),
  CHECK (instrument_type <> 'INDEX' OR entity_id IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_market_prices_entity_date ON public.market_daily_prices(entity_id, trading_date) WHERE entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_market_prices_instrument_date ON public.market_daily_prices(instrument_type, instrument_code, exchange, trading_date);
