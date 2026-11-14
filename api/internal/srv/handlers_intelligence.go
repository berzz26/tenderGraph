package srv

import (
	"net/http"
	"strconv"
	"strings"
)

const intelligenceSummarySQL = `
SELECT
  count(DISTINCT participant_id) total_l1_positions,
  count(DISTINCT participant_id) FILTER (WHERE outcome_classification = 'L1_AWAITING_DECISION') awaiting_decision,
  count(DISTINCT participant_id) FILTER (WHERE outcome_classification = 'L1_SELECTED_AOC_PENDING') selected_aoc_pending,
  count(DISTINCT participant_id) FILTER (WHERE outcome_classification = 'AOC_CONFIRMED') confirmed_aocs,
  count(DISTINCT participant_id) FILTER (WHERE outcome_classification = 'L1_NOT_SELECTED') not_selected,
  count(DISTINCT participant_id) FILTER (WHERE entity_mapping_status = 'MATCHED' AND is_listed) mapped_listed_positions,
  count(DISTINCT participant_id) FILTER (WHERE entity_mapping_status = 'UNRESOLVED') unresolved_positions,
  coalesce(sum(l1_bid_value_numeric), 0) total_l1_bid_value,
  coalesce(sum(awarded_value_numeric) FILTER (WHERE outcome_classification = 'AOC_CONFIRMED'), 0) confirmed_awarded_value
FROM public.vw_vyuha_l1_intelligence`

func (s *Server) IntelligenceSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kpis, err := s.QueryOne(ctx, intelligenceSummarySQL)
	if err != nil {
		sendErr(w, err)
		return
	}
	outcomes, err := s.Query(ctx, `SELECT outcome_classification, count(*) position_count, coalesce(sum(l1_bid_value_numeric),0) total_l1_bid_value, count(DISTINCT mapped_entity_id) company_count FROM public.vw_vyuha_l1_intelligence GROUP BY 1 ORDER BY position_count DESC`)
	if err != nil {
		sendErr(w, err)
		return
	}
	monthly, err := s.Query(ctx, `SELECT date_trunc('month', l1_identified_at)::date month_bucket, count(*) l1_positions, count(*) FILTER (WHERE is_listed) listed_company_positions, coalesce(sum(l1_bid_value_numeric),0) total_l1_bid_value FROM public.vw_vyuha_l1_intelligence WHERE l1_identified_at IS NOT NULL GROUP BY 1 ORDER BY 1`)
	if err != nil {
		sendErr(w, err)
		return
	}
	topCompanies, err := s.Query(ctx, `SELECT mapped_entity_id, listed_company_name, ticker_nse, count(*) l1_positions, coalesce(sum(l1_bid_value_numeric),0) l1_bid_value FROM public.vw_vyuha_l1_intelligence WHERE mapped_entity_id IS NOT NULL AND is_listed GROUP BY 1,2,3 ORDER BY l1_bid_value DESC NULLS LAST LIMIT 10`)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"kpis": kpis, "outcomes": outcomes, "monthly": monthly, "top_companies": topCompanies})
}

func (s *Server) IntelligenceL1(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	q := strings.TrimSpace(qp(r, "q", ""))
	args := []any{}
	where := []string{"1=1"}
	if q != "" {
		args = append(args, "%"+q+"%")
		where = append(where, "(tender_title ILIKE $1 OR participant_name ILIKE $1 OR listed_company_name ILIKE $1 OR external_tender_id ILIKE $1)")
	}
	if status := qp(r, "outcome", ""); status != "" {
		args = append(args, status)
		where = append(where, "outcome_classification = $"+strconv.Itoa(len(args)))
	}
	if mapping := qp(r, "mapping", ""); mapping != "" {
		args = append(args, mapping)
		where = append(where, "entity_mapping_status = $"+strconv.Itoa(len(args)))
	}
	args = append(args, limit, offset)
	rows, err := s.Query(r.Context(), `SELECT * FROM public.vw_vyuha_l1_intelligence WHERE `+strings.Join(where, " AND ")+` ORDER BY l1_identified_at DESC NULLS LAST, participant_id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	countArgs := args[:len(args)-2]
	count, err := s.QueryOne(r.Context(), `SELECT count(*) total FROM public.vw_vyuha_l1_intelligence WHERE `+strings.Join(where, " AND "), countArgs...)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"data": rows, "total": count["total"], "limit": limit, "offset": offset})
}

func (s *Server) IntelligenceAwards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	metrics, err := s.QueryOne(ctx, `SELECT
		count(*) FILTER (WHERE outcome_classification IN ('L1_SELECTED_AOC_PENDING','AOC_CONFIRMED')) selected_positions,
		count(*) FILTER (WHERE outcome_classification = 'L1_NOT_SELECTED') not_selected_positions,
		count(*) FILTER (WHERE outcome_classification = 'AOC_CONFIRMED') aoc_confirmed_positions,
		count(*) FILTER (WHERE outcome_classification = 'L1_SELECTED_AOC_PENDING') selected_pending_aoc,
		round(100.0 * count(*) FILTER (WHERE outcome_classification IN ('L1_SELECTED_AOC_PENDING','AOC_CONFIRMED')) / nullif(count(*) FILTER (WHERE outcome_classification IN ('L1_SELECTED_AOC_PENDING','AOC_CONFIRMED','L1_NOT_SELECTED')), 0), 2) winner_selection_rate_pct,
		round(100.0 * count(*) FILTER (WHERE outcome_classification = 'AOC_CONFIRMED') / nullif(count(*) FILTER (WHERE outcome_classification IN ('L1_SELECTED_AOC_PENDING','AOC_CONFIRMED')), 0), 2) aoc_completion_rate_pct,
		count(*) FILTER (WHERE outcome_classification IN ('L1_SELECTED_AOC_PENDING','AOC_CONFIRMED') AND l1_identified_at <= current_date - ($1::int * interval '1 day')) mature_selected_positions,
		count(*) FILTER (WHERE outcome_classification = 'L1_NOT_SELECTED' AND l1_identified_at <= current_date - ($1::int * interval '1 day')) mature_not_selected_positions
		FROM public.vw_vyuha_l1_intelligence`, qpInt(r, "maturity_days", 90, 730))
	if err != nil {
		sendErr(w, err)
		return
	}
	pending, err := s.Query(ctx, `SELECT participant_id, tender_id, external_tender_id, tender_title, organization, ref_no, participant_name, listed_company_name, ticker_nse, ticker_bse, financial_bid_value, l1_bid_value_numeric, l1_identified_at, financial_evaluation_date, awardee_name, winning_bid_number, contract_date, lifecycle_stage, current_date - coalesce(contract_date, financial_evaluation_date::date, l1_identified_at::date) days_since_last_known_milestone, winner_evidence FROM public.vw_vyuha_l1_intelligence WHERE outcome_classification = 'L1_SELECTED_AOC_PENDING' ORDER BY l1_identified_at ASC NULLS LAST LIMIT 200`)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"metrics": metrics, "maturity_days": qpInt(r, "maturity_days", 90, 730), "pending": pending})
}
