package projection

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SourceAdapter reads the Phase 2 vertical-slice source tables. Every query
// here is a parameterized SELECT; the adapter has no mutation methods.
type SourceAdapter struct{ Pool *pgxpool.Pool }

// LoadVerticalSlice reads one tender and its directly supported core facts.
func (a SourceAdapter) LoadVerticalSlice(ctx context.Context, tenderID int64) (TenderRecord, []BidRecord, *VendorRecord, *AwardRecord, error) {
	if a.Pool == nil {
		return TenderRecord{}, nil, nil, nil, fmt.Errorf("source adapter: nil pool")
	}
	var t TenderRecord
	var contractorID int64
	var metaID int64
	var org, website, stage string
	var title, category, productCategory, refNo, location, contractType, pincode, description string
	var tenderValue, emdValue string
	row := a.Pool.QueryRow(ctx, `
		SELECT t.id::bigint, t.tender_id, COALESCE(t.organization,''), COALESCE(t.website_url,''),
		       COALESCE(t.lifecycle_stage,''), COALESCE(t.contractor_v2_id,0)::bigint,
		       COALESCE((EXTRACT(EPOCH FROM t.created_at) * 1000)::bigint,0),
		       COALESCE((EXTRACT(EPOCH FROM t.updated_at) * 1000)::bigint,0),
		       COALESCE(tem.id,0)::bigint, COALESCE(tem.title,''), COALESCE(tem.category,''),
		       COALESCE(tem.product_category,''), COALESCE(tem.ref_no,''), COALESCE(tem.location,''),
		       COALESCE(tem.contract_type,''), COALESCE(tem.pincode,''), COALESCE(tem.description,''),
		       COALESCE(tem.tender_value,''), COALESCE(tem.emd_value::text,'')
		FROM tenders t
		LEFT JOIN tender_extracted_meta tem ON tem.tender_id = t.id
		WHERE t.id = $1`, tenderID)
	if err := row.Scan(&t.ID, &t.ExternalID, &org, &website, &stage, &contractorID, &t.CreatedAtMS, &t.UpdatedAtMS,
		&metaID, &title, &category, &productCategory, &refNo, &location, &contractType, &pincode, &description, &tenderValue, &emdValue); err != nil {
		return TenderRecord{}, nil, nil, nil, fmt.Errorf("load tender %d: %w", tenderID, err)
	}
	t.Organization, t.WebsiteURL, t.LifecycleStage = org, website, stage
	t.OrganizationNorm = NormalizeName(org)
	t.Title, t.Category, t.ProductCategory, t.RefNo, t.Location = title, category, productCategory, refNo, location
	t.ContractType, t.Pincode, t.Description, t.TenderValue, t.EMDValue = contractType, pincode, description, tenderValue, emdValue

	bids, err := a.loadBids(ctx, t.ID)
	if err != nil {
		return TenderRecord{}, nil, nil, nil, err
	}

	var vendor *VendorRecord
	if contractorID != 0 {
		var v VendorRecord
		if err := a.Pool.QueryRow(ctx, `SELECT id::bigint, COALESCE(business_name,''), COALESCE(gst,''), COALESCE((EXTRACT(EPOCH FROM created_at)*1000)::bigint,0) FROM contractor_v2 WHERE id=$1`, contractorID).
			Scan(&v.ID, &v.BusinessName, &v.GSTIN, &v.CreatedAtMS); err != nil {
			return TenderRecord{}, nil, nil, nil, fmt.Errorf("load contractor %d: %w", contractorID, err)
		}
		vendor = &v
	}

	var award *AwardRecord
	if metaID != 0 {
		var ar AwardRecord
		var awardee, awardeeNorm, awarded, winning, contractDate string
		if err := a.Pool.QueryRow(ctx, `
			SELECT id::bigint, tender_id::bigint, COALESCE(awardee_name,''), COALESCE(awardee_normalized,''),
			       COALESCE(awarded_value,''), COALESCE(winning_bid_number,''), COALESCE(contract_date::text,'')
			FROM tender_extracted_meta WHERE id=$1`, metaID).
			Scan(&ar.ID, &ar.TenderID, &awardee, &awardeeNorm, &awarded, &winning, &contractDate); err != nil {
			return TenderRecord{}, nil, nil, nil, fmt.Errorf("load award meta %d: %w", metaID, err)
		}
		ar.AwardeeName, ar.AwardeeNormalized, ar.AwardedValue, ar.WinningBidNumber, ar.ContractDate = awardee, awardeeNorm, awarded, winning, contractDate
		// The view is a validation/reference projection. It supplies the observed
		// outcome classification but does not replace the source table.
		var outcome string
		_ = a.Pool.QueryRow(ctx, `
			SELECT COALESCE(outcome_classification,'')
			FROM vw_vyuha_l1_intelligence
			WHERE tender_id=$1 AND winner_evidence IN ('BID_NUMBER_MATCH','AWARDEE_NAME_MATCH')
			ORDER BY participant_id LIMIT 1`, t.ID).Scan(&outcome)
		ar.OutcomeClassification, ar.AwardStage = outcome, outcome
		award = &ar
	}
	return t, bids, vendor, award, nil
}

func (a SourceAdapter) loadBids(ctx context.Context, tenderID int64) ([]BidRecord, error) {
	rows, err := a.Pool.Query(ctx, `
		SELECT id::bigint, tender_id::bigint, COALESCE(bid_number,''), COALESCE(participant_name,''),
		       COALESCE(participant_normalized,''), COALESCE(financial_rank,''), COALESCE(financial_bid_value,''),
		       COALESCE(is_winner,false), COALESCE(technical_status,''), COALESCE(financial_status,''),
		       COALESCE((EXTRACT(EPOCH FROM submitted_date)*1000)::bigint,0),
		       COALESCE((EXTRACT(EPOCH FROM updated_at)*1000)::bigint,0)
		FROM tender_participants WHERE tender_id=$1 ORDER BY id`, tenderID)
	if err != nil {
		return nil, fmt.Errorf("load bids for tender %d: %w", tenderID, err)
	}
	defer rows.Close()
	var out []BidRecord
	for rows.Next() {
		var b BidRecord
		if err := rows.Scan(&b.ID, &b.TenderID, &b.BidNumber, &b.ParticipantName, &b.ParticipantNormalized, &b.FinancialRank, &b.FinancialBidValue, &b.IsWinner, &b.TechnicalStatus, &b.FinancialStatus, &b.SubmittedAtMS, &b.UpdatedAtMS); err != nil {
			return nil, fmt.Errorf("scan bid: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bids: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tender %d has no participant rows", tenderID)
	}
	return out, nil
}

// WinnerEvidenceSummary is useful for the Phase 2 report and validation.
func WinnerEvidenceSummary(bids []BidRecord, award *AwardRecord) map[string]int {
	out := map[string]int{}
	if award == nil {
		return out
	}
	for _, b := range bids {
		if e, ok := WinnerEvidenceFor(b, *award); ok {
			out[strings.TrimSpace(e.Kind)]++
		}
	}
	return out
}
