package intel

import "testing"

func TestParseExactDecimal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"grouped rupees", "1,00,29,253.65", "10029253.65", true},
		{"spaces", "  -42.500 ", "-42.500", true},
		{"invalid", "12 crore", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, ok := ParseExactDecimal(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ParseExactDecimal(%q)=(%q,%v), want (%q,%v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDecimalTotalPreservesExactValues(t *testing.T) {
	got, valid := decimalTotal([]string{"1,00,29,253.65", "746.35", "not-a-number"})
	if got != "10030000.00" || valid != 2 {
		t.Fatalf("decimalTotal=%q,%d", got, valid)
	}
}

func TestNormalizeComparableIsConservativeAndDeterministic(t *testing.T) {
	if got := normalizeComparable("A.B & C Pvt. Ltd."); got != "abc pvt ltd" && got != "abcpvtltd" {
		t.Fatalf("unexpected normalized value %q", got)
	}
	if normalizeComparable("ABC") != normalizeComparable("abc") {
		t.Fatal("normalization is not case-insensitive")
	}
}
