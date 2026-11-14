package intel

import "time"

// TimeWindow is an inclusive lower/exclusive upper observation-time window.
// A nil bound means unbounded on that side.
type TimeWindow struct {
	From *time.Time
	To   *time.Time
}

func (w TimeWindow) Contains(value time.Time) bool {
	if w.From != nil && value.Before(*w.From) {
		return false
	}
	if w.To != nil && !value.Before(*w.To) {
		return false
	}
	return true
}

// Jaccard returns intersection / union for two tender-participation sets.
// Empty sets have similarity zero; this avoids manufacturing similarity for
// two bidders with no observed participation.
func Jaccard(intersection, left, right int) float64 {
	union := left + right - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// Coverage is the fraction of one bidder's observed tenders shared with the
// other bidder. The denominator is deliberately explicit in responses.
func Coverage(shared, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(shared) / float64(total)
}
