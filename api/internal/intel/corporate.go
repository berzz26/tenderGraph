package intel

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"vyuha/api/internal/projection"
)

var decimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

// ParseExactDecimal validates a source monetary string without converting it
// through float64. Commas and spaces are accepted; malformed values are not.
func ParseExactDecimal(raw string) (string, *big.Rat, bool) {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(raw), ",", ""), " ", "")
	if clean == "" || !decimalPattern.MatchString(clean) {
		return "", nil, false
	}
	r := new(big.Rat)
	if _, ok := r.SetString(clean); !ok {
		return "", nil, false
	}
	return clean, r, true
}

func decimalTotal(values []string) (string, int) {
	total := new(big.Rat)
	valid := 0
	for _, value := range values {
		_, r, ok := ParseExactDecimal(value)
		if ok {
			total.Add(total, r)
			valid++
		}
	}
	return total.FloatString(2), valid
}

type EntitySummary struct {
	EntityID       string `json:"entity_id"`
	EntityGID      string `json:"entity_gid"`
	CanonicalName  string `json:"canonical_name"`
	NormalizedName string `json:"normalized_name,omitempty"`
	TickerNSE      string `json:"ticker_nse,omitempty"`
	TickerBSE      string `json:"ticker_bse,omitempty"`
	IsListed       bool   `json:"is_listed"`
}

type VendorCandidate struct {
	VendorID       int64      `json:"vendor_id"`
	VendorGID      string     `json:"vendor_gid"`
	BusinessName   string     `json:"business_name"`
	GSTIN          string     `json:"gstin,omitempty"`
	Status         string     `json:"status"`
	Methods        []string   `json:"methods"`
	Evidence       []Evidence `json:"evidence"`
	GSTEntityCount int        `json:"gst_entity_count,omitempty"`
}

type AwardExposure struct {
	TenderID        int64      `json:"tender_id"`
	TenderGID       string     `json:"tender_gid"`
	ExternalTender  string     `json:"external_tender_id"`
	Organization    string     `json:"organization"`
	TenderDate      *time.Time `json:"tender_date,omitempty"`
	AwardID         *int64     `json:"award_id,omitempty"`
	AwardGID        string     `json:"award_gid,omitempty"`
	AwardeeName     string     `json:"awardee_name,omitempty"`
	AwardValueRaw   string     `json:"award_value_raw,omitempty"`
	AwardValueINR   string     `json:"award_value_inr_exact,omitempty"`
	AwardValueValid bool       `json:"award_value_valid"`
	ContractDate    string     `json:"contract_date,omitempty"`
	Association     string     `json:"association"`
	Evidence        []Evidence `json:"evidence"`
}

type Exposure struct {
	Entity                  EntitySummary     `json:"entity"`
	VendorCandidates        []VendorCandidate `json:"vendor_candidates"`
	TenderCount             int               `json:"contractor_on_record_tender_count"`
	EvidencedAwardCount     int               `json:"evidenced_award_count"`
	TotalAwardValueINR      string            `json:"total_award_value_inr_exact"`
	ValidAwardValues        int               `json:"valid_award_value_count"`
	InvalidAwardValues      int               `json:"invalid_award_value_count"`
	GovernmentOrganizations []string          `json:"government_organizations"`
	Awards                  []AwardExposure   `json:"awards"`
	Interpretation          string            `json:"interpretation"`
}

type FinancialSnapshot struct {
	FiscalYear       string     `json:"fiscal_year"`
	RevenueExact     string     `json:"revenue_exact,omitempty"`
	EBITDAExact      string     `json:"ebitda_exact,omitempty"`
	NetProfitExact   string     `json:"net_profit_exact,omitempty"`
	NetWorthExact    string     `json:"net_worth_exact,omitempty"`
	TotalAssetsExact string     `json:"total_assets_exact,omitempty"`
	Unit             string     `json:"unit"`
	Source           string     `json:"source,omitempty"`
	SourceDate       *time.Time `json:"source_date,omitempty"`
}

