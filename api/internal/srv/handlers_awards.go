package srv

import (
	"net/http"
	"strings"

	"vyuha/api/internal/repo"
)

// awWhere builds filter conditions over the projected award rows.
func awWhere(r *http.Request, q *repo.QB) string {
	var conds []string
	if txt := strings.TrimSpace(qp(r, "q", "")); txt != "" {
		p := ph(q, "%"+lower(txt)+"%")
		conds = append(conds, "(awardee_name ILIKE "+p+" OR awardee_normalized ILIKE "+p+
			" OR canonical_name ILIKE "+p+" OR external_tender_id ILIKE "+p+" OR title ILIKE "+p+
			" OR buyer_org ILIKE "+p+")")
	}
	if st := known(lower(qp(r, "mapping_status", "")), "mapped", "ambiguous", "unresolved"); st != "" {
		conds = append(conds, "mapping_status = "+ph(q, st))
	}
	if m := known(lower(qp(r, "method", "")), "direct_normalized_match", "alias_match", "canonical_name_match", "gst_contractor_match", "gst_company_name_match"); m != "" {
		conds = append(conds, "mapping_method = "+ph(q, m))
	}
	if fy := strings.TrimSpace(qp(r, "fiscal_year", "")); fy != "" {
		conds = append(conds, "fiscal_year = "+ph(q, fy))
	}
	if bo := strings.TrimSpace(qp(r, "buyer_org", "")); bo != "" {
		conds = append(conds, "buyer_org ILIKE "+ph(q, bo+"%"))
	}
	if ent := strings.TrimSpace(qp(r, "entity_id", "")); ent != "" {
		conds = append(conds, "entity_id::text = "+ph(q, ent))
	}
	if lc := strings.TrimSpace(qp(r, "lifecycle", "")); lc != "" {
		conds = append(conds, "lifecycle_stage = "+ph(q, lc))
	}
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// ListAwards serves GET /api/v1/awards and /mappings/awards.
func (s *Server) ListAwards(w http.ResponseWriter, r *http.Request) {
	q := &repo.QB{}
	q.W(repo.AwardCore())
	q.W(` SELECT meta_id, tender_id, external_tender_id, lifecycle_stage, organization,
	             buyer_org, title, awardee_name, awardee_normalized, awarded_value,
	             contract_date, fiscal_year, winning_bid_number,
	             mapping_status, mapping_method, candidate_count,
	             entity_id, canonical_name, ticker_nse, ticker_bse, isin, is_listed, sector,
	             contractor_business_name, contractor_gstin, entity_gstins, candidate_names,
	             COUNT(*) OVER () AS total_rows
	      FROM arows`)
	q.W(awWhere(r, q))
	ord := repo.OrderBy(qp(r, "sort", "contract_date"), qp(r, "dir", "desc"), repo.AwardSorts,
		"contract_date DESC NULLS LAST, meta_id DESC")
	q.W(" ORDER BY " + ord)
	lim, off := paging(r)
	q.W(" LIMIT " + ph(q, lim) + " OFFSET " + ph(q, off))

	rows, err := s.Query(r.Context(), q.SQL(), q.Args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, listResp(rows, totalFrom(rows), lim, off))
}

// SummaryAwards serves GET /api/v1/awards/summary.
func (s *Server) SummaryAwards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	totals, err := s.QueryOne(ctx, repo.AwardCore()+
		" SELECT COUNT(*) records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(DISTINCT awardee_name) unique_awardee_names,"+
		" COUNT(DISTINCT buyer_org) unique_buyers,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped' AND is_listed) mapped_listed_records,"+
		" COUNT(DISTINCT entity_id) FILTER (WHERE mapping_status='mapped') unique_mapped_entities,"+
		" COUNT(*) FILTER (WHERE mapping_status='ambiguous') ambiguous_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='unresolved') unresolved_records,"+
		" COUNT(DISTINCT awardee_name) FILTER (WHERE mapping_status='unresolved') unresolved_names,"+
		" COALESCE(SUM(awarded_value) FILTER (WHERE mapping_status='mapped'),0) mapped_awarded_value_total,"+
		" COUNT(DISTINCT fiscal_year) fiscal_years"+
		" FROM arows")
	if err != nil {
		sendErr(w, err)
		return
	}
	byStatus, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_status, COUNT(*) award_records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(DISTINCT awardee_name) unique_awardee_names, COALESCE(SUM(awarded_value),0) awarded_value_total"+
		" FROM arows GROUP BY mapping_status ORDER BY award_records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	byMethod, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_method, COUNT(*) mapped_records, COUNT(DISTINCT entity_id) unique_entities,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total"+
		" FROM arows WHERE mapping_status='mapped' GROUP BY mapping_method ORDER BY mapped_records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	byFY, err := s.Query(ctx, repo.AwardCore()+
		" SELECT fiscal_year, COUNT(*) award_records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped_records"+
		" FROM arows GROUP BY fiscal_year ORDER BY fiscal_year")
	if err != nil {
		sendErr(w, err)
		return
	}
	byBuyer, err := s.Query(ctx, repo.AwardCore()+
		" SELECT buyer_org, COUNT(*) award_records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total"+
		" FROM arows GROUP BY buyer_org ORDER BY awarded_value_total DESC NULLS LAST LIMIT 15")
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"totals":            totals,
		"by_mapping_status": byStatus,
		"by_mapping_method": byMethod,
		"awarded_by_fy":     byFY,
		"top_buyers":        byBuyer,
	})
}

// LeaderboardAwards serves GET /api/v1/awards/leaderboard (Query N).
func (s *Server) LeaderboardAwards(w http.ResponseWriter, r *http.Request) {
	q := &repo.QB{}
	q.W(repo.AwardCore())
	q.W(` SELECT entity_id, canonical_name, ticker_nse, ticker_bse, fiscal_year,
	             COUNT(*) award_records,
	             COUNT(DISTINCT tender_id) unique_awarded_tenders,
	             COUNT(DISTINCT buyer_org) distinct_buyers,
	             COALESCE(SUM(awarded_value),0) total_awarded_value,
	             MAX(awarded_value) largest_contract_value,
	             (ARRAY_AGG(title ORDER BY awarded_value DESC NULLS LAST, meta_id))[1] largest_contract,
	             (ARRAY_AGG(buyer_org ORDER BY awarded_value DESC NULLS LAST, meta_id))[1] largest_contract_buyer,
	             COUNT(*) OVER () total_rows
	      FROM arows
	      WHERE mapping_status = 'mapped' AND is_listed = TRUE`)
	if txt := strings.TrimSpace(qp(r, "q", "")); txt != "" {
		p := ph(q, "%"+lower(txt)+"%")
		q.W(" AND (canonical_name ILIKE " + p + " OR ticker_nse ILIKE " + p + " OR ticker_bse ILIKE " + p + ")")
	}
	if fy := strings.TrimSpace(qp(r, "fiscal_year", "")); fy != "" {
		q.W(" AND fiscal_year = " + ph(q, fy))
	}
	q.W(" GROUP BY entity_id, canonical_name, ticker_nse, ticker_bse, fiscal_year")
	ord := repo.OrderBy(qp(r, "sort", "value"), qp(r, "dir", "desc"), repo.AwardLeaderboardSorts,
		"total_awarded_value DESC, unique_awarded_tenders DESC, canonical_name")
	q.W(" ORDER BY " + ord)
	lim, off := paging(r)
	q.W(" LIMIT " + ph(q, lim) + " OFFSET " + ph(q, off))

	rows, err := s.Query(r.Context(), q.SQL(), q.Args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, listResp(rows, totalFrom(rows), lim, off))
}
