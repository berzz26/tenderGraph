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
	Client    *graph.HTTPClient
	BatchSize int
}

// WriteReport records the requests made by a projection write.
type WriteReport struct {
	NodeRequests int
	EdgeRequests int
	Nodes        int
	Edges        int
}

// EdgeVerification is an endpoint-level verification result. HydraDB's
// verified runtime does not expose relationship variables in RETURN, so the
// result intentionally identifies edges by type and endpoint pair. The
// relationship id is still used for writes and replay, but cannot be read
// back from this runtime.
type EdgeVerification struct {
	Expected   int
	Actual     int
	Missing    int
	Duplicate  int
	Unexpected int
}

// EdgeScope identifies the endpoint side whose existing relationships may
// have changed in a source batch. It is deliberately bounded to the source
// records in the current batch; it is not a graph-wide delete operation.
type EdgeScope struct {
	Type       string
	FromLabel  string
	ToLabel    string
	EndpointID int64
	Side       string // "from" or "to"
}

// VerifyNodes checks an expected node inventory in bounded id batches. It does
// not scan an entire label, which is important for HydraDB runtimes where a
// label-wide ordered scan can exceed the HTTP query budget.
func (w HydraWriter) VerifyNodes(ctx context.Context, label string, expected map[int64]string) (missing, invalid int, err error) {
	if w.Client == nil {
		return 0, 0, fmt.Errorf("hydra writer: nil client")
	}
	if err := validateIdentifier(label); err != nil {
		return 0, 0, err
	}
	ids := make([]int64, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	const readChunk = 500
	for start := 0; start < len(ids); start += readChunk {
		end := start + readChunk
		if end > len(ids) {
			end = len(ids)
		}
		params := map[string]any{}
		predicates := make([]string, 0, end-start)
		found := map[int64]bool{}
		for i, id := range ids[start:end] {
			key := fmt.Sprintf("id%d", i)
			predicates = append(predicates, "n.id = $"+key)
			params[key] = id
		}
		q := "MATCH (n:" + label + ") WHERE " + strings.Join(predicates, " OR ") + " RETURN n.id AS id, n.gid AS gid, n.src_table AS src_table, n.src_pk AS src_pk"
		res, queryErr := w.Client.DoQuery(ctx, q, params)
		if queryErr != nil {
			return missing, invalid, fmt.Errorf("verify %s node batch: %w", label, queryErr)
		}
		for i := range res.Rows {
			id, idErr := res.Int64(i, 0)
			gid, gidErr := res.String(i, 1)
			_, tableErr := res.String(i, 2)
			_, pkErr := res.String(i, 3)
			if idErr != nil || gidErr != nil || tableErr != nil || pkErr != nil || gid == "" || expected[id] != gid {
				invalid++
				continue
			}
			found[id] = true
		}
		missing += (end - start) - len(found)
	}
	return missing, invalid, nil
}

// InventoryEdges walks one typed relationship inventory using HydraDB's
// consuming HTTP cursor. The callback receives endpoint identities only;
// relationship properties cannot be returned by the verified runtime because
// relationship variables are not supported in RETURN.
func (w HydraWriter) InventoryEdges(ctx context.Context, fromLabel, typ, toLabel string, visit func(int64, int64) error) (int, error) {
	if w.Client == nil {
		return 0, fmt.Errorf("hydra writer: nil client")
	}
	for _, value := range []string{fromLabel, typ, toLabel} {
		if err := validateIdentifier(value); err != nil {
			return 0, err
		}
	}
	q := "MATCH (a:" + fromLabel + ")-[:" + typ + "]->(b:" + toLabel + ") RETURN a.id AS src, b.id AS dst ORDER BY a.id, b.id"
	res, err := w.Client.DoQuery(ctx, q, nil)
	if err != nil {
		return 0, fmt.Errorf("inventory %s edges: %w", typ, err)
	}
	count := 0
	for {
		for i := range res.Rows {
			src, err := res.Int64(i, 0)
			if err != nil {
				return count, err
			}
			dst, err := res.Int64(i, 1)
			if err != nil {
				return count, err
			}
			if err := visit(src, dst); err != nil {
				return count, err
			}
			count++
		}
		if res.NextCursor == nil || res.QueryID == "" {
			break
		}
		res, err = w.Client.QueryPage(ctx, q, nil, res.QueryID, *res.NextCursor)
		if err != nil {
			return count, fmt.Errorf("inventory %s edges page: %w", typ, err)
		}
	}
	return count, nil
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
	const readChunk = 500
	for _, n := range nodes {
		if prior, ok := seen[n.ID]; ok {
			if prior != n.GID {
				return &CollisionError{ID: n.ID, ExistingGID: prior, IncomingGID: n.GID}
			}
			continue
		}
		seen[n.ID] = n.GID
	}
	unique := make([]Node, 0, len(seen))
	added := make(map[int64]bool, len(seen))
	for _, n := range nodes {
		if !added[n.ID] {
			added[n.ID] = true
			unique = append(unique, n)
		}
	}
	for start := 0; start < len(unique); start += readChunk {
		end := start + readChunk
		if end > len(unique) {
			end = len(unique)
		}
		params := map[string]any{}
		predicates := make([]string, 0, end-start)
		expected := map[int64]string{}
		for i, n := range unique[start:end] {
			key := fmt.Sprintf("id%d", i)
			predicates = append(predicates, "n.id = $"+key)
			params[key] = n.ID
			expected[n.ID] = n.GID
		}
		q := "MATCH (n) WHERE " + strings.Join(predicates, " OR ") + " RETURN n.id AS id, n.gid AS gid"
		res, err := w.Client.DoQuery(ctx, q, params)
		if err != nil {
			return fmt.Errorf("check collision batch: %w", err)
		}
		for i := range res.Rows {
			id, err := res.Int64(i, 0)
			if err != nil {
				return fmt.Errorf("check collision id: %w", err)
			}
			old, err := res.String(i, 1)
			if err != nil {
				return fmt.Errorf("check collision gid: %w", err)
			}
			if old != expected[id] {
				return &CollisionError{ID: id, ExistingGID: old, IncomingGID: expected[id]}
			}
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
	limit := w.BatchSize
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if err := w.CheckCollisions(ctx, batch.Nodes); err != nil {
		return report, err
	}

	for _, group := range groupNodes(batch.Nodes) {
		for start := 0; start < len(group.nodes); start += limit {
			end := start + limit
			if end > len(group.nodes) {
				end = len(group.nodes)
			}
			rows := make([]map[string]any, 0, end-start)
			for _, n := range group.nodes[start:end] {
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
	}

	for _, group := range groupEdges(batch.Edges) {
		for start := 0; start < len(group.edges); start += limit {
			end := start + limit
			if end > len(group.edges) {
				end = len(group.edges)
			}
			rows := make([]map[string]any, 0, end-start)
			for _, e := range group.edges[start:end] {
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

// VerifyBatch checks every written node and relationship in bounded read
// queries. HydraDB's dialect does not support IN, so ids are expressed as
// parameterized OR predicates, never interpolated.
func (w HydraWriter) VerifyBatch(ctx context.Context, batch Batch) error {
	if w.Client == nil {
		return fmt.Errorf("hydra writer: nil client")
	}
	const readChunk = 100
	for _, group := range groupNodes(batch.Nodes) {
		for start := 0; start < len(group.nodes); start += readChunk {
			end := start + readChunk
			if end > len(group.nodes) {
				end = len(group.nodes)
			}
			params := map[string]any{}
			predicates := make([]string, 0, end-start)
			expected := map[int64]string{}
			for i, n := range group.nodes[start:end] {
				key := fmt.Sprintf("id%d", i)
				predicates = append(predicates, "n.id = $"+key)
				params[key] = n.ID
				expected[n.ID] = n.GID
			}
			q := "MATCH (n:" + group.label + ") WHERE " + strings.Join(predicates, " OR ") + " RETURN n.id AS id, n.gid AS gid"
			res, err := w.Client.DoQuery(ctx, q, params)
			if err != nil {
				return fmt.Errorf("verify %s nodes: %w", group.label, err)
			}
			if res.Count() != len(expected) {
				return fmt.Errorf("verify %s nodes: got %d rows want %d", group.label, res.Count(), len(expected))
			}
			for i := range res.Rows {
				id, err := res.Int64(i, 0)
				if err != nil {
					return err
				}
				gid, err := res.String(i, 1)
				if err != nil {
					return err
				}
				if expected[id] != gid {
					return fmt.Errorf("verify node id %d gid %q want %q", id, gid, expected[id])
				}
				delete(expected, id)
			}
			if len(expected) != 0 {
				return fmt.Errorf("verify %s nodes missing %d ids", group.label, len(expected))
			}
		}
	}
	for _, group := range groupEdges(batch.Edges) {
		check, err := w.VerifyEdges(ctx, group.edges)
		if err != nil {
			return err
		}
		if check.Missing != 0 || check.Duplicate != 0 || check.Unexpected != 0 {
			return fmt.Errorf("verify %s edges: expected=%d actual=%d missing=%d duplicate=%d unexpected=%d", group.typ, check.Expected, check.Actual, check.Missing, check.Duplicate, check.Unexpected)
		}
	}
	return nil
}

// VerifyEdges performs bounded endpoint/type inventory checks for a set of
// expected relationships. It is also used by reconciliation to distinguish
// missing relationships from duplicate logical endpoint relationships.
func (w HydraWriter) VerifyEdges(ctx context.Context, edges []Edge) (EdgeVerification, error) {
	if w.Client == nil {
		return EdgeVerification{}, fmt.Errorf("hydra writer: nil client")
	}
	const readChunk = 500
	var total EdgeVerification
	for _, group := range groupEdges(edges) {
		sourcePairs := map[int64]map[string]bool{}
		for _, edge := range group.edges {
			if sourcePairs[edge.FromID] == nil {
				sourcePairs[edge.FromID] = map[string]bool{}
			}
			sourcePairs[edge.FromID][edgeKey(edge)] = true
		}
		sources := make([]int64, 0, len(sourcePairs))
		for source := range sourcePairs {
			sources = append(sources, source)
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
		for start := 0; start < len(sources); start += readChunk {
			end := start + readChunk
			if end > len(sources) {
				end = len(sources)
			}
			params := map[string]any{}
			predicates := make([]string, 0, end-start)
			expected := map[string]bool{}
			for i, source := range sources[start:end] {
				key := fmt.Sprintf("a%d", i)
				predicates = append(predicates, "a.id = $"+key)
				params[key] = source
				for pair := range sourcePairs[source] {
					expected[pair] = true
				}
			}
			q := "MATCH (a:" + group.fromLabel + ")-[:" + group.typ + "]->(b:" + group.toLabel + ") WHERE " + strings.Join(predicates, " OR ") + " RETURN a.id AS src, b.id AS dst"
			res, err := w.Client.DoQuery(ctx, q, params)
			if err != nil {
				return total, fmt.Errorf("verify %s edges: %w", group.typ, err)
			}
			expectedCount := len(expected)
			actual := 0
			unexpected := 0
			for {
				actual += res.Count()
				for i := range res.Rows {
					src, err := res.Int64(i, 0)
					if err != nil {
						return total, err
					}
					dst, err := res.Int64(i, 1)
					if err != nil {
						return total, err
					}
					key := fmt.Sprintf("%d:%d", src, dst)
					if !expected[key] {
						unexpected++
					} else {
						delete(expected, key)
					}
				}
				if res.NextCursor == nil || res.QueryID == "" {
					break
				}
				res, err = w.Client.QueryPage(ctx, q, params, res.QueryID, *res.NextCursor)
				if err != nil {
					return total, fmt.Errorf("verify %s edges page: %w", group.typ, err)
				}
			}
			missing := len(expected)
			total.Expected += expectedCount
			total.Actual += actual
			total.Missing += missing
			total.Unexpected += unexpected
			found := expectedCount - missing
			if actual > found {
				total.Duplicate += actual - found
			}
		}
	}
	return total, nil
}

// ReconcileEdges removes only relationships in the supplied endpoint scopes
// that are absent from the desired batch. Relationship properties cannot be
// read in the verified HydraDB runtime, so the deterministic relationship ID
// is reconstructed from endpoint GIDs and source provenance carried by the
// endpoint nodes. This is safe because it deletes an exact relationship ID,
// never all relationships between two endpoints.
func (w HydraWriter) ReconcileEdges(ctx context.Context, desired []Edge, scopes []EdgeScope) error {
	if w.Client == nil {
		return fmt.Errorf("hydra writer: nil client")
	}
	if len(scopes) == 0 {
		return nil
	}
	desiredPairs := map[string]map[string]bool{}
	for _, edge := range desired {
		if desiredPairs[edge.Type] == nil {
			desiredPairs[edge.Type] = map[string]bool{}
		}
		desiredPairs[edge.Type][edgeKey(edge)] = true
	}
	var obsolete []Edge
	seen := map[int64]bool{}
	for _, scope := range scopes {
		for _, v := range []string{scope.Type, scope.FromLabel, scope.ToLabel, scope.Side} {
			if err := validateIdentifier(v); err != nil && v != "from" && v != "to" {
				return err
			}
		}
		if scope.Side != "from" && scope.Side != "to" {
			return fmt.Errorf("edge scope %s has invalid side %q", scope.Type, scope.Side)
		}
		q := "MATCH (a:" + scope.FromLabel + ")-[:" + scope.Type + "]->(b:" + scope.ToLabel + ") WHERE "
		if scope.Side == "from" {
			q += "a.id = $id"
		} else {
			q += "b.id = $id"
		}
		q += " RETURN a.id AS src, b.id AS dst, a.gid AS src_gid, b.gid AS dst_gid, a.src_pk AS src_pk, b.src_pk AS dst_pk"
		res, err := w.Client.DoQuery(ctx, q, map[string]any{"id": scope.EndpointID})
		if err != nil {
			return fmt.Errorf("inspect %s relationships for endpoint %d: %w", scope.Type, scope.EndpointID, err)
		}
		coTargets := map[int64]bool{}
		for i := range res.Rows {
			src, err := res.Int64(i, 0)
			if err != nil {
				return err
			}
			dst, err := res.Int64(i, 1)
			if err != nil {
				return err
			}
			pair := fmt.Sprintf("%d:%d", src, dst)
			if scope.Type == "CO_BID_IN" && scope.Side == "from" {
				coTargets[dst] = true
			}
			if desiredPairs[scope.Type][pair] {
				continue
			}
			srcGID, err := res.String(i, 2)
			if err != nil {
				return err
			}
			dstGID, err := res.String(i, 3)
			if err != nil {
				return err
			}
			srcPK, err := res.String(i, 4)
			if err != nil {
				return err
			}
			dstPK, err := res.String(i, 5)
			if err != nil {
				return err
			}
			pk := relationshipSourcePK(scope.Type, srcPK, dstPK)
			table := relationshipSourceTable(scope.Type)
			if pk == "" || table == "" {
				return fmt.Errorf("cannot reconstruct %s provenance for %d->%d", scope.Type, src, dst)
			}
			edge, err := NewEdge(scope.Type,
				Node{Label: scope.FromLabel, GID: srcGID, ID: src},
				Node{Label: scope.ToLabel, GID: dstGID, ID: dst},
				table, pk, 0, nil)
			if err != nil {
				return err
			}
			if !seen[edge.RID] {
				seen[edge.RID] = true
				obsolete = append(obsolete, edge)
			}
		}
		// When a participant changes tender membership, an obsolete observation
		// can still have an incoming edge from the other participant. Once an
		// affected Bid points at that observation, reconcile all incoming edges
		// to the observation as well.
		if scope.Type == "CO_BID_IN" && scope.Side == "from" {
			for observationID := range coTargets {
				q := "MATCH (a:Bid)-[:CO_BID_IN]->(b:CoBidObservation) WHERE b.id = $id RETURN a.id AS src, b.id AS dst, a.gid AS src_gid, b.gid AS dst_gid, a.src_pk AS src_pk, b.src_pk AS dst_pk"
				incoming, err := w.Client.DoQuery(ctx, q, map[string]any{"id": observationID})
				if err != nil {
					return fmt.Errorf("inspect CO_BID_IN observation %d: %w", observationID, err)
				}
				for i := range incoming.Rows {
					src, err := incoming.Int64(i, 0)
					if err != nil {
						return err
					}
					dst, err := incoming.Int64(i, 1)
					if err != nil {
						return err
					}
					if desiredPairs[scope.Type][fmt.Sprintf("%d:%d", src, dst)] {
						continue
					}
					srcGID, err := incoming.String(i, 2)
					if err != nil {
						return err
					}
					dstGID, err := incoming.String(i, 3)
					if err != nil {
						return err
					}
					srcPK, err := incoming.String(i, 4)
					if err != nil {
						return err
					}
					dstPK, err := incoming.String(i, 5)
					if err != nil {
						return err
					}
					edge, err := NewEdge("CO_BID_IN", Node{Label: "Bid", GID: srcGID, ID: src}, Node{Label: "CoBidObservation", GID: dstGID, ID: dst}, "tender_participants", srcPK, 0, nil)
					if err != nil {
						return err
					}
					if !seen[edge.RID] {
						seen[edge.RID] = true
						obsolete = append(obsolete, edge)
					}
					_ = dstPK
				}
			}
		}
	}
	return w.DeleteEdges(ctx, obsolete)
}

// DeleteEdges removes exact deterministic relationship IDs. Current HydraDB
// accepts this form with anonymous endpoints, while labelled endpoint
// deletion is not supported by the verified mutation grammar.
func (w HydraWriter) DeleteEdges(ctx context.Context, edges []Edge) error {
	if len(edges) == 0 {
		return nil
	}
	for _, group := range groupEdges(edges) {
		for start := 0; start < len(group.edges); start += 500 {
			end := start + 500
			if end > len(group.edges) {
				end = len(group.edges)
			}
			rows := make([]map[string]any, 0, end-start)
			for _, edge := range group.edges[start:end] {
				rows = append(rows, map[string]any{"rid": edge.RID})
			}
			q := "UNWIND $rows AS row MATCH (a)-[r:" + group.typ + " {id: row.rid}]->(b) DELETE r"
			if _, err := w.Client.DoQuery(ctx, q, map[string]any{"rows": rows}); err != nil {
				return fmt.Errorf("delete obsolete %s relationships: %w", group.typ, err)
			}
		}
	}
	return nil
}

func relationshipSourceTable(typ string) string {
	switch typ {
	case "ISSUED_BY", "CONTRACTOR_ON_RECORD":
		return "tenders"
	case "SUBMITTED_BID", "CO_BID_IN", "WON":
		return "tender_participants"
	case "HAS_AWARD":
		return "tender_extracted_meta"
	case "HAS_ALIAS":
		return "entity_aliases"
	case "HAS_GST_REGISTRATION":
		return "entity_gst"
	default:
		return ""
	}
}

func relationshipSourcePK(typ, srcPK, dstPK string) string {
	switch typ {
	case "CONTRACTOR_ON_RECORD", "HAS_AWARD", "HAS_ALIAS", "HAS_GST_REGISTRATION":
		return dstPK
	default:
		return srcPK
	}
}

func edgeKey(e Edge) string { return fmt.Sprintf("%d:%d", e.FromID, e.ToID) }

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
