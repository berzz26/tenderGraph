// Package ingest implements bounded, sequential PostgreSQL -> HydraDB
// synchronization. It owns the control-plane workflow but delegates graph
// identity and source-row projection to internal/projection.
package ingest

import (
	"sort"
	"time"
)

type Strategy string

const (
	StrategyTimestampPK Strategy = "timestamp_pk"
	StrategyCreatedPK   Strategy = "created_pk"
	StrategyPK          Strategy = "pk"
)

// DatasetSpec is the explicit ingestion registry entry for one approved source.
type DatasetSpec struct {
	Name         string
	SourceTable  string
	Strategy     Strategy
	PKColumn     string
	PKType       string // bigint, integer, uuid
	TimeColumn   string
	Lookback     time.Duration
	Dependencies []string
	NodeLabels   []string
	EdgeTypes    []string
	Description  string
	BatchSize    int
}

// Registry is a deterministic dataset registry.
type Registry struct {
	items map[string]DatasetSpec
}

func ApprovedRegistry() Registry {
	items := []DatasetSpec{
		{Name: "contractor_v2", SourceTable: "contractor_v2", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "bigint", TimeColumn: "updated_at", Lookback: time.Hour, NodeLabels: []string{"Vendor"}, Description: "Vendor registry nodes"},
		{Name: "entities", SourceTable: "entities", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "uuid", TimeColumn: "updated_at", Lookback: time.Hour, NodeLabels: []string{"CorporateEntity"}, Description: "CorporateEntity nodes"},
		{Name: "entity_aliases", SourceTable: "entity_aliases", Strategy: StrategyCreatedPK, PKColumn: "id", PKType: "uuid", TimeColumn: "created_at", Lookback: 0, Dependencies: []string{"entities"}, NodeLabels: []string{"Alias"}, EdgeTypes: []string{"HAS_ALIAS"}, Description: "Alias nodes and entity links"},
		{Name: "entity_gst", SourceTable: "entity_gst", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "bigint", TimeColumn: "updated_at", Lookback: time.Hour, Dependencies: []string{"entities"}, NodeLabels: []string{"GstRegistration"}, EdgeTypes: []string{"HAS_GST_REGISTRATION"}, Description: "GST registration nodes and entity links"},
		{Name: "tenders", SourceTable: "tenders", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "bigint", TimeColumn: "updated_at", Lookback: time.Hour, Dependencies: []string{"contractor_v2"}, NodeLabels: []string{"Tender", "GovernmentOrganization"}, EdgeTypes: []string{"ISSUED_BY", "CONTRACTOR_ON_RECORD"}, Description: "Tender, organization and contractor assignment"},
		{Name: "tender_participants", SourceTable: "tender_participants", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "bigint", TimeColumn: "updated_at", Lookback: time.Hour, Dependencies: []string{"tenders"}, NodeLabels: []string{"Bid", "CoBidObservation"}, EdgeTypes: []string{"SUBMITTED_BID", "CO_BID_IN"}, Description: "Bid rows and co-bid observations"},
		{Name: "tender_extracted_meta", SourceTable: "tender_extracted_meta", Strategy: StrategyTimestampPK, PKColumn: "id", PKType: "bigint", TimeColumn: "updated_at", Lookback: time.Hour, Dependencies: []string{"tenders", "tender_participants"}, NodeLabels: []string{"Award"}, EdgeTypes: []string{"HAS_AWARD", "WON"}, Description: "Award rows and winner evidence"},
	}
	m := make(map[string]DatasetSpec, len(items))
	for _, item := range items {
		item.BatchSize = 500
		m[item.Name] = item
	}
	return Registry{items: m}
}

func (r Registry) Get(name string) (DatasetSpec, bool) {
	item, ok := r.items[name]
	return item, ok
}

func (r Registry) All() []DatasetSpec {
	out := make([]DatasetSpec, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r Registry) Order() []DatasetSpec {
	all := r.All()
	ordered := make([]DatasetSpec, 0, len(all))
	seen := map[string]bool{}
	for len(ordered) < len(all) {
		progress := false
		for _, item := range all {
			if seen[item.Name] {
				continue
			}
			ready := true
			for _, dep := range item.Dependencies {
				if !seen[dep] {
					ready = false
					break
				}
			}
			if ready {
				seen[item.Name] = true
				ordered = append(ordered, item)
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return ordered
}
