package graph

import (
	"strings"
	"testing"
)

// TestAPIErrorNeverContainsToken: the token only travels in the Authorization
// header; errors must never echo it back.
func TestAPIErrorNeverContainsToken(t *testing.T) {
	const token = "secret-bearer-token-abc"
	e := &APIError{Status: 500, Code: "internal", Message: "boom", Raw: "boom"}
	if strings.Contains(e.Error(), token) {
		t.Error("APIError must not contain the token")
	}
}

func TestTruncate(t *testing.T) {
	s := strings.Repeat("x", 400)
	got := truncate(s, 300)
	if len(got) != 303 { // 300 bytes + 3-byte ellipsis rune
		t.Errorf("truncate len = %d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated string should end with ellipsis")
	}
}
