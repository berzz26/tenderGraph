package srv

import (
	"net/http"
	"strings"

	"vyuha/api/internal/repo"
)

// TenderDetail serves GET /api/v1/tenders/{tenderId}.
//
// It resolves one tender by its external tender_id (e.g. "2026_IGRMS_925402_1")
// and carries everything the detail page needs:
//   - the tenders + tender_extracted_meta record and the assigned contractor_v2
//   - deterministic mapping outcome for the awardee (AwardCore CTE composition)
//   - every participant of the tender with its own mapping outcome
//   - document files with per-file BOQ parse state
//   - the parsed BOQ line items (capped)
func (s *Server) TenderDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("tenderId"))
	if id == "" {
		fail(w, http.StatusBadRequest, "tender id is required")
		return
	}
	ctx := r.Context()

	tender, err := s.QueryOne(ctx, repo.TenderDetailSQL, id)
	if err != nil {
		sendErr(w, err)
		return
	}
	if tender == nil {
		fail(w, http.StatusNotFound, "tender not found")
		return
	}

	files, err := s.Query(ctx, repo.TenderFilesSQL, tender["tender_internal_id"])
	if err != nil {
		sendErr(w, err)
		return
	}

	// Participant rows for this tender, reusing the shared resolution CTEs.
	participants, err := s.Query(ctx, repo.ParticipantCore()+`
		 SELECT participant_id, participant_name, participant_normalized, financial_rank,
		        financial_status, financial_remarks, bid_value, is_winner, submitted_date,
		        mapping_status, mapping_method, candidate_count, entity_id, canonical_name,
		        ticker_nse, ticker_bse, isin, is_listed, sector, candidate_names
		 FROM prow
		 WHERE external_tender_id = $1
		 ORDER BY financial_rank NULLS LAST, participant_name`, id)
	if err != nil {
		sendErr(w, err)
		return
	}

	// Award (awardee) mapping row for this tender.
	awardRows, err := s.Query(ctx, repo.AwardCore()+`
		 SELECT meta_id, awardee_name, awardee_normalized, awarded_value, contract_date,
		        fiscal_year, winning_bid_number, mapping_status, mapping_method,
		        candidate_count, entity_id, canonical_name, ticker_nse, ticker_bse,
		        isin, is_listed, sector, contractor_business_name, contractor_gstin,
		        candidate_names
		 FROM arows
		 WHERE external_tender_id = $1`, id)
	if err != nil {
		sendErr(w, err)
		return
	}
	var award map[string]any
	if len(awardRows) > 0 {
		award = awardRows[0]
	}

	boq, err := s.Query(ctx, repo.TenderBoqItemsSQL+" LIMIT 500", tender["tender_internal_id"])
	if err != nil {
		sendErr(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"tender":       tender,
		"award":        award,
		"participants": participants,
		"files":        files,
		"boq_items":    boq,
	})
}
