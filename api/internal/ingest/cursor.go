package ingest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cursor is the durable compound key for a dataset scan.
type Cursor struct {
	Time *time.Time
	PK   string
}

func (c Cursor) Empty() bool { return c.Time == nil && c.PK == "" }

func (c Cursor) String() string {
	if c.Time == nil {
		return c.PK
	}
	return fmt.Sprintf("%s|%s", c.Time.UTC().Format(time.RFC3339Nano), c.PK)
}

func (c Cursor) Less(other Cursor) bool {
	if c.Time == nil || other.Time == nil {
		return comparePK(c.PK, other.PK) < 0
	}
	if c.Time.Equal(*other.Time) {
		return comparePK(c.PK, other.PK) < 0
	}
	return c.Time.Before(*other.Time)
}

func (c Cursor) Equal(other Cursor) bool {
	if c.Time == nil || other.Time == nil {
		return c.Time == nil && other.Time == nil && c.PK == other.PK
	}
	return c.Time.Equal(*other.Time) && c.PK == other.PK
}

func (c Cursor) WithLookback(d time.Duration) Cursor {
	if c.Time == nil || d <= 0 {
		return c
	}
	t := c.Time.Add(-d)
	return Cursor{Time: &t, PK: ""}
}

func maxCursor(a, b Cursor) Cursor {
	if a.Empty() {
		return b
	}
	if b.Empty() || b.Less(a) {
		return a
	}
	return b
}

func comparePK(a, b string) int {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}
