package srv

import (
	"net/http"
	"strings"

	"vyuha/api/internal/repo"
	"vyuha/api/internal/sqlf"
)

const materialityCompanyBase = `
WITH company_rows AS (
  SELECT * FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id = $1
), tender_rollup AS (
  SELECT tender_id, max(external_tender_id) external_tender_id, max(tender_title) tender_title,
    max(organization) organization, max(ref_no) ref_no, max(category) category,
    max(product_category) product_category, max(location) location,
    max(l1_identified_at) l1_identified_at, max(financial_evaluation_date) financial_evaluation_date,
    max(winner_evidence) winner_evidence,
    CASE WHEN bool_or(outcome_classification = 'AOC_CONFIRMED') THEN 'AOC_CONFIRMED'
      WHEN bool_or(outcome_classification = 'L1_SELECTED_AOC_PENDING') THEN 'L1_SELECTED_AOC_PENDING'
      WHEN bool_or(outcome_classification = 'L1_NOT_SELECTED') THEN 'L1_NOT_SELECTED'
      WHEN bool_or(outcome_classification = 'L1_AWAITING_DECISION') THEN 'L1_AWAITING_DECISION'
      ELSE 'OUTCOME_UNVERIFIED' END outcome_classification,
    sum(l1_bid_value_numeric) l1_bid_value,
    max(awarded_value_numeric) awarded_value,
    max(aoc_date) aoc_date,
    count(*) l1_positions
  FROM company_rows GROUP BY tender_id
), company AS (
  SELECT e.id entity_id, e.canonical_name, e.ticker_nse, e.ticker_bse, e.isin, e.sector, e.industry,
    e.exchange_status, lf.fiscal_year, lf.source_date financial_source_date, lf.revenue, lf.ebitda,
    lf.net_profit, lf.net_worth, lm.snapshot_date market_snapshot_date, lm.market_cap, lm.current_price,
    coalesce((SELECT sum(l1_bid_value) FROM tender_rollup), 0) l1_bid_exposure,
    coalesce((SELECT sum(awarded_value) FROM tender_rollup WHERE outcome_classification = 'AOC_CONFIRMED'), 0) confirmed_award_exposure,
    (SELECT count(*) FROM tender_rollup) l1_tender_count,
    (SELECT count(*) FROM tender_rollup WHERE outcome_classification = 'AOC_CONFIRMED') confirmed_aoc_count,
    (SELECT count(*) FROM tender_rollup WHERE outcome_classification = 'L1_SELECTED_AOC_PENDING') pending_award_count,
    (SELECT count(*) FROM tender_rollup WHERE outcome_classification = 'L1_NOT_SELECTED') not_selected_count
  FROM entities e LEFT JOIN latest_entity_financials lf ON lf.entity_id=e.id
  LEFT JOIN latest_entity_market_data lm ON lm.entity_id=e.id WHERE e.id=$1
)
SELECT * FROM company`

