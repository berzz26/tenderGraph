package ingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"vyuha/api/internal/projection"
)

type SourceScanner struct {
	Pool *pgxpool.Pool
	// RecomputePairs enables the incremental path that includes pairs touching
	// either endpoint. Full/selected scans can generate each unordered pair
	// once, owned by its lower participant id.
	RecomputePairs bool
}

type ScannedBatch struct {
	Items  int
	Cursor Cursor
	Batch  projection.Batch
}

func (s SourceScanner) HighWatermark(ctx context.Context, spec DatasetSpec) (Cursor, error) {
	expr := timeExpr(spec)
	query := fmt.Sprintf(`SELECT %s, %s::text FROM %s ORDER BY %s DESC, %s DESC LIMIT 1`, expr, spec.PKColumn, spec.SourceTable, expr, spec.PKColumn)
	var ts time.Time
	var pk string
	if err := s.Pool.QueryRow(ctx, query).Scan(&ts, &pk); err != nil {
		if err == pgx.ErrNoRows || strings.Contains(err.Error(), "no rows") {
			return Cursor{}, nil
		}
		return Cursor{}, fmt.Errorf("high watermark %s: %w", spec.Name, err)
	}
	return Cursor{Time: &ts, PK: pk}, nil
}

func (s SourceScanner) Scan(ctx context.Context, spec DatasetSpec, after, high Cursor, limit int, syncedAtMS int64) (ScannedBatch, error) {
	if s.Pool == nil {
		return ScannedBatch{}, fmt.Errorf("source scanner: nil pool")
	}
	if limit <= 0 || limit > 1000 {
		return ScannedBatch{}, fmt.Errorf("invalid batch limit %d", limit)
	}
	where, args := bounds(spec, after, high)
	switch spec.Name {
	case "contractor_v2":
		return s.scanContractors(ctx, spec, where, args, limit, syncedAtMS)
	case "entities":
		return s.scanEntities(ctx, spec, where, args, limit, syncedAtMS)
	case "entity_aliases":
		return s.scanAliases(ctx, spec, where, args, limit, syncedAtMS)
	case "entity_gst":
		return s.scanGST(ctx, spec, where, args, limit, syncedAtMS)
	case "tenders":
		return s.scanTenders(ctx, spec, where, args, limit, syncedAtMS)
	case "tender_participants":
		return s.scanParticipants(ctx, spec, where, args, limit, syncedAtMS)
	case "tender_extracted_meta":
		return s.scanAwards(ctx, spec, where, args, limit, syncedAtMS)
	default:
		return ScannedBatch{}, fmt.Errorf("no source scanner for dataset %s", spec.Name)
	}
}

func (s SourceScanner) scanContractors(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT id::bigint, COALESCE(business_name,''), COALESCE(gst,''), COALESCE((EXTRACT(EPOCH FROM created_at)*1000)::bigint,0), COALESCE(%s,created_at)
		FROM contractor_v2 WHERE %s ORDER BY %s, id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var out projection.Batch
	var last Cursor
	count := 0
	for rows.Next() {
		var id int64
		var name, gst string
		var createdMS int64
		var ts time.Time
		if err := rows.Scan(&id, &name, &gst, &createdMS, &ts); err != nil {
			return ScannedBatch{}, err
		}
		n, err := projection.VendorNode(projection.VendorRecord{ID: id, BusinessName: name, GSTIN: gst, CreatedAtMS: createdMS}, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, n)
		last = Cursor{Time: &ts, PK: strconv.FormatInt(id, 10)}
		count++
	}
	return ScannedBatch{Items: count, Cursor: last, Batch: out}, rows.Err()
}

