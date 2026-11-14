package srv

import (
	"net/http"
	"strconv"
	"strings"

	"vyuha/api/internal/repo"
)

// ph allocates a placeholder for value v against qb (args append in order).
func ph(q *repo.QB, v any) string {
	q.Args = append(q.Args, v)
	return "$" + strconv.Itoa(len(q.Args))
}

func known(value string, allowed ...string) string {
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	return ""
}

func sendErr(w http.ResponseWriter, err error) {
	httpLog(err)
	JSON(w, http.StatusInternalServerError, map[string]any{"error": "internal query error"})
}

// ───────────────────────── Financial Evaluation ─────────────────────────

// feWhere builds filter conditions over the projected participant rows.
func feWhere(r *http.Request, q *repo.QB) string {
	var conds []string
	if txt := strings.TrimSpace(qp(r, "q", "")); txt != "" {
		p := ph(q, "%"+lower(txt)+"%")
		conds = append(conds, "(participant_name ILIKE "+p+" OR participant_normalized ILIKE "+p+
			" OR canonical_name ILIKE "+p+" OR external_tender_id ILIKE "+p+" OR title ILIKE "+p+")")
	}
	if st := known(lower(qp(r, "mapping_status", "")), "mapped", "ambiguous", "unresolved"); st != "" {
		conds = append(conds, "mapping_status = "+ph(q, st))
	}
	if m := known(lower(qp(r, "method", "")), "direct_normalized_match", "alias_match", "canonical_name_match", "gst_company_name_match", "gst_contractor_match"); m != "" {
		conds = append(conds, "mapping_method = "+ph(q, m))
	}
	if lc := strings.TrimSpace(qp(r, "lifecycle", "")); lc != "" {
		conds = append(conds, "lifecycle_stage = "+ph(q, lc))
	}
	if rk := strings.TrimSpace(qp(r, "rank", "")); rk != "" {
		conds = append(conds, "financial_rank ILIKE "+ph(q, rk+"%"))
	}
	switch lower(qp(r, "winner", "")) {
	case "true":
		conds = append(conds, "is_winner = TRUE")
	case "false":
		conds = append(conds, "is_winner = FALSE")
	}
	if ent := strings.TrimSpace(qp(r, "entity_id", "")); ent != "" {
		conds = append(conds, "entity_id::text = "+ph(q, ent))
	}
	if t := strings.TrimSpace(qp(r, "tender_id", "")); t != "" {
		conds = append(conds, "external_tender_id = "+ph(q, t))
	}
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

const feListSelect = ` SELECT *, COUNT(*) OVER () AS total_rows FROM prow`

// ListFE serves GET /api/v1/financial-evaluation and /mappings/financial-evaluation.
func (s *Server) ListFE(w http.ResponseWriter, r *http.Request) {
	q := &repo.QB{}
	q.W(repo.ParticipantCore())
	q.W(feListSelect)
	q.W(feWhere(r, q))
	ord := repo.OrderBy(qp(r, "sort", "submitted"), qp(r, "dir", "asc"), repo.ParticipantSorts,
		"submitted_date DESC NULLS LAST, participant_id DESC")
	q.W(" ORDER BY " + ord)
	lim, off := paging(r)
	q.W(" LIMIT " + ph(q, lim) + " OFFSET " + ph(q, off))

	rows, err := s.Query(r.Context(), q.SQL(), q.Args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	total := totalFrom(rows)
	JSON(w, http.StatusOK, listResp(rows, total, lim, off))
}

func totalFrom(rows []map[string]any) int {
	if len(rows) > 0 {
		if t, ok := rows[0]["total_rows"].(float64); ok {
			return int(t)
		}
	}
	return len(rows)
}

func listResp(rows []map[string]any, total, lim, off int) map[string]any {
	return map[string]any{"data": rows, "total": total, "limit": lim, "offset": off}
}

// SummaryFE serves GET /api/v1/financial-evaluation/summary.
func (s *Server) SummaryFE(w http.ResponseWriter, r *http.Request) {
	s.SummaryFull(w, r)
}

// SummaryFull returns status/method/month/rank breakdowns in one response.
func (s *Server) SummaryFull(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	byStatus, err := s.Query(ctx, repo.ParticipantCore()+" SELECT mapping_status, COUNT(*) participant_records,"+
		" COUNT(DISTINCT participant_name) unique_participant_names, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COALESCE(SUM(bid_value),0) bid_value_total FROM prow GROUP BY mapping_status ORDER BY participant_records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	byMethod, err := s.Query(ctx, repo.ParticipantCore()+" SELECT mapping_method, COUNT(*) mapped_records,"+
		" COUNT(DISTINCT entity_id) unique_entities, COALESCE(SUM(bid_value),0) bid_value_total FROM prow"+
		" WHERE mapping_status='mapped' GROUP BY mapping_method ORDER BY mapped_records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	byMonth, err := s.Query(ctx, repo.ParticipantCore()+" SELECT date_trunc('month', COALESCE(submitted_date, record_created_at)) AS month_bucket,"+
		" COUNT(*) participant_records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(*) FILTER (WHERE is_winner) winners,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped"+
		" FROM prow GROUP BY 1 ORDER BY 1 NULLS LAST")
	if err != nil {
		sendErr(w, err)
		return
	}
	byRank, err := s.Query(ctx, repo.ParticipantCore()+" SELECT COALESCE(NULLIF(TRIM(financial_rank),''),'(not ranked)') financial_rank,"+
		" COUNT(*) records FROM prow GROUP BY 1 ORDER BY records DESC LIMIT 20")
	if err != nil {
		sendErr(w, err)
		return
	}
	totals, err := s.QueryOne(ctx, repo.ParticipantCore()+
		" SELECT COUNT(*) records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(DISTINCT participant_name) unique_names,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped' AND is_listed) mapped_listed_records,"+
		" COUNT(DISTINCT entity_id) FILTER (WHERE mapping_status='mapped') unique_mapped_entities,"+
		" COUNT(*) FILTER (WHERE mapping_status='ambiguous') ambiguous_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='unresolved') unresolved_records,"+
		" COUNT(DISTINCT participant_name) FILTER (WHERE mapping_status='unresolved') unresolved_names,"+
		" COUNT(*) FILTER (WHERE mapping_method='gst_company_name_match') gst_mapped_records,"+
		" COUNT(*) FILTER (WHERE mapping_method='alias_match') alias_mapped_records,"+
		" COALESCE(SUM(bid_value) FILTER (WHERE mapping_status='mapped'),0) mapped_bid_value_total"+
		" FROM prow")
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"totals":                totals,
		"by_mapping_status":     byStatus,
		"by_mapping_method":     byMethod,
		"participants_by_month": byMonth,
		"rank_distribution":     byRank,
	})
}

