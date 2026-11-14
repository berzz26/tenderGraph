package projection

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"vyuha/api/internal/graph"
)

// HydraWriter sends projection batches through the existing verified HTTP
// client. It intentionally uses separate node and edge requests because
// HydraDB has auto-commit writes and no multi-statement transactions.
type HydraWriter struct {
	Client *graph.HTTPClient
}

// WriteReport records the requests made by a projection write.
type WriteReport struct {
	NodeRequests int
	EdgeRequests int
	Nodes        int
	Edges        int
}

// CheckCollisions reads only the gid property at each outgoing id. A missing
// node is safe; a present node without the expected gid is treated as a
// collision rather than overwritten.
func (w HydraWriter) CheckCollisions(ctx context.Context, nodes []Node) error {
	if w.Client == nil {
		return fmt.Errorf("hydra writer: nil client")
	}
	seen := make(map[int64]string, len(nodes))
	if err := ValidateNoCollisions(nil, nodes); err != nil {
		return err
	}
	for _, n := range nodes {
		if prior, ok := seen[n.ID]; ok && prior == n.GID {
			continue
		}
		seen[n.ID] = n.GID
		res, err := w.Client.DoQuery(ctx, "MATCH (n {id: $id}) RETURN n.gid AS gid", map[string]any{"id": n.ID})
		if err != nil {
			return fmt.Errorf("check collision for %s: %w", n.GID, err)
		}
		if res.Count() == 0 {
			continue
		}
		old, err := res.String(0, 0)
		if err != nil || old != n.GID {
			return &CollisionError{ID: n.ID, ExistingGID: old, IncomingGID: n.GID}
		}
	}
	return nil
}

// EnsureAbsent is a safety gate for disposable demonstrations. It prevents a
// Phase 2 test from overwriting or later deleting graph data that predates the
// test run.
func (w HydraWriter) EnsureAbsent(ctx context.Context, nodes []Node) error {
	if w.Client == nil {
		return fmt.Errorf("hydra writer: nil client")
	}
	seen := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		res, err := w.Client.DoQuery(ctx, "MATCH (n {id: $id}) RETURN n.gid AS gid", map[string]any{"id": n.ID})
		if err != nil {
			return fmt.Errorf("check pre-existing node %s: %w", n.GID, err)
		}
		if res.Count() > 0 {
			old, _ := res.String(0, 0)
			return fmt.Errorf("refusing to use pre-existing graph node: id=%d gid=%q expected=%q", n.ID, old, n.GID)
		}
	}
	return nil
}

// Write sends nodes first and edges second. Replaying the same Batch is safe
// for nodes by id and for row-keyed edges using deterministic relationship
// MERGE ids. The Phase 2 live probe verified that this MERGE form collapses
// duplicate requests; no undocumented relationship constraint is assumed.
func (w HydraWriter) Write(ctx context.Context, batch Batch) (WriteReport, error) {
	var report WriteReport
	if w.Client == nil {
		return report, fmt.Errorf("hydra writer: nil client")
	}
	if err := w.CheckCollisions(ctx, batch.Nodes); err != nil {
		return report, err
	}

	for _, group := range groupNodes(batch.Nodes) {
		rows := make([]map[string]any, 0, len(group.nodes))
		for _, n := range group.nodes {
			row := cloneMap(n.Props)
			row["id"] = n.ID
			rows = append(rows, row)
		}
		q, err := nodeBatchQuery(group.label, group.keys)
		if err != nil {
			return report, err
		}
		if _, err := w.Client.DoQuery(ctx, q, map[string]any{"rows": rows}); err != nil {
			return report, fmt.Errorf("write %s nodes: %w", group.label, err)
		}
		report.NodeRequests++
		report.Nodes += len(rows)
	}

	for _, group := range groupEdges(batch.Edges) {
		rows := make([]map[string]any, 0, len(group.edges))
		for _, e := range group.edges {
			row := cloneMap(e.Props)
			row["src"] = e.FromID
			row["dst"] = e.ToID
			row["rid"] = e.RID
			rows = append(rows, row)
		}
		q, err := edgeBatchQuery(group)
		if err != nil {
			return report, err
		}
		if _, err := w.Client.DoQuery(ctx, q, map[string]any{"rows": rows}); err != nil {
			return report, fmt.Errorf("write %s edges: %w", group.typ, err)
		}
		report.EdgeRequests++
		report.Edges += len(rows)
	}
	return report, nil
}

