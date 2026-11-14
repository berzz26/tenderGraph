package srv

import (
	"net/http"

	"strings"

	"vyuha/api/internal/repo"
)

// OverviewKpis serves GET /api/v1/overview/kpis.
func (s *Server) OverviewKpis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Financial-evaluation side.
	feTot, err := s.QueryOne(ctx, repo.ParticipantCore()+
		" SELECT COUNT(*) records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(DISTINCT participant_name) unique_names,"+
		" COUNT(DISTINCT entity_id) FILTER (WHERE mapping_status='mapped') mapped_entities,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped' AND is_listed) mapped_listed_records,"+
		" COUNT(*) FILTER (WHERE mapping_method='gst_company_name_match') gst_mappings,"+
		" COUNT(*) FILTER (WHERE mapping_method='alias_match') alias_mappings,"+
		" COUNT(DISTINCT participant_name) FILTER (WHERE mapping_status='unresolved') unresolved_names,"+
		" COUNT(*) FILTER (WHERE mapping_status='unresolved') unresolved_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='ambiguous') ambiguous_records,"+
		" COALESCE(SUM(bid_value),0) bid_value_total,"+
		" COUNT(*) FILTER (WHERE is_winner) winner_records"+
		" FROM prow")
	if err != nil {
		sendErr(w, err)
		return
	}

	// Awards side.
	awTot, err := s.QueryOne(ctx, repo.AwardCore()+
		" SELECT COUNT(*) award_records, COUNT(DISTINCT tender_id) unique_tenders,"+
		" COUNT(DISTINCT entity_id) FILTER (WHERE mapping_status='mapped') mapped_entities,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped' AND is_listed) mapped_listed_records,"+
		" COUNT(*) FILTER (WHERE mapping_status='ambiguous') ambiguous_records,"+
		" COUNT(DISTINCT awardee_name) FILTER (WHERE mapping_status='unresolved') unresolved_names,"+
		" COUNT(*) FILTER (WHERE mapping_method='gst_contractor_match') gst_contractor_mappings,"+
		" COUNT(*) FILTER (WHERE mapping_method='gst_company_name_match') gst_name_mappings,"+
		" COUNT(*) FILTER (WHERE mapping_method='alias_match') alias_mappings,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total,"+
		" COALESCE(SUM(awarded_value) FILTER (WHERE mapping_status='mapped'),0) mapped_awarded_value_total"+
		" FROM arows")
	if err != nil {
		sendErr(w, err)
		return
	}

	// Lifecycle counts across all tenders.
	lc, err := s.QueryOne(ctx,
		`SELECT COUNT(*) FILTER (WHERE lifecycle_stage='aoc') aoc_tenders,
	        COUNT(*) FILTER (WHERE lifecycle_stage='financial_evaluation') fe_tenders,
	        COUNT(*) FILTER (WHERE lifecycle_stage='open') open_tenders,
	        COUNT(*) total_tenders FROM tenders`)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Charts.
	feMonth, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT date_trunc('month', COALESCE(submitted_date, record_created_at)) AS month_bucket,"+
		" COUNT(*) records, COUNT(*) FILTER (WHERE is_winner) winners,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped FROM prow GROUP BY 1 ORDER BY 1 NULLS LAST")
	if err != nil {
		sendErr(w, err)
		return
	}
	awFY, err := s.Query(ctx, repo.AwardCore()+
		" SELECT fiscal_year, COUNT(*) records, COALESCE(SUM(awarded_value),0) awarded_value,"+
		" COUNT(*) FILTER (WHERE mapping_status='mapped') mapped FROM arows GROUP BY 1 ORDER BY 1")
	if err != nil {
		sendErr(w, err)
		return
	}
	feMethod, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT mapping_method, COUNT(*) records FROM prow WHERE mapping_status='mapped'"+
		" GROUP BY 1 ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	awMethod, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_method, COUNT(*) records, COALESCE(SUM(awarded_value),0) awarded_value FROM arows"+
		" WHERE mapping_status='mapped' GROUP BY 1 ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	rankDist, err := s.Query(ctx, repo.ParticipantCore()+
		` SELECT regexp_replace(COALESCE(financial_rank,''),'[^0-9]','','g') rank_num,
	             NULLIF(TRIM(financial_rank), '') financial_rank,
	             COUNT(*) records FROM prow GROUP BY 1, 2 ORDER BY records DESC LIMIT 12`)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Top L1 companies and top awarded companies.
	topL1, err := s.Query(ctx, repo.ParticipantCore()+
		` SELECT entity_id, canonical_name, ticker_nse, ticker_bse,
	             COUNT(*) FILTER (WHERE COALESCE(financial_rank,'') <> '' AND
	               regexp_replace(COALESCE(financial_rank,''),'[^0-9]','','g') = '1') l1_count,
	             COUNT(*) records
	      FROM prow WHERE mapping_status='mapped' AND is_listed = TRUE
	      GROUP BY entity_id, canonical_name, ticker_nse, ticker_bse
	      ORDER BY l1_count DESC, records DESC LIMIT 10`)
	if err != nil {
		sendErr(w, err)
		return
	}
	topAwarded, err := s.Query(ctx, repo.AwardCore()+
		` SELECT entity_id, canonical_name, ticker_nse, ticker_bse,
	              COALESCE(SUM(awarded_value),0) awarded_value,
	              COUNT(*) records
	       FROM arows WHERE mapping_status='mapped' AND is_listed = TRUE
	       GROUP BY entity_id, canonical_name, ticker_nse, ticker_bse
	       ORDER BY awarded_value DESC LIMIT 10`)
	if err != nil {
		sendErr(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"financial_evaluation": feTot,
		"awards":               awTot,
		"tender_lifecycle":     lc,
		"charts": map[string]any{
			"fe_by_month":            feMonth,
			"awards_by_fy":           awFY,
			"fe_method_distribution": feMethod,
			"aw_method_distribution": awMethod,
			"rank_distribution":      rankDist,
			"top_l1_companies":       topL1,
			"top_awarded_companies":  topAwarded,
		},
	})
}