// LeaderboardFE serves GET /api/v1/financial-evaluation/leaderboard (Query J).
func (s *Server) LeaderboardFE(w http.ResponseWriter, r *http.Request) {
	q := &repo.QB{}
	q.W(repo.ParticipantCore())
	q.W(` SELECT entity_id, canonical_name, ticker_nse, ticker_bse, isin, sector,
        COUNT(*) participant_records,
        COUNT(DISTINCT tender_id) unique_tenders,
        COUNT(*) FILTER (WHERE regexp_replace(COALESCE(financial_rank,''),'[^0-9]','','g') = '1'
                         AND COALESCE(financial_rank,'') <> '') l1_appearances,
        COUNT(*) FILTER (WHERE regexp_replace(COALESCE(financial_rank,''),'[^0-9]','','g') = '2'
                         AND COALESCE(financial_rank,'') <> '') l2_appearances,
        COUNT(*) FILTER (WHERE is_winner) winner_designated_records,
        COALESCE(SUM(bid_value),0) total_bid_value,
        MAX(bid_value) max_bid_value,
        COUNT(*) OVER () total_rows
      FROM prow
      WHERE mapping_status = 'mapped' AND is_listed = TRUE`)
	if txt := strings.TrimSpace(qp(r, "q", "")); txt != "" {
		p := ph(q, "%"+lower(txt)+"%")
		q.W(" AND (canonical_name ILIKE " + p + " OR ticker_nse ILIKE " + p + " OR ticker_bse ILIKE " + p + ")")
	}
	q.W(" GROUP BY entity_id, canonical_name, ticker_nse, ticker_bse, isin, sector")
	ord := repo.OrderBy(qp(r, "sort", "tenders"), qp(r, "dir", "desc"), repo.ParticipantLeaderboardSorts,
		"unique_tenders DESC, l1_appearances DESC, total_bid_value DESC, canonical_name")
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

// UnresolvedNames serves GET /api/v1/mappings/unresolved (Query H).
func (s *Server) UnresolvedNames(w http.ResponseWriter, r *http.Request) {
	dataset := lower(qp(r, "dataset", "all"))
	ctx := r.Context()
	limit, off := paging(r)

	var fe, aw map[string]any
	resp := map[string]any{}

	if dataset == "all" || dataset == "fe" {
		rows, err := s.Query(ctx, repo.ParticipantCore()+`
		 SELECT participant_name, participant_normalized,
		        COUNT(*) appearances, COUNT(DISTINCT tender_id) tender_count,
		        ARRAY_AGG(DISTINCT financial_rank) financial_ranks,
		        STRING_AGG(DISTINCT financial_status, ', ') financial_statuses,
		        COUNT(*) OVER () total_rows
		 FROM prow WHERE mapping_status = 'unresolved'
		 GROUP BY participant_name, participant_normalized
		 ORDER BY appearances DESC, participant_name
		 LIMIT `+limitArg(0)+``, limit, off)
		if err != nil {
			sendErr(w, err)
			return
		}
		fe = listResp(rows, totalFrom(rows), limit, off)
		resp["financial_evaluation"] = fe
	}
	if dataset == "all" || dataset == "awards" {
		rows, err := s.Query(ctx, repo.AwardCore()+`
		 SELECT awardee_name, awardee_normalized,
		        COUNT(*) appearances, COUNT(DISTINCT tender_id) tender_count,
		        STRING_AGG(DISTINCT buyer_org, ' | ') FILTER (WHERE buyer_org IS NOT NULL) buyers,
		        COALESCE(SUM(awarded_value),0) awarded_value_total,
		        COUNT(*) OVER () total_rows
		 FROM arows WHERE mapping_status = 'unresolved'
		 GROUP BY awardee_name, awardee_normalized
		 ORDER BY appearances DESC, awardee_name
		 LIMIT `+limitArg(0)+``, limit, off)
		if err != nil {
			sendErr(w, err)
			return
		}
		aw = listResp(rows, totalFrom(rows), limit, off)
		resp["awards"] = aw
	}
	JSON(w, http.StatusOK, resp)
}

// Ambiguous serves GET /api/v1/mappings/ambiguous (Query I).
func (s *Server) Ambiguous(w http.ResponseWriter, r *http.Request) {
	dataset := lower(qp(r, "dataset", "all"))
	ctx := r.Context()
	limit, off := paging(r)
	resp := map[string]any{}

	if dataset == "all" || dataset == "fe" {
		rows, err := s.Query(ctx, repo.ParticipantCore()+`
		 SELECT participant_name, participant_normalized, tender_id, external_tender_id,
		        financial_rank, financial_status,
		        candidate_count, candidate_names, mapping_method
		 FROM prow WHERE mapping_status = 'ambiguous'
		 ORDER BY candidate_count DESC, participant_name
		 LIMIT `+limitArg(0), limit, off)
		if err != nil {
			sendErr(w, err)
			return
		}
		resp["financial_evaluation"] = listResp(rows, totalFrom(rows), limit, off)
	}
	if dataset == "all" || dataset == "awards" {
		rows, err := s.Query(ctx, repo.AwardCore()+`
		 SELECT awardee_name, awardee_normalized, tender_id, external_tender_id,
		        awarded_value, buyer_org,
		        candidate_count, candidate_names, mapping_method
		 FROM arows WHERE mapping_status = 'ambiguous'
		 ORDER BY candidate_count DESC, awardee_name
		 LIMIT `+limitArg(0), limit, off)
		if err != nil {
			sendErr(w, err)
			return
		}
		resp["awards"] = listResp(rows, totalFrom(rows), limit, off)
	}
	JSON(w, http.StatusOK, resp)
}

func limitArg(_ int) string { return "$1 OFFSET $2" }