// DeleteAndVerify removes only the supplied ids and confirms each is absent.
// It is intended for the disposable vertical slice, not production deletion.
func (w HydraWriter) DeleteAndVerify(ctx context.Context, nodes []Node) error {
	if w.Client == nil {
		return fmt.Errorf("hydra writer: nil client")
	}
	rows := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, map[string]any{"id": n.ID})
	}
	if len(rows) > 0 {
		if _, err := w.Client.DoQuery(ctx, "UNWIND $rows AS row MATCH (n {id: row.id}) DETACH DELETE n", map[string]any{"rows": rows}); err != nil {
			return fmt.Errorf("delete vertical slice: %w", err)
		}
	}
	for _, n := range nodes {
		res, err := w.Client.DoQuery(ctx, "MATCH (n {id: $id}) RETURN count(*) AS c", map[string]any{"id": n.ID})
		if err != nil {
			return fmt.Errorf("verify delete %s: %w", n.GID, err)
		}
		if res.Count() != 1 {
			return fmt.Errorf("verify delete %s: expected one count row, got %d", n.GID, res.Count())
		}
		count, err := res.Int64(0, 0)
		if err != nil || count != 0 {
			return fmt.Errorf("verify delete %s: count=%d err=%v", n.GID, count, err)
		}
	}
	return nil
}

type nodeGroup struct {
	label string
	keys  []string
	nodes []Node
}
type edgeGroup struct {
	typ, fromLabel, toLabel string
	keys                    []string
	edges                   []Edge
}

func groupNodes(nodes []Node) []nodeGroup {
	type bucket struct {
		label string
		keys  []string
		nodes []Node
	}
	m := map[string]*bucket{}
	for _, n := range nodes {
		keys := propertyKeys(n.Props, "id")
		key := n.Label + "\x00" + strings.Join(keys, "\x00")
		b := m[key]
		if b == nil {
			b = &bucket{label: n.Label, keys: keys}
			m[key] = b
		}
		b.nodes = append(b.nodes, n)
	}
	out := make([]nodeGroup, 0, len(m))
	for _, b := range m {
		out = append(out, nodeGroup{b.label, b.keys, b.nodes})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].label < out[j].label || out[i].label == out[j].label && strings.Join(out[i].keys, ",") < strings.Join(out[j].keys, ",")
	})
	return out
}

func groupEdges(edges []Edge) []edgeGroup {
	type bucket struct {
		typ, fromLabel, toLabel string
		keys                    []string
		edges                   []Edge
	}
	m := map[string]*bucket{}
	for _, e := range edges {
		keys := propertyKeys(e.Props, "id")
		key := e.Type + "\x00" + e.FromLabel + "\x00" + e.ToLabel + "\x00" + strings.Join(keys, "\x00")
		b := m[key]
		if b == nil {
			b = &bucket{typ: e.Type, fromLabel: e.FromLabel, toLabel: e.ToLabel, keys: keys}
			m[key] = b
		}
		b.edges = append(b.edges, e)
	}
	out := make([]edgeGroup, 0, len(m))
	for _, b := range m {
		out = append(out, edgeGroup{b.typ, b.fromLabel, b.toLabel, b.keys, b.edges})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].typ < out[j].typ || out[i].typ == out[j].typ && strings.Join(out[i].keys, ",") < strings.Join(out[j].keys, ",")
	})
	return out
}

func propertyKeys(props map[string]any, skip ...string) []string {
	ignore := map[string]bool{}
	for _, k := range skip {
		ignore[k] = true
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		if !ignore[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func nodeBatchQuery(label string, keys []string) (string, error) {
	if err := validateIdentifier(label); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, "n:"+label)
	for _, k := range keys {
		if err := validateIdentifier(k); err != nil {
			return "", err
		}
		parts = append(parts, "n."+k+" = row."+k)
	}
	return "UNWIND $rows AS row MERGE (n {id: row.id}) SET " + strings.Join(parts, ", "), nil
}

func edgeBatchQuery(g edgeGroup) (string, error) {
	for _, s := range []string{g.typ, g.fromLabel, g.toLabel} {
		if err := validateIdentifier(s); err != nil {
			return "", err
		}
	}
	setParts := make([]string, 0, len(g.keys))
	for _, k := range g.keys {
		if err := validateIdentifier(k); err != nil {
			return "", err
		}
		setParts = append(setParts, "r."+k+" = row."+k)
	}
	query := "UNWIND $rows AS row MATCH (a:" + g.fromLabel + " {id: row.src}), (b:" + g.toLabel + " {id: row.dst}) MERGE (a)-[r:" + g.typ + " {id: row.rid}]->(b)"
	if len(setParts) > 0 {
		query += " SET " + strings.Join(setParts, ", ")
	}
	return query, nil
}

// FormatID is useful to keep source PK values visibly textual in provenance.
func FormatID(id int64) string { return strconv.FormatInt(id, 10) }
