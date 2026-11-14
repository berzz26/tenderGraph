package ingest

import (
	"strings"
	"testing"
	"time"

	"vyuha/api/internal/projection"
)

func TestRegistryDependencyOrder(t *testing.T) {
	r := ApprovedRegistry()
	order := r.Order()
	position := map[string]int{}
	for i, item := range order {
		position[item.Name] = i
	}
	for _, item := range r.All() {
		for _, dep := range item.Dependencies {
			if position[dep] >= position[item.Name] {
				t.Fatalf("%s must follow %s: %#v", item.Name, dep, order)
			}
		}
	}
	if len(order) != 7 {
		t.Fatalf("registry order length=%d", len(order))
	}
}

func TestCursorCompoundOrderingAndLookback(t *testing.T) {
	tm := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	a := Cursor{Time: &tm, PK: "10"}
	b := Cursor{Time: &tm, PK: "11"}
	if !a.Less(b) || a.Equal(b) {
		t.Fatal("compound cursor ordering failed")
	}
	if !(Cursor{Time: &tm, PK: "8935"}).Less(Cursor{Time: &tm, PK: "12623"}) {
		t.Fatal("numeric primary-key ordering failed")
	}
	l := b.WithLookback(time.Hour)
	if !l.Time.Equal(tm.Add(-time.Hour)) || l.PK != "" {
		t.Fatalf("lookback=%s", l.String())
	}
}

func TestBoundsKeepEqualTimestampsOrderedByPrimaryKey(t *testing.T) {
	tm := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	spec := DatasetSpec{Name: "x", PKColumn: "id", PKType: "bigint", Strategy: StrategyTimestampPK, TimeColumn: "updated_at"}
	where, args := bounds(spec, Cursor{Time: &tm, PK: "10"}, Cursor{Time: &tm, PK: "20"})
	if len(args) != 4 || where == "" {
		t.Fatalf("where=%q args=%#v", where, args)
	}
	if !strings.Contains(where, "id > $2::bigint") || !strings.Contains(where, "id <= $4::bigint") {
		t.Fatalf("compound bounds=%q", where)
	}
}

func TestDeduplicateBatch(t *testing.T) {
	n1, _ := projection.NewNode("Tender", "TENDER:v:1", "tenders", "1", 1, nil)
	n2, _ := projection.NewNode("Tender", "TENDER:v:1", "tenders", "1", 2, nil)
	e1, _ := projection.NewEdge("ISSUED_BY", n1, n1, "tenders", "1", 1, nil)
	e2, _ := projection.NewEdge("ISSUED_BY", n1, n1, "tenders", "1", 2, nil)
	b, err := deduplicate(projection.Batch{Nodes: []projection.Node{n1, n2}, Edges: []projection.Edge{e1, e2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Nodes) != 1 || len(b.Edges) != 1 {
		t.Fatalf("nodes=%d edges=%d", len(b.Nodes), len(b.Edges))
	}
}

func TestDeduplicateRejectsIDCollision(t *testing.T) {
	n1 := projection.Node{Label: "Tender", GID: "TENDER:v:1", ID: 1}
	n2 := projection.Node{Label: "Tender", GID: "TENDER:v:2", ID: 1}
	_, err := deduplicate(projection.Batch{Nodes: []projection.Node{n1, n2}})
	if err == nil {
		t.Fatal("expected collision")
	}
	if _, ok := err.(*projection.CollisionError); !ok {
		t.Fatalf("error type=%T", err)
	}
}