// MappingsSummary serves GET /api/v1/mappings/summary.
func (s *Server) MappingsSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	feStatus, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT mapping_status, COUNT(*) records, COUNT(DISTINCT participant_name) unique_names,"+
		" COUNT(DISTINCT tender_id) unique_tenders, COUNT(DISTINCT entity_id) unique_entities,"+
		" COALESCE(SUM(bid_value),0) bid_value_total FROM prow GROUP BY mapping_status ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	feMethod, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT mapping_method, COUNT(*) records, COUNT(DISTINCT entity_id) unique_entities"+
		" FROM prow WHERE mapping_status='mapped' GROUP BY 1 ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	awStatus, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_status, COUNT(*) records, COUNT(DISTINCT awardee_name) unique_names,"+
		" COUNT(DISTINCT tender_id) unique_tenders, COUNT(DISTINCT entity_id) unique_entities,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total FROM arows GROUP BY mapping_status ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	awMethod, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_method, COUNT(*) records, COUNT(DISTINCT entity_id) unique_entities,"+
		" COALESCE(SUM(awarded_value),0) awarded_value_total"+
		" FROM arows WHERE mapping_status='mapped' GROUP BY 1 ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"financial_evaluation": map[string]any{"by_status": feStatus, "by_method": feMethod},
		"awards":               map[string]any{"by_status": awStatus, "by_method": awMethod},
	})
}

// feAggSQL: mapped financial-stage exposure per entity (bid values are
// financial-stage bids, never confirmed order inflow).
const feAggSQL = `
  SELECT entity_id, COUNT(*) fe_records, COUNT(DISTINCT tender_id) fe_tenders,
         COUNT(*) FILTER (WHERE COALESCE(financial_rank,'') <> '' AND
           regexp_replace(COALESCE(financial_rank,''),'[^0-9]','','g') = '1') l1_count,
         COALESCE(SUM(bid_value),0) bid_value_total
  FROM prow
  WHERE mapping_status = 'mapped' AND entity_id IS NOT NULL
  GROUP BY entity_id`

const awAggSQL = `
  SELECT entity_id, COUNT(*) award_records, COUNT(DISTINCT tender_id) awarded_tenders,
         COALESCE(SUM(awarded_value),0) awarded_value_total,
         MAX(awarded_value) max_awarded_value
  FROM arows
  WHERE mapping_status = 'mapped' AND entity_id IS NOT NULL
  GROUP BY entity_id`

