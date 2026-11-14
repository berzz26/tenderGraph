// Package projection contains database-independent TenderGraph projection
// primitives. It deliberately does not know how PostgreSQL or HydraDB are
// connected; callers provide verified source records and a sync timestamp.
package projection

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// IDAlgoV is the immutable numeric-id algorithm version stored on every node.
const IDAlgoV int64 = 1

const idAlgoName = "fnv1a64-mask63"

var nonName = regexp.MustCompile(`[^a-z0-9&]+`)
var spaces = regexp.MustCompile(`\s+`)
var legalSuffix = regexp.MustCompile(`\b(pvt|private|ltd|limited|llp|limted|limte)\b`)

// Node is a graph node before transport to HydraDB.
type Node struct {
	Label string
	GID   string
	ID    int64
	Props map[string]any
}

// Edge is a directed, typed relationship. RID is a deterministic relationship
// property id; it is not treated as a HydraDB uniqueness constraint.
type Edge struct {
	Type      string
	FromLabel string
	ToLabel   string
	FromGID   string
	ToGID     string
	FromID    int64
	ToID      int64
	RID       int64
	Props     map[string]any
}

// Batch is the complete graph content for one small projection unit.
type Batch struct {
	Nodes []Node
	Edges []Edge
}

// TenderRecord is the source-backed procurement tender subset used in Phase 2.
type TenderRecord struct {
	ID               int64
	ExternalID       string
	Organization     string
	OrganizationNorm string
	WebsiteURL       string
	LifecycleStage   string
	Title            string
	Category         string
	ProductCategory  string
	RefNo            string
	Location         string
	ContractType     string
	Pincode          string
	Description      string
	TenderValue      string
	EMDValue         string
	CreatedAtMS      int64
	UpdatedAtMS      int64
}

// BidRecord is one actual tender_participants row.
type BidRecord struct {
	ID                    int64
	TenderID              int64
	BidNumber             string
	ParticipantName       string
	ParticipantNormalized string
	FinancialRank         string
	FinancialBidValue     string
	IsWinner              bool
	TechnicalStatus       string
	FinancialStatus       string
	SubmittedAtMS         int64
	UpdatedAtMS           int64
}

// VendorRecord is a contractor_v2 row. It is not inferred from a participant
// name and is never merged with CorporateEntity.
type VendorRecord struct {
	ID           int64
	BusinessName string
	GSTIN        string
	LegalStatus  string
	Address      string
	CreatedAtMS  int64
}

// AwardRecord is one tender_extracted_meta row with award evidence.
type AwardRecord struct {
	ID                    int64
	TenderID              int64
	AwardeeName           string
	AwardeeNormalized     string
	AwardedValue          string
	WinningBidNumber      string
	ContractDate          string
	AwardStage            string
	OutcomeClassification string
}

// WinnerEvidence describes positive evidence for a Bid -> Award edge.
type WinnerEvidence struct {
	Kind   string
	Method string
}

// NormalizeName mirrors the repository's sqlf.NormExpr convention for values
// that are not already stored in a *_normalized source column.
func NormalizeName(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	s = nonName.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&", " and ")
	s = legalSuffix.ReplaceAllString(s, " ")
	s = spaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// GID returns the canonical graph identity string.
func GID(typ, origin, key string) string {
	return typ + ":" + origin + ":" + key
}

// NumericID derives the non-negative HydraDB vertex id from the complete GID.
// FNV-1a is in the Go standard library and is stable across supported
// platforms. The high bit is masked to satisfy HydraDB's non-negative integer
// contract; IDAlgoV must be bumped before changing this implementation.
func NumericID(gid string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(gid))
	return int64(h.Sum64() & math.MaxInt64)
}

// IDAlgorithmName is included in dry-run reports and documentation.
func IDAlgorithmName() string { return idAlgoName }

// NewNode creates a node and adds the mandatory provenance properties.
func NewNode(label, gid, srcTable, srcPK string, syncedAtMS int64, props map[string]any) (Node, error) {
	if err := validateIdentifier(label); err != nil {
		return Node{}, err
	}
	if gid == "" || srcTable == "" || srcPK == "" {
		return Node{}, errors.New("node requires gid, src_table and src_pk")
	}
	id := NumericID(gid)
	p := cloneMap(props)
	p["id"] = id
	p["gid"] = gid
	p["id_algo_v"] = IDAlgoV
	p["src_table"] = srcTable
	p["src_pk"] = srcPK
	p["synced_at_ms"] = syncedAtMS
	return Node{Label: label, GID: gid, ID: id, Props: p}, nil
}

