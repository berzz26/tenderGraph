package ingest

import (
	"context"
	"fmt"

	"vyuha/api/internal/projection"
)

type ReconciliationReport struct {
	Dataset               string                                `json:"dataset"`
	Status                string                                `json:"status"`
	MissingNodes          int64                                 `json:"missing_nodes"`
	MissingEdges          int64                                 `json:"missing_edges"`
	StaleNodes            int64                                 `json:"stale_nodes"`
	StaleEdges            int64                                 `json:"stale_edges"`
	InvalidProvenance     int64                                 `json:"invalid_provenance"`
	DuplicateLogicalEdges int64                                 `json:"duplicate_logical_edges"`
	SourceRecords         int64                                 `json:"source_records"`
	Examples              []string                              `json:"examples"`
	Relationships         map[string]RelationshipReconciliation `json:"relationships"`
	InventoryLimitations  []string                              `json:"inventory_limitations,omitempty"`
	Error                 string                                `json:"error,omitempty"`
}

type RelationshipReconciliation struct {
	Expected               int64  `json:"expected"`
	Actual                 int64  `json:"actual"`
	Missing                int64  `json:"missing"`
	Stale                  int64  `json:"stale"`
	DuplicateLogical       int64  `json:"duplicate_logical"`
	ProvenanceVerification string `json:"provenance_verification"`
	StaleVerification      string `json:"stale_verification"`
}

type relationshipShape struct {
	FromLabel string
	ToLabel   string
}

type Reconciler struct {
	Scanner   SourceScanner
	Writer    projection.HydraWriter
	Control   ControlStore
	Registry  Registry
	BatchSize int
}