func (s SourceScanner) scanEntities(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT id::text, COALESCE(canonical_name,''), COALESCE(normalized_name,''), COALESCE(ticker_nse,''), COALESCE(ticker_bse,''), COALESCE(is_listed,false), COALESCE(exchange,''), COALESCE(exchange_status,''), COALESCE(sector,''), COALESCE(industry,''), COALESCE(website,''), %s
		FROM entities WHERE %s ORDER BY %s, id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var out projection.Batch
	var last Cursor
	count := 0
	for rows.Next() {
		var id, canonical, normalized, nse, bse, exchange, status, sector, industry, website string
		var listed bool
		var ts time.Time
		if err := rows.Scan(&id, &canonical, &normalized, &nse, &bse, &listed, &exchange, &status, &sector, &industry, &website, &ts); err != nil {
			return ScannedBatch{}, err
		}
		n, err := projection.EntityNode(projection.EntityRecord{ID: id, CanonicalName: canonical, NormalizedName: normalized, TickerNSE: nse, TickerBSE: bse, ISListed: listed, Exchange: exchange, ExchangeStatus: status, Sector: sector, Industry: industry, Website: website}, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, n)
		last = Cursor{Time: &ts, PK: id}
		count++
	}
	return ScannedBatch{Items: count, Cursor: last, Batch: out}, rows.Err()
}