type MarketSnapshot struct {
	SnapshotDate      time.Time `json:"snapshot_date"`
	MarketCapExact    string    `json:"market_cap_exact,omitempty"`
	CurrentPriceExact string    `json:"current_price_exact,omitempty"`
	SharesOutstanding *int64    `json:"shares_outstanding,omitempty"`
	MarketCapUnit     string    `json:"market_cap_unit"`
	PriceUnit         string    `json:"price_unit"`
}

type Announcement struct {
	ID               int64      `json:"announcement_id"`
	GID              string     `json:"announcement_gid"`
	Title            string     `json:"title,omitempty"`
	Category         string     `json:"category,omitempty"`
	CompanyName      string     `json:"company_name,omitempty"`
	Symbol           string     `json:"symbol,omitempty"`
	ISIN             string     `json:"isin,omitempty"`
	AnnouncementTime *time.Time `json:"announcement_time,omitempty"`
	Source           string     `json:"source"`
	AttachmentURL    string     `json:"attachment_url,omitempty"`
	DocumentCount    int        `json:"document_count"`
	Association      string     `json:"association"`
	Evidence         Evidence   `json:"evidence"`
}

type CompanyReport struct {
	Entity          EntitySummary       `json:"entity"`
	LinkCandidates  []VendorCandidate   `json:"link_candidates"`
	Exposure        Exposure            `json:"procurement_exposure"`
	Financials      []FinancialSnapshot `json:"financial_snapshots"`
	LatestMarket    *MarketSnapshot     `json:"latest_market_snapshot,omitempty"`
	Announcements   []Announcement      `json:"announcements"`
	DataLimitations []string            `json:"data_limitations"`
}

type AwardReport struct {
	AwardID           int64      `json:"award_id"`
	AwardGID          string     `json:"award_gid"`
	TenderID          int64      `json:"tender_id"`
	TenderGID         string     `json:"tender_gid"`
	ExternalTender    string     `json:"external_tender_id"`
	Organization      string     `json:"organization"`
	AwardeeName       string     `json:"awardee_name"`
	AwardeeNormalized string     `json:"awardee_normalized,omitempty"`
	AwardValueRaw     string     `json:"award_value_raw,omitempty"`
	AwardValueINR     string     `json:"award_value_inr_exact,omitempty"`
	AwardValueValid   bool       `json:"award_value_valid"`
	WinningBidNumber  string     `json:"winning_bid_number,omitempty"`
	WinnerEvidence    []Evidence `json:"winner_evidence"`
	CandidateEntities []Evidence `json:"candidate_entity_evidence"`
}

func (s Service) loadEntity(ctx context.Context, id string) (EntitySummary, error) {
	if err := s.requirePool(); err != nil {
		return EntitySummary{}, err
	}
	var e EntitySummary
	err := s.Pool.QueryRow(ctx, `SELECT id::text,COALESCE(canonical_name,''),COALESCE(normalized_name,''),COALESCE(ticker_nse,''),COALESCE(ticker_bse,''),COALESCE(is_listed,false) FROM entities WHERE id=$1`, id).Scan(&e.EntityID, &e.CanonicalName, &e.NormalizedName, &e.TickerNSE, &e.TickerBSE, &e.IsListed)
	if err != nil {
		return e, fmt.Errorf("load entity %s: %w", id, err)
	}
	e.EntityGID = projection.GID("ENTITY", "v", id)
	return e, nil
}