// NewEdge creates a directed edge with deterministic endpoint-derived identity
// and mandatory provenance. The edge id is only a replay key carried as a
// property; graph correctness does not depend on relationship constraints.
func NewEdge(typ string, from, to Node, srcTable, srcPK string, syncedAtMS int64, props map[string]any) (Edge, error) {
	if err := validateIdentifier(typ); err != nil {
		return Edge{}, err
	}
	if srcTable == "" || srcPK == "" {
		return Edge{}, errors.New("edge requires src_table and src_pk")
	}
	edgeGID := GID("EDGE", typ, from.GID+":"+to.GID+":"+srcPK)
	p := cloneMap(props)
	p["id"] = NumericID(edgeGID)
	p["src_table"] = srcTable
	p["src_pk"] = srcPK
	p["synced_at_ms"] = syncedAtMS
	return Edge{
		Type: typ, FromLabel: from.Label, ToLabel: to.Label,
		FromGID: from.GID, ToGID: to.GID, FromID: from.ID, ToID: to.ID,
		RID: NumericID(edgeGID), Props: p,
	}, nil
}

// BuildVerticalSlice projects the Phase 2 procurement core for one tender.
// Missing optional source records remain missing; the function never invents
// participant-to-vendor or vendor-to-entity links.
func BuildVerticalSlice(t TenderRecord, bids []BidRecord, vendor *VendorRecord, award *AwardRecord, syncedAtMS int64) (Batch, error) {
	var out Batch
	orgNorm := t.OrganizationNorm
	if orgNorm == "" {
		orgNorm = NormalizeName(t.Organization)
	}
	tender, err := tenderNode(t, syncedAtMS)
	if err != nil {
		return Batch{}, err
	}
	out.Nodes = append(out.Nodes, tender)

	var org Node
	if orgNorm != "" {
		org, err = NewNode("GovernmentOrganization", GID("GOVORG", "org", orgNorm), "tenders.organization", orgNorm, syncedAtMS, map[string]any{
			"name":      t.Organization,
			"norm_name": orgNorm,
		})
		if err != nil {
			return Batch{}, err
		}
		out.Nodes = append(out.Nodes, org)
		e, eerr := NewEdge("ISSUED_BY", tender, org, "tenders", strconv.FormatInt(t.ID, 10), syncedAtMS, map[string]any{
			"organization_norm": orgNorm,
		})
		if eerr != nil {
			return Batch{}, eerr
		}
		out.Edges = append(out.Edges, e)
	}

	bidNodes := make([]Node, 0, len(bids))
	for _, b := range bids {
		n, nerr := bidNode(b, syncedAtMS)
		if nerr != nil {
			return Batch{}, nerr
		}
		bidNodes = append(bidNodes, n)
		out.Nodes = append(out.Nodes, n)
		e, eerr := NewEdge("SUBMITTED_BID", n, tender, "tender_participants", strconv.FormatInt(b.ID, 10), syncedAtMS, map[string]any{
			"tender_src_pk": strconv.FormatInt(t.ID, 10),
		})
		if eerr != nil {
			return Batch{}, eerr
		}
		out.Edges = append(out.Edges, e)
	}

	if vendor != nil && vendor.ID != 0 {
		vn, nerr := vendorNode(*vendor, syncedAtMS)
		if nerr != nil {
			return Batch{}, nerr
		}
		out.Nodes = append(out.Nodes, vn)
		e, eerr := NewEdge("CONTRACTOR_ON_RECORD", vn, tender, "tenders", strconv.FormatInt(t.ID, 10), syncedAtMS, map[string]any{
			"assignment": "contractor_of_record",
		})
		if eerr != nil {
			return Batch{}, eerr
		}
		out.Edges = append(out.Edges, e)
	}

	if award != nil && (strings.TrimSpace(award.AwardeeName) != "" || strings.TrimSpace(award.WinningBidNumber) != "") {
		an, nerr := awardNode(*award, syncedAtMS)
		if nerr != nil {
			return Batch{}, nerr
		}
		out.Nodes = append(out.Nodes, an)
		e, eerr := NewEdge("HAS_AWARD", tender, an, "tender_extracted_meta", strconv.FormatInt(award.ID, 10), syncedAtMS, map[string]any{
			"tender_src_pk": strconv.FormatInt(award.TenderID, 10),
		})
		if eerr != nil {
			return Batch{}, eerr
		}
		out.Edges = append(out.Edges, e)
		for i, b := range bids {
			evidence, ok := WinnerEvidenceFor(b, *award)
			if !ok {
				continue
			}
			we, werr := NewEdge("WON", bidNodes[i], an, "tender_participants+tender_extracted_meta", strconv.FormatInt(b.ID, 10), syncedAtMS, map[string]any{
				"award_src_pk":  strconv.FormatInt(award.ID, 10),
				"evidence_kind": evidence.Kind,
				"method":        evidence.Method,
			})
			if werr != nil {
				return Batch{}, werr
			}
			out.Edges = append(out.Edges, we)
		}
	}

	obsNodes, obsEdges, oerr := BuildCoBidObservations(bids, syncedAtMS)
	if oerr != nil {
		return Batch{}, oerr
	}
	out.Nodes = append(out.Nodes, obsNodes...)
	out.Edges = append(out.Edges, obsEdges...)
	return out, nil
}