// Companies serves GET /api/v1/companies.
func (s *Server) Companies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	q := &repo.QB{}
	q.W("WITH " + repo.ParticipantCTEs() + ", " + repo.AwardCTEs() + "," + `
  feagg AS (` + feAggSQL + "), awagg AS (" + awAggSQL + `), crow AS (
	    SELECT e.id AS entity_id, e.canonical_name, e.normalized_name, e.ticker_nse, e.ticker_bse,
	           e.isin, e.sector, e.industry, e.is_listed, e.exchange_status,
	           lf.fiscal_year, lf.revenue, lf.net_worth, lf.net_profit, lf.source_date,
	           lf.source AS financial_source,
	           lm.market_cap, lm.current_price, lm.snapshot_date,
	           COALESCE(f.fe_records, 0) fe_records,
	           COALESCE(f.fe_tenders, 0) fe_tenders,
	           COALESCE(f.l1_count, 0) l1_count,
	           f.bid_value_total,
	           COALESCE(a.award_records, 0) award_records,
	           COALESCE(a.awarded_tenders, 0) awarded_tenders,
	           a.awarded_value_total,
	           a.max_awarded_value,
	           COUNT(*) OVER () total_rows
	    FROM entities e
	    LEFT JOIN latest_entity_financials lf ON lf.entity_id = e.id
	    LEFT JOIN latest_entity_market_data lm ON lm.entity_id = e.id
	    LEFT JOIN feagg f ON f.entity_id = e.id
	    LEFT JOIN awagg a ON a.entity_id = e.id
	  )
	  SELECT * FROM crow`)

	var conds []string
	if txt := strings.TrimSpace(qp(r, "q", "")); txt != "" {
		p := ph(q, "%"+lower(txt)+"%")
		conds = append(conds, "(canonical_name ILIKE "+p+" OR ticker_nse ILIKE "+p+" OR ticker_bse ILIKE "+p+" OR isin ILIKE "+p+")")
	}
	if sec := strings.TrimSpace(qp(r, "sector", "")); sec != "" {
		conds = append(conds, "sector = "+ph(q, sec))
	}
	switch lower(qp(r, "has_activity", "")) {
	case "true":
		conds = append(conds, "(fe_records > 0 OR award_records > 0)")
	}
	switch lower(qp(r, "financials", "")) {
	case "with":
		conds = append(conds, "revenue IS NOT NULL")
	case "without":
		conds = append(conds, "revenue IS NULL")
	}
	switch lower(qp(r, "market", "")) {
	case "with":
		conds = append(conds, "market_cap IS NOT NULL")
	case "without":
		conds = append(conds, "market_cap IS NULL")
	}
	if len(conds) > 0 {
		q.W(" WHERE " + strings.Join(conds, " AND "))
	}
	ord := repo.OrderBy(qp(r, "sort", "entity"), qp(r, "dir", "asc"), repo.CompanySorts, "canonical_name")
	q.W(" ORDER BY " + ord)
	lim, off := paging(r)
	q.W(" LIMIT " + ph(q, lim) + " OFFSET " + ph(q, off))

	rows, err := s.Query(ctx, q.SQL(), q.Args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, listResp(rows, totalFrom(rows), lim, off))
}

