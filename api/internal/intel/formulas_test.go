package intel

import (
	"testing"
	"time"
)

func TestJaccard(t *testing.T) {
	if got := Jaccard(3, 5, 7); got != 3.0/9.0 {
		t.Fatalf("jaccard=%v", got)
	}
	if got := Jaccard(0, 0, 0); got != 0 {
		t.Fatalf("empty jaccard=%v", got)
	}
}

func TestCoverage(t *testing.T) {
	if got := Coverage(2, 8); got != 0.25 {
		t.Fatalf("coverage=%v", got)
	}
	if got := Coverage(2, 0); got != 0 {
		t.Fatalf("zero coverage=%v", got)
	}
}

func TestTimeWindow(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	w := TimeWindow{From: &from, To: &to}
	if !w.Contains(from) || w.Contains(to) || w.Contains(from.Add(-time.Second)) {
		t.Fatal("inclusive/exclusive window failed")
	}
}