// WinnerEvidenceFor implements the verified winner-evidence order. is_winner
// alone intentionally returns false.
func WinnerEvidenceFor(b BidRecord, a AwardRecord) (WinnerEvidence, bool) {
	if strings.TrimSpace(a.WinningBidNumber) != "" && strings.TrimSpace(b.BidNumber) == strings.TrimSpace(a.WinningBidNumber) {
		return WinnerEvidence{Kind: "BID_NUMBER_MATCH", Method: "winning_bid_number_exact"}, true
	}
	awardNorm := a.AwardeeNormalized
	if awardNorm == "" {
		awardNorm = NormalizeName(a.AwardeeName)
	}
	participantNorm := b.ParticipantNormalized
	if participantNorm == "" {
		participantNorm = NormalizeName(b.ParticipantName)
	}
	if awardNorm != "" && awardNorm == participantNorm {
		return WinnerEvidence{Kind: "AWARDEE_NAME_MATCH", Method: "awardee_normalized_exact"}, true
	}
	return WinnerEvidence{}, false
}

// NewIdentityCandidate creates an evidence-backed candidate edge. It is the
// only identity edge constructor in this phase: confirmation and rejection are
// analyst-controlled state transitions and are therefore not created by
// source-row projection.
func NewIdentityCandidate(left, right Node, method string, evidenceGIDs []string, srcTable, srcPK string, syncedAtMS int64) (Edge, error) {
	if method == "" || len(evidenceGIDs) == 0 {
		return Edge{}, errors.New("identity candidate requires method and evidence gids")
	}
	encoded, err := json.Marshal(evidenceGIDs)
	if err != nil {
		return Edge{}, fmt.Errorf("encode identity evidence: %w", err)
	}
	return NewEdge("IDENTIFIED_AS", left, right, srcTable, srcPK, syncedAtMS, map[string]any{
		"status":         "CANDIDATE",
		"method":         method,
		"evidence_gids":  string(encoded),
		"evidence_count": int64(len(evidenceGIDs)),
	})
}

// BuildCoBidObservations creates exactly one observation per tender and
// unordered participant pair, with two Bid -> Observation edges.
func BuildCoBidObservations(bids []BidRecord, syncedAtMS int64) ([]Node, []Edge, error) {
	sorted := append([]BidRecord(nil), bids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var nodes []Node
	var edges []Edge
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			left, right := sorted[i], sorted[j]
			gid := GID("COBID", "v", fmt.Sprintf("%d:%d:%d", left.TenderID, left.ID, right.ID))
			on, err := NewNode("CoBidObservation", gid, "tender_participants", fmt.Sprintf("%d:%d:%d", left.ID, right.ID, left.TenderID), syncedAtMS, map[string]any{
				"tender_pk": left.TenderID,
				"left_pk":   left.ID,
				"right_pk":  right.ID,
				"left_gid":  GID("BID", "v", strconv.FormatInt(left.ID, 10)),
				"right_gid": GID("BID", "v", strconv.FormatInt(right.ID, 10)),
			})
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, on)
			ln, lerr := bidNode(left, syncedAtMS)
			if lerr != nil {
				return nil, nil, lerr
			}
			rn, rerr := bidNode(right, syncedAtMS)
			if rerr != nil {
				return nil, nil, rerr
			}
			for _, bn := range []Node{ln, rn} {
				e, eerr := NewEdge("CO_BID_IN", bn, on, "tender_participants", strconv.FormatInt(bn.ID, 10), syncedAtMS, map[string]any{
					"observation_tender_pk": left.TenderID,
					"observation_left_pk":   left.ID,
					"observation_right_pk":  right.ID,
					"derivation_method":     "same_tender_participant_pair",
				})
				if eerr != nil {
					return nil, nil, eerr
				}
				edges = append(edges, e)
			}
		}
	}
	return nodes, edges, nil
}