// CompanyDetail serves GET /api/v1/companies/{entityId}.
func (s *Server) CompanyDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("entityId")
	if len(id) != 36 {
		fail(w, http.StatusBadRequest, "invalid entity id")
		return
	}
	base, err := s.QueryOne(ctx,
		`SELECT e.id AS entity_id, e.canonical_name, e.normalized_name, e.ticker_nse, e.ticker_bse,
	        e.isin, e.sector, e.industry, e.is_listed, e.exchange_status, e.website,
	        lf.fiscal_year, lf.revenue, lf.net_worth, lf.net_profit, lf.ebitda,
	        lf.borrowings, lf.total_assets, lf.source_date AS financial_snapshot, lf.source AS financial_source,
	        lm.market_cap, lm.current_price, lm.snapshot_date AS market_snapshot,
	        (SELECT string_agg(DISTINCT eg.gstin, ', ') FROM entity_gst eg WHERE eg.entity_id = e.id) gstins,
	        (SELECT string_agg(DISTINCT ea.alias_name, ' | ') FROM entity_aliases ea WHERE ea.entity_id = e.id) aliases
	  FROM entities e
	  LEFT JOIN latest_entity_financials lf ON lf.entity_id = e.id
	  LEFT JOIN latest_entity_market_data lm ON lm.entity_id = e.id
	  WHERE e.id = $1`, id)
	if err != nil {
		sendErr(w, err)
		return
	}
	if base == nil {
		fail(w, http.StatusNotFound, "entity not found")
		return
	}

	finHistory, err := s.Query(ctx,
		`SELECT fiscal_year, revenue, ebitda, net_profit, equity_capital, reserves,
	        net_worth, borrowings, total_assets, source_date, source
	  FROM entity_financials WHERE entity_id = $1 ORDER BY fiscal_year`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	mktHistory, err := s.Query(ctx,
		`SELECT snapshot_date, current_price, market_cap
	  FROM entity_market_data WHERE entity_id = $1 ORDER BY snapshot_date DESC`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Mapped financial-stage bid exposure.
	feTot, err := s.QueryOne(ctx, repo.ParticipantCore()+
		` SELECT entity_id, COUNT(*) participant_records, COUNT(DISTINCT tender_id) tender_count,
             COALESCE(SUM(bid_value),0) bid_value_total, MAX(bid_value) max_bid_value
      FROM prow WHERE entity_id::text = $1 GROUP BY entity_id`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Awarded exposure.
	awTot, err := s.QueryOne(ctx, repo.AwardCore()+
		` SELECT entity_id, COUNT(*) award_records, COUNT(DISTINCT tender_id) awarded_tenders,
             COALESCE(SUM(awarded_value),0) awarded_value_total, MAX(awarded_value) max_contract_value,
             (ARRAY_AGG(title ORDER BY awarded_value DESC NULLS LAST))[1] largest_contract
      FROM arows WHERE entity_id::text = $1 GROUP BY entity_id`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Recent participation and awards.
	feRows, err := s.Query(ctx, repo.ParticipantCore()+
		` SELECT participant_id, external_tender_id, tender_id, lifecycle_stage, organization, buyer_org,
	             title, participant_name, financial_rank, financial_status, bid_value, is_winner,
	             mapping_status, mapping_method, participant_normalized
	      FROM prow WHERE entity_id::text = $1 ORDER BY COALESCE(submitted_date, record_created_at) DESC LIMIT 100`, id)
	if err != nil {
		sendErr(w, err)
		return
	}
	awRows, err := s.Query(ctx, repo.AwardCore()+
		` SELECT meta_id, external_tender_id, tender_id, buyer_org, title, awardee_name,
		             awarded_value, contract_updated_date AS contract_date, fiscal_year, mapping_status, mapping_method,
	             contractor_business_name, contractor_gstin
	      FROM arows WHERE entity_id::text = $1 ORDER BY contract_date DESC NULLS LAST LIMIT 100`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	mat := materialityRatios(feTot, awTot, base)
	JSON(w, http.StatusOK, map[string]any{
		"entity":                  base,
		"financial_history":       finHistory,
		"market_history":          mktHistory,
		"financial_participation": feTot,
		"awarded_exposure":        awTot,
		"recent_participation":    feRows,
		"recent_awards":           awRows,
		"materiality":             mat,
	})
}

// materialityRatios computes exposure ratios; NULL when a denominator is
// missing or zero. Awarded/bid values are stored in rupees; financials and
// market data in Rs crore — denominators are converted before dividing.
func materialityRatios(feTot, awTot, base map[string]any) map[string]any {
	const cr = 10000000.0
	if base == nil {
		return nil
	}
	revenue := numOr(base, "revenue") * cr
	netWorth := numOr(base, "net_worth") * cr
	mcap := numOr(base, "market_cap") * cr
	awarded := numOr(awTot, "awarded_value_total")
	bid := numOr(feTot, "bid_value_total")
	ratio := func(num, den float64) any {
		if den == 0 {
			return nil
		}
		return num / den
	}
	return map[string]any{
		"awarded_vs_revenue":    ratio(awarded, revenue),
		"awarded_vs_net_worth":  ratio(awarded, netWorth),
		"awarded_vs_market_cap": ratio(awarded, mcap),
		"bid_vs_revenue":        ratio(bid, revenue),
		"bid_vs_net_worth":      ratio(bid, netWorth),
		"bid_vs_market_cap":     ratio(bid, mcap),
		"awarded_value_total":   awarded,
		"bid_value_total":       bid,
		"revenue":               numOr(base, "revenue"),
		"net_worth":             numOr(base, "net_worth"),
		"market_cap":            numOr(base, "market_cap"),
	}
}

func numOr(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// DataQuality serves GET /api/v1/data-quality.
func (s *Server) DataQuality(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	lifecycle, err := s.Query(ctx,
		`SELECT lifecycle_stage, COUNT(*) tender_count,
	        COUNT(*) FILTER (WHERE tem.awardee_name IS NULL OR tem.awardee_name = '') missing_awardee_tenders,
	        COUNT(*) FILTER (WHERE tem.awardee_name IS NOT NULL AND tem.awardee_name <> '') awarded_tenders
	  FROM tenders t LEFT JOIN tender_extracted_meta tem ON tem.tender_id = t.id
	  GROUP BY lifecycle_stage ORDER BY tender_count DESC`)
	if err != nil {
		sendErr(w, err)
		return
	}

	awQ, err := s.QueryOne(ctx,
		`SELECT COUNT(*) total_awards,
	        COUNT(*) FILTER (WHERE COALESCE(awardee_normalized,'') = '') missing_awardee_normalized,
	        COUNT(*) FILTER (WHERE COALESCE(awarded_value,'') = '' OR awarded_value IS NULL) missing_awarded_value,
	        COUNT(*) FILTER (WHERE awarded_value IS NOT NULL
	          AND regexp_replace(TRIM(awarded_value), '[, ]', '', 'g') !~ '^-?[0-9]+(\.?[0-9]*)?$') unparseable_awarded_value
	  FROM tender_extracted_meta WHERE awardee_name IS NOT NULL AND awardee_name <> ''`)
	if err != nil {
		sendErr(w, err)
		return
	}

	ptQ, err := s.QueryOne(ctx,
		`SELECT COUNT(*) total_participants,
	        COUNT(*) FILTER (WHERE COALESCE(participant_normalized,'') = '') missing_normalized,
	        COUNT(*) FILTER (WHERE financial_bid_value IS NOT NULL AND financial_bid_value <> ''
	          AND regexp_replace(TRIM(financial_bid_value), '[, ]', '', 'g') !~ '^-?[0-9]+(\.?[0-9]*)?$') unparseable_bid_value,
	        COUNT(*) FILTER (WHERE financial_bid_value IS NOT NULL AND financial_bid_value <> '') with_bid_value,
	        COUNT(*) FILTER (WHERE is_winner) winners,
	        COUNT(*) FILTER (WHERE COALESCE(financial_rank,'') = '') missing_rank
	  FROM tender_participants`)
	if err != nil {
		sendErr(w, err)
		return
	}

	feStatus, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT mapping_status, COUNT(*) records FROM prow GROUP BY mapping_status ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}
	awStatus, err := s.Query(ctx, repo.AwardCore()+
		" SELECT mapping_status, COUNT(*) records FROM arows GROUP BY mapping_status ORDER BY records DESC")
	if err != nil {
		sendErr(w, err)
		return
	}

	feAmb, err := s.Query(ctx, repo.ParticipantCore()+
		" SELECT participant_id, participant_name, participant_normalized, external_tender_id, candidate_count, candidate_names"+
		" FROM prow WHERE mapping_status='ambiguous' ORDER BY candidate_count DESC LIMIT 50")
	if err != nil {
		sendErr(w, err)
		return
	}
	awAmb, err := s.Query(ctx, repo.AwardCore()+
		" SELECT meta_id, awardee_name, awardee_normalized, external_tender_id, awarded_value, candidate_count, candidate_names, mapping_method"+
		" FROM arows WHERE mapping_status='ambiguous' ORDER BY candidate_count DESC LIMIT 50")
	if err != nil {
		sendErr(w, err)
		return
	}

	dupAliases, err := s.Query(ctx,
		`SELECT ea.normalized_alias,
	        STRING_AGG(DISTINCT ea.alias_name, ' | ') alias_names,
	        COUNT(DISTINCT ea.entity_id) entity_count,
	        STRING_AGG(DISTINCT e.canonical_name, ' | ') canonical_names
	  FROM entity_aliases ea JOIN entities e ON e.id = ea.entity_id
	  GROUP BY ea.normalized_alias
	  HAVING COUNT(DISTINCT ea.entity_id) > 1
	  ORDER BY entity_count DESC LIMIT 50`)
	if err != nil {
		sendErr(w, err)
		return
	}

	// One normalized awardee resolving to different entities across records.
	conflicts, err := s.Query(ctx, repo.AwardCore()+
		" SELECT awardee_name, awardee_normalized, COUNT(DISTINCT entity_id) entity_count, COUNT(*) records,"+
		" STRING_AGG(DISTINCT canonical_name, ' | ') canonical_names"+
		" FROM arows WHERE mapping_status='mapped' GROUP BY awardee_name, awardee_normalized"+
		" HAVING COUNT(DISTINCT entity_id) > 1 ORDER BY records DESC LIMIT 50")
	if err != nil {
		sendErr(w, err)
		return
	}

	finMissing, err := s.QueryOne(ctx,
		`SELECT COUNT(*) FILTER (WHERE lf.entity_id IS NULL) missing_financials,
	        COUNT(*) FILTER (WHERE lm.entity_id IS NULL) missing_market_data,
	        COUNT(*) FILTER (WHERE lf.entity_id IS NULL AND lm.entity_id IS NULL) missing_both,
	        COUNT(*) FILTER (WHERE lf.revenue IS NULL) missing_revenue
	  FROM entities e
	  LEFT JOIN latest_entity_financials lf ON lf.entity_id = e.id
	  LEFT JOIN latest_entity_market_data lm ON lm.entity_id = e.id`)
	if err != nil {
		sendErr(w, err)
		return
	}

	boq, err := s.QueryOne(ctx,
		`SELECT (SELECT COUNT(*) FROM tender_files) total_files,
	        (SELECT COUNT(*) FROM tender_files WHERE is_boq_parsed_success = TRUE) boq_parsed_success,
	        (SELECT COUNT(*) FROM tender_files WHERE is_boq_parsed_success = FALSE) boq_parsed_failed,
	        (SELECT COUNT(*) FROM tender_files WHERE is_boq_parsed_success IS NULL) boq_parse_unknown,
	        (SELECT COUNT(*) FROM boq_items) boq_items,
	        (SELECT COUNT(DISTINCT tf.tender_id) FROM tender_files tf WHERE tf.is_boq_parsed_success = TRUE) tenders_with_parsed_boq,
	        (SELECT COUNT(DISTINCT tender_id) FROM tender_files) tenders_with_files`)
	if err != nil {
		sendErr(w, err)
		return
	}

	gst, err := s.QueryOne(ctx,
		`SELECT (SELECT COUNT(*) FROM entity_gst) entity_gst_rows,
	        (SELECT COUNT(DISTINCT entity_id) FROM entity_gst) entities_with_gst,
	        (SELECT COUNT(*) FROM contractor_v2 WHERE gst IS NOT NULL AND gst <> '') contractor_v2_with_gst,
	        (SELECT COUNT(*) FROM contractor_v2 cv JOIN entity_gst eg ON eg.gstin = cv.gst) contractor_gst_matched`)
	if err != nil {
		sendErr(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"lifecycle":            lifecycle,
		"awards_quality":       awQ,
		"participants_quality": ptQ,
		"fe_mapping_status":    feStatus,
		"aw_mapping_status":    awStatus,
		"fe_ambiguous":         feAmb,
		"aw_ambiguous":         awAmb,
		"duplicate_aliases":    dupAliases,
		"conflicting_mappings": conflicts,
		"entity_data_missing":  finMissing,
		"boq_parsing":          boq,
		"gst_coverage":         gst,
	})
}