func (s Service) LinkCandidates(ctx context.Context, entityID string, limit int) ([]VendorCandidate, error) {
	entity, err := s.loadEntity(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT gstin FROM entity_gst WHERE entity_id=$1 AND NULLIF(BTRIM(gstin),'') IS NOT NULL`, entityID)
	if err != nil {
		return nil, err
	}
	var gstins []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			rows.Close()
			return nil, err
		}
		gstins = append(gstins, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	q := `SELECT c.id::bigint,COALESCE(c.business_name,''),COALESCE(c.gst,'') FROM contractor_v2 c WHERE FALSE`
	args := []any{}
	if len(gstins) > 0 {
		q = `SELECT c.id::bigint,COALESCE(c.business_name,''),COALESCE(c.gst,'') FROM contractor_v2 c WHERE c.gst=ANY($1::text[])`
		args = []any{gstins}
	}
	if entity.NormalizedName != "" {
		if len(args) == 0 {
			q = `SELECT c.id::bigint,COALESCE(c.business_name,''),COALESCE(c.gst,'') FROM contractor_v2 c WHERE LOWER(regexp_replace(COALESCE(c.business_name,''),'[^a-zA-Z0-9]+','','g'))=LOWER(regexp_replace($1,'[^a-zA-Z0-9]+','','g'))`
			args = []any{entity.NormalizedName}
		} else {
			q += ` OR LOWER(regexp_replace(COALESCE(c.business_name,''),'[^a-zA-Z0-9]+','','g'))=LOWER(regexp_replace($2,'[^a-zA-Z0-9]+','','g'))`
			args = append(args, entity.NormalizedName)
		}
	}
	q += ` ORDER BY c.id LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, limit)
	rows, err = s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VendorCandidate
	for rows.Next() {
		var c VendorCandidate
		if err := rows.Scan(&c.VendorID, &c.BusinessName, &c.GSTIN); err != nil {
			return nil, err
		}
		c.VendorGID = projection.GID("VENDOR", "cp", fmt.Sprint(c.VendorID))
		c.Status = "CANDIDATE"
		c.Methods = []string{}
		c.Evidence = []Evidence{}
		if c.GSTIN != "" {
			var count int
			_ = s.Pool.QueryRow(ctx, `SELECT COUNT(DISTINCT entity_id) FROM entity_gst WHERE gstin=$1`, c.GSTIN).Scan(&count)
			c.GSTEntityCount = count
			if contains(gstins, c.GSTIN) {
				if count == 1 {
					c.Methods = append(c.Methods, "GSTIN_EXACT_UNIQUE")
				} else {
					c.Methods = append(c.Methods, "GSTIN_EXACT_AMBIGUOUS")
				}
				c.Evidence = append(c.Evidence, Evidence{"entity_gst", c.GSTIN, "GST:v:" + c.GSTIN})
			}
		}
		if normalizeComparable(c.BusinessName) == normalizeComparable(entity.NormalizedName) && entity.NormalizedName != "" {
			c.Methods = append(c.Methods, "NAME_NORMALIZED_EXACT")
			c.Evidence = append(c.Evidence, Evidence{"entities", entityID, entity.EntityGID})
		}
		if len(c.Methods) > 0 {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func normalizeComparable(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s Service) Exposure(ctx context.Context, entityID string, limit int) (Exposure, error) {
	e, err := s.loadEntity(ctx, entityID)
	if err != nil {
		return Exposure{}, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	candidates, err := s.LinkCandidates(ctx, entityID, limit)
	if err != nil {
		return Exposure{}, err
	}
	out := Exposure{Entity: e, VendorCandidates: candidates, Awards: []AwardExposure{}, GovernmentOrganizations: []string{}, Interpretation: "These are contractor-of-record exposure records reached through candidate Vendor-to-entity evidence. Contractor assignment is not proof of bid participation, winning, revenue or cash receipt."}
	ids := make([]int64, len(candidates))
	for i := range candidates {
		ids[i] = candidates[i].VendorID
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id::bigint,t.tender_id,t.organization,t.created_at,tem.id::bigint,COALESCE(tem.awardee_name,''),COALESCE(tem.awarded_value,''),COALESCE(tem.contract_date::text,'') FROM tenders t JOIN contractor_v2 c ON c.id=t.contractor_v2_id LEFT JOIN tender_extracted_meta tem ON tem.tender_id=t.id WHERE c.id=ANY($1::bigint[]) ORDER BY t.id DESC LIMIT $2`, ids, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	orgs := map[string]bool{}
	tenders := map[int64]bool{}
	var values []string
	for rows.Next() {
		var a AwardExposure
		var ext string
		var aid *int64
		var value string
		if err := rows.Scan(&a.TenderID, &ext, &a.Organization, &a.TenderDate, &aid, &a.AwardeeName, &value, &a.ContractDate); err != nil {
			return out, err
		}
		a.ExternalTender = ext
		a.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(a.TenderID))
		a.Association = "CONTRACTOR_ON_RECORD"
		a.Evidence = []Evidence{{"tenders", fmt.Sprint(a.TenderID), a.TenderGID}}
		tenders[a.TenderID] = true
		if aid != nil && (a.AwardeeName != "" || value != "") {
			a.AwardID = aid
			gid := projection.GID("AWARD", "v", fmt.Sprint(*aid))
			a.AwardGID = gid
			a.AwardValueRaw = value
			a.Evidence = append(a.Evidence, Evidence{"tender_extracted_meta", fmt.Sprint(*aid), gid})
			if exact, _, ok := ParseExactDecimal(value); ok {
				a.AwardValueINR = exact
				a.AwardValueValid = true
				values = append(values, value)
			} else {
				out.InvalidAwardValues++
			}
			out.EvidencedAwardCount++
		}
		orgs[a.Organization] = true
		out.Awards = append(out.Awards, a)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.TenderCount = len(tenders)
	out.TotalAwardValueINR, out.ValidAwardValues = decimalTotal(values)
	for org := range orgs {
		out.GovernmentOrganizations = append(out.GovernmentOrganizations, org)
	}
	sort.Strings(out.GovernmentOrganizations)
	return out, nil
}

func (s Service) Announcements(ctx context.Context, entityID string, limit int) ([]Announcement, error) {
	if _, err := s.loadEntity(ctx, entityID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id::bigint,COALESCE(a.title,''),COALESCE(a.category,''),COALESCE(a.company_name,''),COALESCE(a.symbol,''),COALESCE(a.isin,''),a.announcement_time,COALESCE(a.source,''),COALESCE(a.attachment_url,''),COUNT(d.id) FROM announcements_raw a LEFT JOIN announcement_documents d ON d.announcement_id=a.id WHERE a.entity_id=$1 GROUP BY a.id ORDER BY a.announcement_time DESC NULLS LAST,a.id DESC LIMIT $2`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Title, &a.Category, &a.CompanyName, &a.Symbol, &a.ISIN, &a.AnnouncementTime, &a.Source, &a.AttachmentURL, &a.DocumentCount); err != nil {
			return nil, err
		}
		a.GID = projection.GID("ANN", "v", fmt.Sprint(a.ID))
		a.Association = "EXPLICIT_SOURCE_ENTITY_FK"
		a.Evidence = Evidence{"announcements_raw", fmt.Sprint(a.ID), a.GID}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s Service) Financials(ctx context.Context, entityID string, limit int) ([]FinancialSnapshot, error) {
	if _, err := s.loadEntity(ctx, entityID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.Pool.Query(ctx, `SELECT fiscal_year,COALESCE(revenue::text,''),COALESCE(ebitda::text,''),COALESCE(net_profit::text,''),COALESCE(net_worth::text,''),COALESCE(total_assets::text,''),COALESCE(source,''),source_date FROM entity_financials WHERE entity_id=$1 ORDER BY fiscal_year DESC LIMIT $2`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinancialSnapshot
	for rows.Next() {
		var f FinancialSnapshot
		if err := rows.Scan(&f.FiscalYear, &f.RevenueExact, &f.EBITDAExact, &f.NetProfitExact, &f.NetWorthExact, &f.TotalAssetsExact, &f.Source, &f.SourceDate); err != nil {
			return nil, err
		}
		f.Unit = "INR crore"
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s Service) LatestMarket(ctx context.Context, entityID string) (*MarketSnapshot, error) {
	if _, err := s.loadEntity(ctx, entityID); err != nil {
		return nil, err
	}
	var m MarketSnapshot
	err := s.Pool.QueryRow(ctx, `SELECT snapshot_date,COALESCE(market_cap::text,''),COALESCE(current_price::text,''),shares_outstanding FROM latest_entity_market_data WHERE entity_id=$1`, entityID).Scan(&m.SnapshotDate, &m.MarketCapExact, &m.CurrentPriceExact, &m.SharesOutstanding)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.MarketCapUnit = "INR crore"
	m.PriceUnit = "INR/share"
	return &m, nil
}

func (s Service) Company(ctx context.Context, entityID string, limit int) (CompanyReport, error) {
	e, err := s.loadEntity(ctx, entityID)
	if err != nil {
		return CompanyReport{}, err
	}
	links, err := s.LinkCandidates(ctx, entityID, limit)
	if err != nil {
		return CompanyReport{}, err
	}
	exp, err := s.Exposure(ctx, entityID, limit)
	if err != nil {
		return CompanyReport{}, err
	}
	fin, err := s.Financials(ctx, entityID, limit)
	if err != nil {
		return CompanyReport{}, err
	}
	market, err := s.LatestMarket(ctx, entityID)
	if err != nil {
		return CompanyReport{}, err
	}
	anns, err := s.Announcements(ctx, entityID, limit)
	if err != nil {
		return CompanyReport{}, err
	}
	return CompanyReport{Entity: e, LinkCandidates: links, Exposure: exp, Financials: fin, LatestMarket: market, Announcements: anns, DataLimitations: []string{"No automatic legal identity confirmation; contractor links are candidates.", "entity_market_data is snapshot data; market_daily_prices is empty, so no return or event-study calculation is made.", "company_events and tender_announcement_matches are currently empty.", "Procurement award values are rupees; financial and market-cap values are INR crore."}}, nil
}

func (s Service) Award(ctx context.Context, awardID int64) (AwardReport, error) {
	if err := s.requirePool(); err != nil {
		return AwardReport{}, err
	}
	var a AwardReport
	var value, winning, normalized string
	err := s.Pool.QueryRow(ctx, `SELECT tem.id::bigint,tem.tender_id::bigint,t.tender_id,COALESCE(t.organization,''),COALESCE(tem.awardee_name,''),COALESCE(tem.awardee_normalized,''),COALESCE(tem.awarded_value,''),COALESCE(tem.winning_bid_number,'') FROM tender_extracted_meta tem JOIN tenders t ON t.id=tem.tender_id WHERE tem.id=$1`, awardID).Scan(&a.AwardID, &a.TenderID, &a.ExternalTender, &a.Organization, &a.AwardeeName, &normalized, &value, &winning)
	if err != nil {
		return a, err
	}
	a.AwardGID = projection.GID("AWARD", "v", fmt.Sprint(awardID))
	a.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(a.TenderID))
	a.AwardeeNormalized = normalized
	a.AwardValueRaw = value
	a.WinningBidNumber = winning
	if exact, _, ok := ParseExactDecimal(value); ok {
		a.AwardValueINR = exact
		a.AwardValueValid = true
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::bigint FROM tender_participants WHERE tender_id=$1 AND (($2<>'' AND BTRIM(bid_number)=BTRIM($2)) OR ($3<>'' AND participant_normalized=$3))`, a.TenderID, winning, normalized)
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var bid int64
		if err := rows.Scan(&bid); err != nil {
			return a, err
		}
		a.WinnerEvidence = append(a.WinnerEvidence, Evidence{"tender_participants", fmt.Sprint(bid), projection.GID("BID", "v", fmt.Sprint(bid))})
	}
	rows.Close()
	if normalized != "" {
		erows, err := s.Pool.Query(ctx, `SELECT id::text FROM entities WHERE normalized_name=$1 UNION SELECT entity_id::text FROM entity_aliases WHERE normalized_alias=$1 LIMIT 25`, normalized)
		if err == nil {
			for erows.Next() {
				var id string
				if erows.Scan(&id) == nil {
					a.CandidateEntities = append(a.CandidateEntities, Evidence{"entities", id, projection.GID("ENTITY", "v", id)})
				}
			}
			erows.Close()
		}
	}
	return a, nil
}
