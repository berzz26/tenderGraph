package projection

import (
	"reflect"
	"testing"
)

func TestNumericIDVectors(t *testing.T) {
	tests := map[string]int64{
		"":                       5472609002491880229,
		"TENDER:v:1042":          3565830287611912216,
		"BID:v:7781":             6457453924866997687,
		"COBID:v:1042:7781:7803": 4649989190860415346,
		"GOVORG:org:national highways and infrastructure development corporation": 5033129105100022286,
	}
	for gid, want := range tests {
		if got := NumericID(gid); got != want {
			t.Errorf("NumericID(%q)=%d, want %d", gid, got, want)
		}
		if got := NumericID(gid); got < 0 {
			t.Errorf("NumericID(%q) is negative: %d", gid, got)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	got := NormalizeName("M/s. SUN & SHINE OVERSEAS PVT LTD")
	if got != "m s sun and shine overseas" {
		t.Fatalf("normalized name=%q", got)
	}
}

func TestParseMoney(t *testing.T) {
	exact, numeric, ok := ParseMoney("1,00,29,253.65")
	if !ok || exact != "10029253.65" || numeric != 10029253.65 {
		t.Fatalf("money=%q %v %v", exact, numeric, ok)
	}
	if _, _, ok := ParseMoney("not-money"); ok {
		t.Fatal("invalid money parsed")
	}
}

func TestWinnerEvidenceDoesNotUseWinnerFlagAlone(t *testing.T) {
	b := BidRecord{ID: 1, BidNumber: "B-1", ParticipantNormalized: "alpha", IsWinner: true}
	a := AwardRecord{ID: 2, AwardeeNormalized: "beta"}
	if _, ok := WinnerEvidenceFor(b, a); ok {
		t.Fatal("winner flag alone created positive evidence")
	}
	b.BidNumber = "B-2"
	a.WinningBidNumber = "B-2"
	e, ok := WinnerEvidenceFor(b, a)
	if !ok || e.Kind != "BID_NUMBER_MATCH" {
		t.Fatalf("evidence=%+v ok=%v", e, ok)
	}
}

func TestCoBidObservationDeterminism(t *testing.T) {
	bids := []BidRecord{{ID: 80, TenderID: 16234, BidNumber: "b"}, {ID: 79, TenderID: 16234, BidNumber: "a"}, {ID: 81, TenderID: 16234, BidNumber: "c"}}
	a, ae, err := BuildCoBidObservations(bids, 123)
	if err != nil {
		t.Fatal(err)
	}
	b, be, err := BuildCoBidObservations([]BidRecord{bids[2], bids[0], bids[1]}, 123)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(ae, be) {
		t.Fatal("co-bid projection changed with input order")
	}
	if len(a) != 3 || len(ae) != 6 {
		t.Fatalf("nodes=%d edges=%d", len(a), len(ae))
	}
	if a[0].GID != "COBID:v:16234:79:80" {
		t.Fatalf("first observation gid=%s", a[0].GID)
	}
}

func TestVerticalSliceSemanticsAndProvenance(t *testing.T) {
	tender := TenderRecord{ID: 16234, ExternalID: "2026_NHIDC_909174_1", Organization: "National Highways and Infrastructure Development Corporation", LifecycleStage: "aoc", TenderValue: "1,00,29,253.65"}
	bids := []BidRecord{
		{ID: 79, TenderID: 16234, BidNumber: "3421641", ParticipantName: "DENZON CONSTRUCTION", ParticipantNormalized: "denzon construction"},
		{ID: 81, TenderID: 16234, BidNumber: "3421499", ParticipantName: "SUN SHINE OVERSEAS PVT LTD", ParticipantNormalized: "sun shine overseas", IsWinner: true, FinancialRank: "L1", FinancialBidValue: "1,00,29,253.65"},
	}
	vendor := &VendorRecord{ID: 1113, BusinessName: "SUN SHINE OVERSEAS PVT LTD", GSTIN: "19AACCS3168A1ZK"}
	award := &AwardRecord{ID: 16234, TenderID: 16234, AwardeeName: "SUN SHINE OVERSEAS PVT LTD", AwardeeNormalized: "sun shine overseas", AwardedValue: "10,029,253.65", WinningBidNumber: "3421499", OutcomeClassification: "L1_SELECTED_AOC_PENDING"}
	b, err := BuildVerticalSlice(tender, bids, vendor, award, 123)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Nodes) != 7 || len(b.Edges) != 8 {
		t.Fatalf("nodes=%d edges=%d", len(b.Nodes), len(b.Edges))
	}
	var won, contractor, submitted, cobid int
	for _, e := range b.Edges {
		switch e.Type {
		case "WON":
			won++
			if e.Props["evidence_kind"] != "BID_NUMBER_MATCH" {
				t.Errorf("bad winner evidence: %#v", e.Props)
			}
		case "CONTRACTOR_ON_RECORD":
			contractor++
		case "SUBMITTED_BID":
			submitted++
		case "CO_BID_IN":
			cobid++
		}
	}
	if won != 1 || contractor != 1 || submitted != 2 || cobid != 2 {
		t.Fatalf("won=%d contractor=%d submitted=%d cobid=%d", won, contractor, submitted, cobid)
	}
	for _, n := range b.Nodes {
		for _, key := range []string{"id", "gid", "id_algo_v", "src_table", "src_pk", "synced_at_ms"} {
			if _, ok := n.Props[key]; !ok {
				t.Errorf("%s missing node provenance %s", n.GID, key)
			}
		}
	}
}

func TestCollisionValidation(t *testing.T) {
	n, err := NewNode("Tender", "TENDER:v:1", "tenders", "1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateNoCollisions(map[int64]string{n.ID: "TENDER:v:other"}, []Node{n}); err == nil {
		t.Fatal("expected existing-node collision")
	}
	if err := ValidateNoCollisions(map[int64]string{n.ID: n.GID}, []Node{n}); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityCandidateRequiresEvidenceAndStaysCandidate(t *testing.T) {
	left, err := NewNode("Vendor", "VENDOR:cp:1", "contractor_v2", "1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewNode("CorporateEntity", "ENTITY:v:e1", "entities", "e1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewIdentityCandidate(left, right, "GSTIN_EXACT", []string{"EVID:v:42"}, "entity_gst+contractor_v2", "1:42", 1)
	if err != nil {
		t.Fatal(err)
	}
	if e.Type != "IDENTIFIED_AS" || e.Props["status"] != "CANDIDATE" || e.Props["evidence_gids"] != "[\"EVID:v:42\"]" {
		t.Fatalf("candidate edge=%#v", e)
	}
	if _, err := NewIdentityCandidate(left, right, "GSTIN_EXACT", nil, "entity_gst", "42", 1); err == nil {
		t.Fatal("expected missing-evidence error")
	}
}