func (s *Server) MaterialityCompany(w http.ResponseWriter, r *http.Request) {
	entityID := strings.TrimSpace(r.PathValue("entityId"))
	base, err := s.QueryOne(r.Context(), materialityCompanyBase, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	if base == nil {
		JSON(w, http.StatusNotFound, map[string]any{"error": "company not found"})
		return
	}

	series, err := s.Query(r.Context(), `WITH tender_rollup AS (
		SELECT tender_id, date_trunc('month', max(l1_identified_at))::date month_bucket,
			sum(l1_bid_value_numeric) l1_bid_value, max(awarded_value_numeric) awarded_value,
			max(outcome_classification) FILTER (WHERE outcome_classification='AOC_CONFIRMED') confirmed_marker
		FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id=$1 GROUP BY tender_id)
		SELECT month_bucket, sum(l1_bid_value) l1_bid_value,
		coalesce(sum(awarded_value) FILTER (WHERE confirmed_marker='AOC_CONFIRMED'),0) confirmed_awarded_value,
		count(*) tender_count, count(*) FILTER (WHERE confirmed_marker='AOC_CONFIRMED') confirmed_aocs
		FROM tender_rollup WHERE month_bucket IS NOT NULL GROUP BY 1 ORDER BY 1`, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	organizations, err := s.Query(r.Context(), `WITH tender_rollup AS (
		SELECT tender_id, max(organization) organization, sum(l1_bid_value_numeric) l1_bid_value, max(awarded_value_numeric) awarded_value,
			max(outcome_classification) FILTER (WHERE outcome_classification='AOC_CONFIRMED') confirmed_marker
		FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id=$1 GROUP BY tender_id)
		SELECT coalesce(organization,'Unknown') organization, count(*) tender_count, sum(l1_bid_value) l1_bid_value,
		coalesce(sum(awarded_value) FILTER (WHERE confirmed_marker='AOC_CONFIRMED'),0) confirmed_awarded_value,
		count(*) FILTER (WHERE confirmed_marker='AOC_CONFIRMED') confirmed_aoc_count
		FROM tender_rollup GROUP BY 1 ORDER BY l1_bid_value DESC NULLS LAST LIMIT 20`, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	categories, err := s.Query(r.Context(), `WITH tender_rollup AS (
		SELECT tender_id, max(coalesce(category,'Uncategorised')) category, max(coalesce(product_category,'Uncategorised')) product_category,
			sum(l1_bid_value_numeric) l1_bid_value, max(awarded_value_numeric) awarded_value,
			max(outcome_classification) FILTER (WHERE outcome_classification='AOC_CONFIRMED') confirmed_marker
		FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id=$1 GROUP BY tender_id)
		SELECT category, product_category, count(*) tender_count, sum(l1_bid_value) l1_bid_value,
		coalesce(sum(awarded_value) FILTER (WHERE confirmed_marker='AOC_CONFIRMED'),0) confirmed_awarded_value
		FROM tender_rollup GROUP BY 1,2 ORDER BY l1_bid_value DESC NULLS LAST LIMIT 40`, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	tenders, err := s.Query(r.Context(), `WITH tender_rollup AS (
		SELECT tender_id, max(external_tender_id) external_tender_id, max(tender_title) tender_title, max(organization) organization,
		max(ref_no) ref_no, max(category) category, max(location) location, max(l1_identified_at) l1_identified_at,
		max(financial_evaluation_date) financial_evaluation_date, max(winner_evidence) winner_evidence,
		CASE WHEN bool_or(outcome_classification='AOC_CONFIRMED') THEN 'AOC_CONFIRMED' WHEN bool_or(outcome_classification='L1_SELECTED_AOC_PENDING') THEN 'L1_SELECTED_AOC_PENDING' WHEN bool_or(outcome_classification='L1_NOT_SELECTED') THEN 'L1_NOT_SELECTED' WHEN bool_or(outcome_classification='L1_AWAITING_DECISION') THEN 'L1_AWAITING_DECISION' ELSE 'OUTCOME_UNVERIFIED' END outcome_classification,
		sum(l1_bid_value_numeric) l1_bid_value, max(awarded_value_numeric) awarded_value, max(aoc_date) aoc_date
		FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id=$1 GROUP BY tender_id)
		SELECT * FROM tender_rollup ORDER BY l1_identified_at DESC NULLS LAST`, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"company": base, "series": series, "organizations": organizations, "categories": categories, "tenders": tenders})
}

// Materiality serves the Financial Materiality tab as a tender-level
// financial-evaluation register. It intentionally returns one row per tender,
// rather than one row per mapped company or participant.
func (s *Server) Materiality(w http.ResponseWriter, r *http.Request) {
	q := &repo.QB{}
	q.W(`WITH latest_meta AS (
		SELECT DISTINCT ON (tender_id) *
		FROM tender_extracted_meta
		ORDER BY tender_id, updated_at DESC NULLS LAST, id DESC
	), mapped_matches AS (
		SELECT DISTINCT tp.id AS participant_id
		FROM tender_participants tp
		JOIN entities e ON e.normalized_name = tp.participant_normalized
		WHERE coalesce(tp.participant_normalized, '') <> ''
		UNION
		SELECT DISTINCT tp.id
		FROM tender_participants tp
		JOIN entity_aliases ea ON ea.normalized_alias = tp.participant_normalized
		JOIN entities e ON e.id = ea.entity_id
		WHERE coalesce(tp.participant_normalized, '') <> ''
		UNION
		SELECT DISTINCT tp.id AS participant_id
		FROM tender_participants tp
		JOIN entities e ON regexp_replace(upper(trim(coalesce(e.canonical_name, ''))), '[^A-Z0-9]', '', 'g') = tp.participant_normalized
		WHERE coalesce(tp.participant_normalized, '') <> ''
		UNION
		SELECT DISTINCT tp.id
		FROM tender_participants tp
		JOIN entity_gst eg ON regexp_replace(upper(trim(coalesce(eg.company_name, ''))), '[^A-Z0-9]', '', 'g') = tp.participant_normalized
		WHERE coalesce(tp.participant_normalized, '') <> ''
	), evaluated AS (
		SELECT
			t.id AS tender_internal_id,
			t.tender_id AS external_tender_id,
			t.organization,
			t.lifecycle_stage,
			m.title,
			m.ref_no,
			m.category,
			m.product_category,
			m.location,
			m.financial_evaluation_date,
			m.tender_value AS tender_value_raw,
			` + sqlf.NumValExpr("m.tender_value") + ` AS tender_value,
			COALESCE(m.financial_evaluation_date, MAX(tp.financial_status_updated_on)) AS evaluation_date,
			COUNT(tp.id) AS participant_count,
			COUNT(DISTINCT mm.participant_id) AS mapped_participant_count,
			(array_agg(DISTINCT tp.participant_name ORDER BY tp.participant_name) FILTER (WHERE mm.participant_id IS NOT NULL)) AS mapped_participants,
			(array_agg(tp.participant_name ORDER BY tp.id) FILTER (WHERE regexp_replace(upper(trim(coalesce(tp.financial_rank, ''))), '[^A-Z0-9]', '', 'g') IN ('1', 'L1')))[1] AS l1_participant,
			(array_agg(tp.financial_rank ORDER BY tp.id) FILTER (WHERE regexp_replace(upper(trim(coalesce(tp.financial_rank, ''))), '[^A-Z0-9]', '', 'g') IN ('1', 'L1')))[1] AS l1_rank,
			(array_agg(tp.financial_bid_value ORDER BY tp.id) FILTER (WHERE regexp_replace(upper(trim(coalesce(tp.financial_rank, ''))), '[^A-Z0-9]', '', 'g') IN ('1', 'L1')))[1] AS l1_bid_value_raw,
			(array_agg(CASE WHEN regexp_replace(coalesce(tp.financial_bid_value, ''), '[^0-9.]', '', 'g') ~ '^[0-9]+(\\.[0-9]+)?' THEN regexp_replace(tp.financial_bid_value, '[^0-9.]', '', 'g')::numeric END ORDER BY tp.id) FILTER (WHERE regexp_replace(upper(trim(coalesce(tp.financial_rank, ''))), '[^A-Z0-9]', '', 'g') IN ('1', 'L1')))[1] AS l1_bid_value
		FROM tenders t
		JOIN tender_participants tp ON tp.tender_id = t.id
		LEFT JOIN latest_meta m ON m.tender_id = t.id
		LEFT JOIN mapped_matches mm ON mm.participant_id = tp.id
		GROUP BY t.id, t.tender_id, t.organization, t.lifecycle_stage, m.title, m.ref_no, m.category, m.product_category, m.location, m.financial_evaluation_date, m.tender_value
	)
	SELECT *, COUNT(*) OVER () AS total_rows FROM evaluated`)

	var conditions []string
	if query := strings.TrimSpace(qp(r, "q", "")); query != "" {
		p := ph(q, "%"+lower(query)+"%")
		conditions = append(conditions, "(lower(coalesce(external_tender_id, '')) LIKE "+p+" OR lower(coalesce(title, '')) LIKE "+p+" OR lower(coalesce(organization, '')) LIKE "+p+" OR lower(coalesce(ref_no, '')) LIKE "+p+")")
	}
	conditions = append(conditions, "evaluation_date IS NOT NULL")
	if mapped := strings.ToLower(strings.TrimSpace(qp(r, "mapped_participants", ""))); mapped == "true" || mapped == "mapped" {
		conditions = append(conditions, "mapped_participant_count > 0")
	} else if mapped == "false" || mapped == "unmapped" {
		conditions = append(conditions, "mapped_participant_count = 0")
	}
	q.W(" WHERE " + strings.Join(conditions, " AND "))

	sortExpr := "evaluation_date"
	switch qp(r, "sort", "evaluation") {
	case "value":
		sortExpr = "tender_value"
	case "bid":
		sortExpr = "l1_bid_value"
	case "title":
		sortExpr = "title"
	}
	direction := "DESC"
	if strings.EqualFold(qp(r, "dir", "desc"), "asc") {
		direction = "ASC"
	}
	q.W(" ORDER BY " + sortExpr + " " + direction + " NULLS LAST, tender_internal_id DESC")
	lim, off := paging(r)
	q.W(" LIMIT " + ph(q, lim) + " OFFSET " + ph(q, off))
	rows, err := s.Query(r.Context(), q.SQL(), q.Args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, listResp(rows, totalFrom(rows), lim, off))
}