func (r Reconciler) Run(ctx context.Context, dataset, mode string) (ReconciliationReport, error) {
	spec, ok := r.Registry.Get(dataset)
	if !ok {
		return ReconciliationReport{Dataset: dataset, Status: "FAILED"}, fmt.Errorf("unknown dataset %s", dataset)
	}
	if mode != "detect" && mode != "repair" {
		return ReconciliationReport{Dataset: dataset, Status: "FAILED"}, fmt.Errorf("invalid reconciliation mode %s", mode)
	}
	high, err := r.Scanner.HighWatermark(ctx, spec)
	if err != nil {
		return ReconciliationReport{Dataset: dataset, Status: "FAILED"}, err
	}
	if high.Empty() {
		return ReconciliationReport{Dataset: dataset, Status: "SUCCEEDED"}, nil
	}
	reconciliationID, err := r.Control.StartReconciliation(ctx, dataset, mode)
	if err != nil {
		return ReconciliationReport{Dataset: dataset, Status: "FAILED"}, err
	}
	limit := r.BatchSize
	if limit <= 0 {
		limit = spec.BatchSize
	}
	if limit > 1000 {
		limit = 1000
	}
	expected := map[string]map[int64]string{}
	for _, label := range spec.NodeLabels {
		expected[label] = map[int64]string{}
	}
	report := ReconciliationReport{Dataset: dataset, Status: "SUCCEEDED", Examples: []string{}, Relationships: map[string]RelationshipReconciliation{}, InventoryLimitations: []string{"HydraDB relationship properties are not readable; relationship provenance and deterministic relationship IDs are write-time verified only"}}
	finished := false
	defer func() {
		if !finished {
			_ = r.Control.FinishReconciliation(context.Background(), reconciliationID, "FAILED", report.MissingNodes, report.MissingEdges, report.StaleNodes, report.InvalidProvenance, report.DuplicateLogicalEdges, stringExamples(report.Examples), report.Error)
		}
	}()
	finish := func(status string, cause error) (ReconciliationReport, error) {
		report.Status = status
		if cause != nil {
			report.Error = cause.Error()
		}
		finished = true
		if finishErr := r.Control.FinishReconciliation(ctx, reconciliationID, status, report.MissingNodes, report.MissingEdges, report.StaleNodes, report.InvalidProvenance, report.DuplicateLogicalEdges, stringExamples(report.Examples), report.Error); finishErr != nil {
			return reconciliationFailure(report, finishErr)
		}
		if cause != nil {
			return report, cause
		}
		return report, nil
	}
	fail := func(cause error) (ReconciliationReport, error) { return finish("FAILED", cause) }
	expectedEdges := map[string]map[string]bool{}
	shapes := map[string]relationshipShape{}
	for _, typ := range spec.EdgeTypes {
		shape, ok := relationshipShapeFor(typ)
		if ok {
			expectedEdges[typ] = map[string]bool{}
			shapes[typ] = shape
		}
	}
	current := Cursor{}
	for {
		batch, err := r.Scanner.Scan(ctx, spec, current, high, limit, 0)
		if err != nil {
			return fail(err)
		}
		if batch.Items == 0 {
			break
		}
		report.SourceRecords += int64(batch.Items)
		b, err := deduplicate(batch.Batch)
		if err != nil {
			return fail(err)
		}
		for _, n := range b.Nodes {
			if _, ok := expected[n.Label]; ok {
				expected[n.Label][n.ID] = n.GID
			}
		}
		for _, edge := range b.Edges {
			if _, ok := expectedEdges[edge.Type]; ok {
				expectedEdges[edge.Type][fmt.Sprintf("%d:%d", edge.FromID, edge.ToID)] = true
			}
		}
		if current.Empty() || current.Less(batch.Cursor) {
			current = batch.Cursor
		} else {
			return fail(fmt.Errorf("reconciliation cursor did not advance"))
		}
		if err := r.Control.CheckpointReconciliation(ctx, reconciliationID, current, report.SourceRecords); err != nil {
			return fail(fmt.Errorf("checkpoint reconciliation: %w", err))
		}
	}
	for typ, pairs := range expectedEdges {
		shape := shapes[typ]
		edges := make([]projection.Edge, 0, len(pairs))
		for key := range pairs {
			var src, dst int64
			if _, err := fmt.Sscanf(key, "%d:%d", &src, &dst); err != nil {
				return fail(fmt.Errorf("decode expected %s edge %q: %w", typ, key, err))
			}
			edges = append(edges, projection.Edge{Type: typ, FromLabel: shape.FromLabel, ToLabel: shape.ToLabel, FromID: src, ToID: dst})
		}
		check, err := r.Writer.VerifyEdges(ctx, edges)
		if err != nil {
			return fail(err)
		}
		rel := RelationshipReconciliation{Expected: int64(check.Expected), Actual: int64(check.Actual), Missing: int64(check.Missing), Stale: int64(check.Unexpected), DuplicateLogical: int64(check.Duplicate), ProvenanceVerification: "unavailable: HydraDB relationship variables are not readable in verified runtime", StaleVerification: "bounded to source-derived endpoint identities; edges from deleted source records require a full runtime inventory"}
		if rel.Missing > 0 && len(report.Examples) < 20 {
			report.Examples = append(report.Examples, fmt.Sprintf("%s missing endpoint relationships=%d", typ, rel.Missing))
		}
		report.Relationships[typ] = rel
		report.MissingEdges += rel.Missing
		report.StaleEdges += rel.Stale
		report.DuplicateLogicalEdges += rel.DuplicateLogical
	}
	for label, ids := range expected {
		missing, invalid, err := r.Writer.VerifyNodes(ctx, label, ids)
		if err != nil {
			return fail(err)
		}
		report.InvalidProvenance += int64(invalid)
		report.MissingNodes += int64(missing)
	}
	report.InventoryLimitations = append(report.InventoryLimitations, "Stale node inventory is not asserted in bounded mode; HydraDB label-wide scans exceeded the HTTP query budget during validation")
	if mode == "repair" && (report.MissingNodes > 0 || report.MissingEdges > 0 || report.InvalidProvenance > 0) {
		_, err := (&Engine{Scanner: r.Scanner, Writer: r.Writer, Control: r.Control, Registry: r.Registry, Verify: true, BatchSize: limit}).RunDataset(ctx, dataset, "selected")
		if err != nil {
			return fail(err)
		}
	}
	return finish("SUCCEEDED", nil)
}

func relationshipShapeFor(typ string) (relationshipShape, bool) {
	shapes := map[string]relationshipShape{
		"ISSUED_BY":            {"Tender", "GovernmentOrganization"},
		"CONTRACTOR_ON_RECORD": {"Vendor", "Tender"},
		"SUBMITTED_BID":        {"Bid", "Tender"},
		"HAS_AWARD":            {"Tender", "Award"},
		"WON":                  {"Bid", "Award"},
		"CO_BID_IN":            {"Bid", "CoBidObservation"},
		"HAS_ALIAS":            {"CorporateEntity", "Alias"},
		"HAS_GST_REGISTRATION": {"CorporateEntity", "GstRegistration"},
	}
	shape, ok := shapes[typ]
	if !ok {
		return relationshipShape{}, false
	}
	return shape, true
}

func reconciliationFailure(report ReconciliationReport, err error) (ReconciliationReport, error) {
	report.Status = "FAILED"
	report.Error = err.Error()
	return report, err
}

func stringExamples(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
