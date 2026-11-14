package projection

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// EntityRecord is the approved CorporateEntity source subset.
type EntityRecord struct {
	ID             string
	CanonicalName  string
	NormalizedName string
	TickerNSE      string
	TickerBSE      string
	ISListed       bool
	Exchange       string
	ExchangeStatus string
	Sector         string
	Industry       string
	Website        string
}

// AliasRecord is one entity_aliases row.
type AliasRecord struct {
	ID              string
	EntityID        string
	AliasName       string
	NormalizedAlias string
	Source          string
}

// GstRecord is one entity_gst row.
type GstRecord struct {
	ID          int64
	EntityID    string
	GSTIN       string
	CompanyName string
	State       string
}

// EntityNode projects an entities row. The UUID is retained as the canonical
// source key in the GID and provenance.
func EntityNode(e EntityRecord, syncedAtMS int64) (Node, error) {
	if _, err := uuid.Parse(e.ID); err != nil {
		return Node{}, fmt.Errorf("entity id %q: %w", e.ID, err)
	}
	p := map[string]any{
		"canonical_name":  e.CanonicalName,
		"normalized_name": e.NormalizedName,
		"ticker_nse":      e.TickerNSE,
		"ticker_bse":      e.TickerBSE,
		"is_listed":       e.ISListed,
		"exchange":        e.Exchange,
		"exchange_status": e.ExchangeStatus,
		"sector":          e.Sector,
		"industry":        e.Industry,
		"website":         e.Website,
	}
	for k, v := range map[string]string{"canonical_name": e.CanonicalName, "normalized_name": e.NormalizedName, "ticker_nse": e.TickerNSE, "ticker_bse": e.TickerBSE, "exchange": e.Exchange, "exchange_status": e.ExchangeStatus, "sector": e.Sector, "industry": e.Industry, "website": e.Website} {
		if v == "" {
			delete(p, k)
		}
	}
	return NewNode("CorporateEntity", GID("ENTITY", "v", e.ID), "entities", e.ID, syncedAtMS, p)
}

// AliasNode projects an entity_aliases row.
func AliasNode(a AliasRecord, syncedAtMS int64) (Node, error) {
	if _, err := uuid.Parse(a.ID); err != nil {
		return Node{}, fmt.Errorf("alias id %q: %w", a.ID, err)
	}
	p := map[string]any{"alias_name": a.AliasName, "normalized_alias": a.NormalizedAlias, "source": a.Source}
	for k, v := range map[string]string{"alias_name": a.AliasName, "normalized_alias": a.NormalizedAlias, "source": a.Source} {
		if v == "" {
			delete(p, k)
		}
	}
	return NewNode("Alias", GID("ALIAS", "v", a.ID), "entity_aliases", a.ID, syncedAtMS, p)
}

// GstNode projects an entity_gst row.
func GstNode(g GstRecord, syncedAtMS int64) (Node, error) {
	p := map[string]any{"gstin": g.GSTIN, "company_name": g.CompanyName, "state": g.State}
	for k, v := range map[string]string{"gstin": g.GSTIN, "company_name": g.CompanyName, "state": g.State} {
		if v == "" {
			delete(p, k)
		}
	}
	return NewNode("GstRegistration", GID("GST", "v", strconv.FormatInt(g.ID, 10)), "entity_gst", strconv.FormatInt(g.ID, 10), syncedAtMS, p)
}

// AliasEdge links an alias to its source entity.
func AliasEdge(entity, alias Node, sourcePK string, syncedAtMS int64) (Edge, error) {
	return NewEdge("HAS_ALIAS", entity, alias, "entity_aliases", sourcePK, syncedAtMS, nil)
}

// GstEdge links a GST registration to its source entity.
func GstEdge(entity, gst Node, sourcePK string, syncedAtMS int64) (Edge, error) {
	return NewEdge("HAS_GST_REGISTRATION", entity, gst, "entity_gst", sourcePK, syncedAtMS, nil)
}

// CoBidObservationPair projects one pair without requiring the complete
// participant set in memory. This is used by bounded participant batches.
func CoBidObservationPair(left, right BidRecord, syncedAtMS int64) (Node, Edge, Edge, error) {
	if left.TenderID != right.TenderID {
		return Node{}, Edge{}, Edge{}, fmt.Errorf("co-bid pair has different tenders: %d and %d", left.TenderID, right.TenderID)
	}
	if left.ID > right.ID {
		left, right = right, left
	}
	gid := GID("COBID", "v", fmt.Sprintf("%d:%d:%d", left.TenderID, left.ID, right.ID))
	on, err := NewNode("CoBidObservation", gid, "tender_participants", fmt.Sprintf("%d:%d:%d", left.ID, right.ID, left.TenderID), syncedAtMS, map[string]any{
		"tender_pk": left.TenderID,
		"left_pk":   left.ID,
		"right_pk":  right.ID,
		"left_gid":  GID("BID", "v", strconv.FormatInt(left.ID, 10)),
		"right_gid": GID("BID", "v", strconv.FormatInt(right.ID, 10)),
	})
	if err != nil {
		return Node{}, Edge{}, Edge{}, err
	}
	ln, err := BidNode(left, syncedAtMS)
	if err != nil {
		return Node{}, Edge{}, Edge{}, err
	}
	rn, err := BidNode(right, syncedAtMS)
	if err != nil {
		return Node{}, Edge{}, Edge{}, err
	}
	le, err := NewEdge("CO_BID_IN", ln, on, "tender_participants", strconv.FormatInt(left.ID, 10), syncedAtMS, map[string]any{
		"observation_tender_pk": left.TenderID,
		"observation_left_pk":   left.ID,
		"observation_right_pk":  right.ID,
		"derivation_method":     "same_tender_participant_pair",
	})
	if err != nil {
		return Node{}, Edge{}, Edge{}, err
	}
	re, err := NewEdge("CO_BID_IN", rn, on, "tender_participants", strconv.FormatInt(right.ID, 10), syncedAtMS, map[string]any{
		"observation_tender_pk": left.TenderID,
		"observation_left_pk":   left.ID,
		"observation_right_pk":  right.ID,
		"derivation_method":     "same_tender_participant_pair",
	})
	if err != nil {
		return Node{}, Edge{}, Edge{}, err
	}
	return on, le, re, nil
}
