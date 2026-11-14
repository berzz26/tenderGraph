package intel

import "testing"

func TestCandidateIdentityIsDeterministic(t *testing.T) {
	gid := CandidateGID("VENDOR_CORPORATE_ENTITY", "VENDOR:cp:1429", "ENTITY:v:abc")
	if gid != "CANDIDATE:VENDOR_CORPORATE_ENTITY:VENDOR:cp:1429:ENTITY:v:abc" {
		t.Fatalf("candidate gid=%q", gid)
	}
	if CandidateID(gid) != CandidateID(gid) || CandidateID(gid) < 0 {
		t.Fatalf("candidate id is not deterministic/non-negative: %d", CandidateID(gid))
	}
	if AnalystProjectionGID(CandidateID(gid), 1) == AnalystProjectionGID(CandidateID(gid), 2) {
		t.Fatal("projection versions must have distinct identities")
	}
}

func TestDecisionTransitions(t *testing.T) {
	valid := []struct {
		previous string
		version  int
		next     string
	}{
		{"PENDING", 0, "CONFIRMED"},
		{"PENDING", 0, "REJECTED"},
		{"PENDING", 0, "NEEDS_REVIEW"},
		{"CONFIRMED", 1, "NEEDS_REVIEW"},
		{"CONFIRMED", 1, "REJECTED"},
		{"REJECTED", 1, "NEEDS_REVIEW"},
		{"REJECTED", 1, "CONFIRMED"},
		{"NEEDS_REVIEW", 2, "CONFIRMED"},
	}
	for _, tt := range valid {
		if err := validateDecisionTransition(tt.previous, tt.version, tt.next); err != nil {
			t.Errorf("%s/%d -> %s rejected: %v", tt.previous, tt.version, tt.next, err)
		}
	}
	if err := validateDecisionTransition("CONFIRMED", 1, "CONFIRMED"); err == nil {
		t.Fatal("same-state decision should be rejected")
	}
	if err := validateDecisionTransition("PENDING", 0, "UNKNOWN"); err == nil {
		t.Fatal("unknown state should be rejected")
	}
}