func (s SourceScanner) scanAliases(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT id::text, entity_id::text, COALESCE(alias_name,''), COALESCE(normalized_alias,''), COALESCE(source,''), %s FROM entity_aliases WHERE %s ORDER BY %s,id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var out projection.Batch
	var last Cursor
	count := 0
	for rows.Next() {
		var id, entityID, name, norm, source string
		var ts time.Time
		if err := rows.Scan(&id, &entityID, &name, &norm, &source, &ts); err != nil {
			return ScannedBatch{}, err
		}
		an, err := projection.AliasNode(projection.AliasRecord{ID: id, EntityID: entityID, AliasName: name, NormalizedAlias: norm, Source: source}, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		en := projection.NodeRef("CorporateEntity", projection.GID("ENTITY", "v", entityID))
		e, err := projection.AliasEdge(en, an, id, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, an)
		out.Edges = append(out.Edges, e)
		last = Cursor{Time: &ts, PK: id}
		count++
	}
	return ScannedBatch{Items: count, Cursor: last, Batch: out}, rows.Err()
}

func (s SourceScanner) scanGST(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT id::bigint, entity_id::text, COALESCE(gstin,''), COALESCE(company_name,''), COALESCE(state,''), %s FROM entity_gst WHERE %s ORDER BY %s,id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var out projection.Batch
	var last Cursor
	count := 0
	for rows.Next() {
		var id int64
		var entityID, gstin, name, state string
		var ts time.Time
		if err := rows.Scan(&id, &entityID, &gstin, &name, &state, &ts); err != nil {
			return ScannedBatch{}, err
		}
		gn, err := projection.GstNode(projection.GstRecord{ID: id, EntityID: entityID, GSTIN: gstin, CompanyName: name, State: state}, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		en := projection.NodeRef("CorporateEntity", projection.GID("ENTITY", "v", entityID))
		e, err := projection.GstEdge(en, gn, strconv.FormatInt(id, 10), synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, gn)
		out.Edges = append(out.Edges, e)
		last = Cursor{Time: &ts, PK: strconv.FormatInt(id, 10)}
		count++
	}
	return ScannedBatch{Items: count, Cursor: last, Batch: out}, rows.Err()
}

func (s SourceScanner) scanTenders(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT t.id::bigint,t.tender_id,COALESCE(t.organization,''),COALESCE(t.website_url,''),COALESCE(t.lifecycle_stage,''),COALESCE(t.contractor_v2_id,0)::bigint,COALESCE((EXTRACT(EPOCH FROM t.created_at)*1000)::bigint,0),COALESCE((EXTRACT(EPOCH FROM t.updated_at)*1000)::bigint,0),%s FROM tenders t WHERE %s ORDER BY %s,t.id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var out projection.Batch
	var last Cursor
	count := 0
	for rows.Next() {
		var t projection.TenderRecord
		var contractor int64
		var ts time.Time
		if err := rows.Scan(&t.ID, &t.ExternalID, &t.Organization, &t.WebsiteURL, &t.LifecycleStage, &contractor, &t.CreatedAtMS, &t.UpdatedAtMS, &ts); err != nil {
			return ScannedBatch{}, err
		}
		t.OrganizationNorm = projection.NormalizeName(t.Organization)
		tn, err := projection.TenderNode(t, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, tn)
		if t.OrganizationNorm != "" {
			on, err := projection.NewNode("GovernmentOrganization", projection.GID("GOVORG", "org", t.OrganizationNorm), "tenders.organization", t.OrganizationNorm, synced, map[string]any{"name": t.Organization, "norm_name": t.OrganizationNorm})
			if err != nil {
				return ScannedBatch{}, err
			}
			out.Nodes = append(out.Nodes, on)
			e, err := projection.NewEdge("ISSUED_BY", tn, on, "tenders", strconv.FormatInt(t.ID, 10), synced, map[string]any{"organization_norm": t.OrganizationNorm})
			if err != nil {
				return ScannedBatch{}, err
			}
			out.Edges = append(out.Edges, e)
		}
		if contractor != 0 {
			vn := projection.NodeRef("Vendor", projection.GID("VENDOR", "cp", strconv.FormatInt(contractor, 10)))
			e, err := projection.NewEdge("CONTRACTOR_ON_RECORD", vn, tn, "tenders", strconv.FormatInt(t.ID, 10), synced, map[string]any{"assignment": "contractor_of_record"})
			if err != nil {
				return ScannedBatch{}, err
			}
			out.Edges = append(out.Edges, e)
		}
		last = Cursor{Time: &ts, PK: strconv.FormatInt(t.ID, 10)}
		count++
	}
	return ScannedBatch{Items: count, Cursor: last, Batch: out}, rows.Err()
}

func (s SourceScanner) scanParticipants(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT id::bigint,tender_id::bigint,COALESCE(bid_number,''),COALESCE(participant_name,''),COALESCE(participant_normalized,''),COALESCE(financial_rank,''),COALESCE(financial_bid_value,''),COALESCE(is_winner,false),COALESCE(technical_status,''),COALESCE(financial_status,''),COALESCE((EXTRACT(EPOCH FROM submitted_date)*1000)::bigint,0),COALESCE((EXTRACT(EPOCH FROM updated_at)*1000)::bigint,0),%s FROM tender_participants WHERE %s ORDER BY %s,id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var bids []projection.BidRecord
	var last Cursor
	for rows.Next() {
		var b projection.BidRecord
		var ts time.Time
		if err := rows.Scan(&b.ID, &b.TenderID, &b.BidNumber, &b.ParticipantName, &b.ParticipantNormalized, &b.FinancialRank, &b.FinancialBidValue, &b.IsWinner, &b.TechnicalStatus, &b.FinancialStatus, &b.SubmittedAtMS, &b.UpdatedAtMS, &ts); err != nil {
			return ScannedBatch{}, err
		}
		bids = append(bids, b)
		last = Cursor{Time: &ts, PK: strconv.FormatInt(b.ID, 10)}
	}
	out := projection.Batch{}
	for _, b := range bids {
		bn, err := projection.BidNode(b, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, bn)
		tn := projection.NodeRef("Tender", projection.GID("TENDER", "v", strconv.FormatInt(b.TenderID, 10)))
		e, err := projection.NewEdge("SUBMITTED_BID", bn, tn, "tender_participants", strconv.FormatInt(b.ID, 10), synced, map[string]any{"tender_src_pk": strconv.FormatInt(b.TenderID, 10)})
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Edges = append(out.Edges, e)
	}
	if len(bids) > 0 {
		participantIDs := make([]int64, 0, len(bids))
		for _, b := range bids {
			participantIDs = append(participantIDs, b.ID)
		}
		pairs, err := s.loadBidPairs(ctx, participantIDs)
		if err != nil {
			return ScannedBatch{}, err
		}
		for _, pair := range pairs {
			on, le, re, err := projection.CoBidObservationPair(pair[0], pair[1], synced)
			if err != nil {
				return ScannedBatch{}, err
			}
			out.Nodes = append(out.Nodes, on)
			out.Edges = append(out.Edges, le, re)
		}
	}
	return ScannedBatch{Items: len(bids), Cursor: last, Batch: out}, nil
}

func (s SourceScanner) scanAwards(ctx context.Context, spec DatasetSpec, where string, args []any, limit int, synced int64) (ScannedBatch, error) {
	q := fmt.Sprintf(`SELECT tem.id::bigint,tem.tender_id::bigint,COALESCE(tem.awardee_name,''),COALESCE(tem.awardee_normalized,''),COALESCE(tem.awarded_value,''),COALESCE(tem.winning_bid_number,''),COALESCE(tem.contract_date::text,''),COALESCE(v.outcome_classification,''),%s FROM tender_extracted_meta tem LEFT JOIN LATERAL (SELECT outcome_classification FROM vw_vyuha_l1_intelligence WHERE tender_id=tem.tender_id AND winner_evidence IN ('BID_NUMBER_MATCH','AWARDEE_NAME_MATCH') ORDER BY participant_id LIMIT 1) v ON true WHERE %s ORDER BY %s,tem.id LIMIT $%d`, timeExpr(spec), where, timeExpr(spec), len(args)+1)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return ScannedBatch{}, err
	}
	defer rows.Close()
	var awards []projection.AwardRecord
	var last Cursor
	for rows.Next() {
		var a projection.AwardRecord
		var ts time.Time
		if err := rows.Scan(&a.ID, &a.TenderID, &a.AwardeeName, &a.AwardeeNormalized, &a.AwardedValue, &a.WinningBidNumber, &a.ContractDate, &a.OutcomeClassification, &ts); err != nil {
			return ScannedBatch{}, err
		}
		a.AwardStage = a.OutcomeClassification
		awards = append(awards, a)
		last = Cursor{Time: &ts, PK: strconv.FormatInt(a.ID, 10)}
	}
	out := projection.Batch{}
	ids := make([]int64, 0, len(awards))
	for _, a := range awards {
		ids = append(ids, a.TenderID)
	}
	bidMap, err := s.loadBidsByTenders(ctx, ids)
	if err != nil {
		return ScannedBatch{}, err
	}
	for _, a := range awards {
		if strings.TrimSpace(a.AwardeeName) == "" && strings.TrimSpace(a.WinningBidNumber) == "" {
			continue
		}
		an, err := projection.AwardNode(a, synced)
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Nodes = append(out.Nodes, an)
		tn := projection.NodeRef("Tender", projection.GID("TENDER", "v", strconv.FormatInt(a.TenderID, 10)))
		e, err := projection.NewEdge("HAS_AWARD", tn, an, "tender_extracted_meta", strconv.FormatInt(a.ID, 10), synced, map[string]any{"tender_src_pk": strconv.FormatInt(a.TenderID, 10)})
		if err != nil {
			return ScannedBatch{}, err
		}
		out.Edges = append(out.Edges, e)
		for _, b := range bidMap[a.TenderID] {
			if ev, ok := projection.WinnerEvidenceFor(b, a); ok {
				bn := projection.NodeRef("Bid", projection.GID("BID", "v", strconv.FormatInt(b.ID, 10)))
				we, err := projection.NewEdge("WON", bn, an, "tender_participants+tender_extracted_meta", strconv.FormatInt(b.ID, 10), synced, map[string]any{"award_src_pk": strconv.FormatInt(a.ID, 10), "evidence_kind": ev.Kind, "method": ev.Method})
				if err != nil {
					return ScannedBatch{}, err
				}
				out.Edges = append(out.Edges, we)
			}
		}
	}
	return ScannedBatch{Items: len(awards), Cursor: last, Batch: out}, nil
}

func (s SourceScanner) loadBidPairs(ctx context.Context, participantIDs []int64) ([][2]projection.BidRecord, error) {
	condition := "p1.id=ANY($1::bigint[])"
	if s.RecomputePairs {
		condition = "p1.id=ANY($1::bigint[]) OR p2.id=ANY($1::bigint[])"
	}
	query := `SELECT p1.id::bigint,p1.tender_id::bigint,COALESCE(p1.bid_number,''),COALESCE(p1.participant_name,''),COALESCE(p1.participant_normalized,''),COALESCE(p1.financial_rank,''),COALESCE(p1.financial_bid_value,''),COALESCE(p1.is_winner,false),COALESCE(p1.technical_status,''),COALESCE(p1.financial_status,''),COALESCE((EXTRACT(EPOCH FROM p1.submitted_date)*1000)::bigint,0),COALESCE((EXTRACT(EPOCH FROM p1.updated_at)*1000)::bigint,0),p2.id::bigint,p2.tender_id::bigint,COALESCE(p2.bid_number,''),COALESCE(p2.participant_name,''),COALESCE(p2.participant_normalized,''),COALESCE(p2.financial_rank,''),COALESCE(p2.financial_bid_value,''),COALESCE(p2.is_winner,false),COALESCE(p2.technical_status,''),COALESCE(p2.financial_status,''),COALESCE((EXTRACT(EPOCH FROM p2.submitted_date)*1000)::bigint,0),COALESCE((EXTRACT(EPOCH FROM p2.updated_at)*1000)::bigint,0) FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id=p1.tender_id AND p2.id>p1.id WHERE ` + condition + ` ORDER BY p1.tender_id,p1.id,p2.id`
	rows, err := s.Pool.Query(ctx, query, participantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]projection.BidRecord
	for rows.Next() {
		var a, b projection.BidRecord
		if err := rows.Scan(&a.ID, &a.TenderID, &a.BidNumber, &a.ParticipantName, &a.ParticipantNormalized, &a.FinancialRank, &a.FinancialBidValue, &a.IsWinner, &a.TechnicalStatus, &a.FinancialStatus, &a.SubmittedAtMS, &a.UpdatedAtMS, &b.ID, &b.TenderID, &b.BidNumber, &b.ParticipantName, &b.ParticipantNormalized, &b.FinancialRank, &b.FinancialBidValue, &b.IsWinner, &b.TechnicalStatus, &b.FinancialStatus, &b.SubmittedAtMS, &b.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, [2]projection.BidRecord{a, b})
	}
	return out, rows.Err()
}

func (s SourceScanner) loadBidsByTenders(ctx context.Context, tenderIDs []int64) (map[int64][]projection.BidRecord, error) {
	out := map[int64][]projection.BidRecord{}
	if len(tenderIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::bigint,tender_id::bigint,COALESCE(bid_number,''),COALESCE(participant_name,''),COALESCE(participant_normalized,''),COALESCE(financial_rank,''),COALESCE(financial_bid_value,''),COALESCE(is_winner,false),COALESCE(technical_status,''),COALESCE(financial_status,''),COALESCE((EXTRACT(EPOCH FROM submitted_date)*1000)::bigint,0),COALESCE((EXTRACT(EPOCH FROM updated_at)*1000)::bigint,0) FROM tender_participants WHERE tender_id=ANY($1::bigint[]) ORDER BY tender_id,id`, tenderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b projection.BidRecord
		if err := rows.Scan(&b.ID, &b.TenderID, &b.BidNumber, &b.ParticipantName, &b.ParticipantNormalized, &b.FinancialRank, &b.FinancialBidValue, &b.IsWinner, &b.TechnicalStatus, &b.FinancialStatus, &b.SubmittedAtMS, &b.UpdatedAtMS); err != nil {
			return nil, err
		}
		out[b.TenderID] = append(out[b.TenderID], b)
	}
	return out, rows.Err()
}

func timeExpr(spec DatasetSpec) string {
	if spec.Strategy == StrategyCreatedPK {
		return spec.TimeColumn
	}
	return fmt.Sprintf("COALESCE(%s, created_at)", spec.TimeColumn)
}

func bounds(spec DatasetSpec, after, high Cursor) (string, []any) {
	expr := timeExpr(spec)
	parts := []string{}
	args := []any{}
	if after.Time != nil {
		args = append(args, *after.Time)
		if after.PK == "" {
			parts = append(parts, fmt.Sprintf("%s >= $%d", expr, len(args)))
		} else {
			args = append(args, after.PK)
			cast := spec.PKType
			parts = append(parts, fmt.Sprintf("(%s > $%d OR (%s = $%d AND %s > $%d::%s))", expr, len(args)-1, expr, len(args)-1, spec.PKColumn, len(args), cast))
		}
	}
	if high.Time != nil {
		args = append(args, *high.Time, high.PK)
		cast := spec.PKType
		parts = append(parts, fmt.Sprintf("(%s < $%d OR (%s = $%d AND %s <= $%d::%s))", expr, len(args)-1, expr, len(args)-1, spec.PKColumn, len(args), cast))
	}
	if len(parts) == 0 {
		return "TRUE", args
	}
	return strings.Join(parts, " AND "), args
}