func tenderNode(t TenderRecord, synced int64) (Node, error) {
	p := map[string]any{"tender_id": t.ExternalID, "organization": t.Organization, "lifecycle_stage": t.LifecycleStage, "website_url": t.WebsiteURL}
	put(p, "title", t.Title)
	put(p, "category", t.Category)
	put(p, "product_category", t.ProductCategory)
	put(p, "ref_no", t.RefNo)
	put(p, "location", t.Location)
	put(p, "contract_type", t.ContractType)
	put(p, "pincode", t.Pincode)
	put(p, "description", t.Description)
	putInt(p, "created_at_ms", t.CreatedAtMS)
	putInt(p, "updated_at_ms", t.UpdatedAtMS)
	putMoney(p, "tender_value", t.TenderValue, "INR")
	putMoney(p, "emd_value", t.EMDValue, "INR")
	return NewNode("Tender", GID("TENDER", "v", strconv.FormatInt(t.ID, 10)), "tenders", strconv.FormatInt(t.ID, 10), synced, p)
}

func bidNode(b BidRecord, synced int64) (Node, error) {
	p := map[string]any{"bid_number": b.BidNumber, "participant_name": b.ParticipantName, "participant_normalized": b.ParticipantNormalized, "is_winner": b.IsWinner, "tender_pk": b.TenderID}
	put(p, "financial_rank", b.FinancialRank)
	put(p, "technical_status", b.TechnicalStatus)
	put(p, "financial_status", b.FinancialStatus)
	putInt(p, "submitted_at_ms", b.SubmittedAtMS)
	putInt(p, "updated_at_ms", b.UpdatedAtMS)
	putMoney(p, "financial_bid_value", b.FinancialBidValue, "INR")
	return NewNode("Bid", GID("BID", "v", strconv.FormatInt(b.ID, 10)), "tender_participants", strconv.FormatInt(b.ID, 10), synced, p)
}

func vendorNode(v VendorRecord, synced int64) (Node, error) {
	p := map[string]any{"business_name": v.BusinessName, "gstin": v.GSTIN}
	put(p, "legal_status", v.LegalStatus)
	put(p, "address", v.Address)
	putInt(p, "created_at_ms", v.CreatedAtMS)
	return NewNode("Vendor", GID("VENDOR", "cp", strconv.FormatInt(v.ID, 10)), "contractor_v2", strconv.FormatInt(v.ID, 10), synced, p)
}

func awardNode(a AwardRecord, synced int64) (Node, error) {
	norm := a.AwardeeNormalized
	if norm == "" {
		norm = NormalizeName(a.AwardeeName)
	}
	p := map[string]any{"awardee_name": a.AwardeeName, "awardee_normalized": norm, "winning_bid_number": a.WinningBidNumber, "contract_date": a.ContractDate, "award_stage": a.AwardStage, "outcome_classification": a.OutcomeClassification, "tender_pk": a.TenderID}
	putMoney(p, "awarded_value", a.AwardedValue, "INR")
	return NewNode("Award", GID("AWARD", "v", strconv.FormatInt(a.ID, 10)), "tender_extracted_meta", strconv.FormatInt(a.ID, 10), synced, p)
}

func put(p map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		p[key] = value
	}
}
func putInt(p map[string]any, key string, value int64) {
	if value != 0 {
		p[key] = value
	}
}

func putMoney(p map[string]any, field, raw, unit string) {
	exact, numeric, ok := ParseMoney(raw)
	if !ok {
		return
	}
	p[field+"_exact"] = exact
	p[field+"_inr"] = numeric
	p[field+"_unit"] = unit
}

// ParseMoney removes source grouping separators while retaining the exact
// decimal spelling as the canonical property and returns a non-authoritative
// float analytics copy.
func ParseMoney(raw string) (string, float64, bool) {
	exact := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(raw), ",", ""), " ", "")
	if exact == "" {
		return "", 0, false
	}
	n, err := strconv.ParseFloat(exact, 64)
	if err != nil {
		return "", 0, false
	}
	return exact, n, true
}

// CollisionError means an existing numeric id is assigned to a different GID.
type CollisionError struct {
	ID                       int64
	ExistingGID, IncomingGID string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf("graph id collision: id=%d existing gid=%q incoming gid=%q", e.ID, e.ExistingGID, e.IncomingGID)
}

// ValidateNoCollisions checks both an existing graph lookup and duplicate ids
// inside the outgoing batch.
func ValidateNoCollisions(existing map[int64]string, nodes []Node) error {
	seen := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		if prior, ok := seen[n.ID]; ok && prior != n.GID {
			return &CollisionError{ID: n.ID, ExistingGID: prior, IncomingGID: n.GID}
		}
		seen[n.ID] = n.GID
		if old, ok := existing[n.ID]; ok && old != "" && old != n.GID {
			return &CollisionError{ID: n.ID, ExistingGID: old, IncomingGID: n.GID}
		}
	}
	return nil
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+6)
	for k, v := range in {
		out[k] = v
	}
	return out
}
func validateIdentifier(s string) error {
	if s == "" {
		return errors.New("empty graph identifier")
	}
	for i, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return fmt.Errorf("invalid graph identifier %q", s)
		}
	}
	return nil
}
