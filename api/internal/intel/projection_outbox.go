package intel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/projection"
)

const (
	projectionOperationUpsert = "UPSERT_CONFIRMED_RELATIONSHIP"
	projectionOperationNone   = "NO_CONFIRMED_RELATIONSHIP"
	projectionStatusPending   = "PENDING"
	projectionStatusFailed    = "FAILED"
)

type ProjectionOutboxItem struct {
	ProjectionID     int64           `json:"projection_id"`
	ProjectionGID    string          `json:"projection_gid"`
	CandidateID      int64           `json:"candidate_id"`
	DecisionVersion  int             `json:"decision_version"`
	DesiredOperation string          `json:"desired_operation"`
	RelationshipType string          `json:"relationship_type"`
	SourceGID        string          `json:"source_gid"`
	TargetGID        string          `json:"target_gid"`
	Provenance       json.RawMessage `json:"provenance"`
	Status           string          `json:"status"`
	AttemptCount     int             `json:"attempt_count"`
	LastError        string          `json:"last_error"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type ProjectionFilter struct {
	Status string
	Limit  int
}

type ProjectionSummary struct {
	Selected   int      `json:"selected"`
	Applied    int      `json:"applied"`
	Superseded int      `json:"superseded"`
	Failed     int      `json:"failed"`
	DryRun     bool     `json:"dry_run"`
	Failures   []string `json:"failures,omitempty"`
}

type ProjectionReconciliation struct {
	Checked      int      `json:"checked"`
	Consistent   int      `json:"consistent"`
	Inconsistent int      `json:"inconsistent"`
	Examples     []string `json:"examples,omitempty"`
	Status       string   `json:"status"`
}

type ProjectionConsumer struct {
	Pool  *pgxpool.Pool
	Graph *graph.HTTPClient
}

func (c ProjectionConsumer) requirePool() error {
	if c.Pool == nil {
		return errors.New("PostgreSQL pool is required")
	}
	return nil
}

func (c ProjectionConsumer) List(ctx context.Context, filter ProjectionFilter) ([]ProjectionOutboxItem, error) {
	if err := c.requirePool(); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	query := `SELECT projection_id,projection_gid,candidate_id,decision_version,desired_operation,relationship_type,source_gid,target_gid,provenance,status,attempt_count,last_error,created_at,updated_at
FROM tg_graph_projection_outbox`
	args := []any{}
	if filter.Status != "" {
		args = append(args, filter.Status)
		query += ` WHERE status=$1`
	}
	query += fmt.Sprintf(` ORDER BY projection_id LIMIT $%d`, len(args)+1)
	args = append(args, filter.Limit)
	rows, err := c.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectionOutboxItem
	for rows.Next() {
		var item ProjectionOutboxItem
		var provenance []byte
		if err := rows.Scan(&item.ProjectionID, &item.ProjectionGID, &item.CandidateID, &item.DecisionVersion, &item.DesiredOperation, &item.RelationshipType, &item.SourceGID, &item.TargetGID, &provenance, &item.Status, &item.AttemptCount, &item.LastError, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Provenance = json.RawMessage(provenance)
		out = append(out, item)
	}
	return out, rows.Err()
}

// Apply processes only explicitly selected desired-state records. PostgreSQL
// remains authoritative: the candidate's current decision/version is checked
// immediately before any graph write, so an older delivery is superseded.
func (c ProjectionConsumer) Apply(ctx context.Context, limit int, dryRun bool) (ProjectionSummary, error) {
	return c.ApplyStatus(ctx, projectionStatusPending, limit, dryRun)
}

func (c ProjectionConsumer) ApplyStatus(ctx context.Context, status string, limit int, dryRun bool) (ProjectionSummary, error) {
	if err := c.requirePool(); err != nil {
		return ProjectionSummary{}, err
	}
	items, err := c.List(ctx, ProjectionFilter{Status: status, Limit: limit})
	if err != nil {
		return ProjectionSummary{}, err
	}
	summary := ProjectionSummary{Selected: len(items), DryRun: dryRun, Failures: []string{}}
	if dryRun {
		return summary, nil
	}
	if c.Graph == nil {
		return summary, errors.New("HydraDB client is required for non-dry-run projection apply")
	}
	for _, item := range items {
		result, err := c.applyOne(ctx, item, status)
		if err != nil {
			summary.Failed++
			summary.Failures = append(summary.Failures, fmt.Sprintf("%d: %v", item.ProjectionID, err))
			continue
		}
		switch result {
		case "APPLIED":
			summary.Applied++
		case "SUPERSEDED":
			summary.Superseded++
		}
	}
	return summary, nil
}

func (c ProjectionConsumer) Status(ctx context.Context) ([]map[string]any, error) {
	if err := c.requirePool(); err != nil {
		return nil, err
	}
	rows, err := c.Pool.Query(ctx, `SELECT status,COUNT(*) FROM tg_graph_projection_outbox GROUP BY status ORDER BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"status": status, "count": count})
	}
	return out, rows.Err()
}

// Reconcile verifies applied desired-state records without changing either
// PostgreSQL or HydraDB. Relationship-property read-back is unavailable in
// the current HydraDB runtime, so verification is bounded to relationship
// type and deterministic endpoint identities.
func (c ProjectionConsumer) Reconcile(ctx context.Context, limit int) (ProjectionReconciliation, error) {
	if c.Graph == nil {
		return ProjectionReconciliation{}, errors.New("HydraDB client is required for projection reconciliation")
	}
	items, err := c.List(ctx, ProjectionFilter{Status: "APPLIED", Limit: limit})
	if err != nil {
		return ProjectionReconciliation{}, err
	}
	report := ProjectionReconciliation{Status: "SUCCEEDED", Examples: []string{}}
	for _, item := range items {
		report.Checked++
		if err := c.verifyDesiredState(ctx, item); err != nil {
			report.Inconsistent++
			if len(report.Examples) < 20 {
				report.Examples = append(report.Examples, fmt.Sprintf("%d: %v", item.ProjectionID, err))
			}
			continue
		}
		report.Consistent++
	}
	if report.Inconsistent > 0 {
		report.Status = "INCONSISTENT"
	}
	return report, nil
}

func (c ProjectionConsumer) applyOne(ctx context.Context, item ProjectionOutboxItem, selectedStatus string) (string, error) {
	claim := uuid.NewString()
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	var currentVersion int
	if err := tx.QueryRow(ctx, `SELECT current_status,current_version FROM tg_relationship_candidates WHERE candidate_id=$1 FOR UPDATE`, item.CandidateID).Scan(&currentStatus, &currentVersion); err != nil {
		return "", fmt.Errorf("load candidate state: %w", err)
	}
	if currentVersion != item.DecisionVersion || !decisionMatchesOperation(currentStatus, item.DesiredOperation) {
		if _, err := tx.Exec(ctx, `UPDATE tg_graph_projection_outbox SET status='SUPERSEDED',claimed_by=NULL,claimed_at=NULL,updated_at=now(),last_error='superseded by a newer candidate decision' WHERE projection_id=$1 AND status=$2`, item.ProjectionID, selectedStatus); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "SUPERSEDED", nil
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_graph_projection_outbox SET claimed_by=$2,claimed_at=now(),attempt_count=attempt_count+1,updated_at=now() WHERE projection_id=$1 AND status=$3`, item.ProjectionID, claim, selectedStatus); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	if err := c.reconcilePreviousEdges(ctx, item); err != nil {
		return "", c.fail(item, claim, err)
	}
	if item.DesiredOperation == projectionOperationUpsert {
		if err := c.upsertConfirmed(ctx, item); err != nil {
			return "", c.fail(item, claim, err)
		}
	}
	if err := c.verifyDesiredState(ctx, item); err != nil {
		return "", c.fail(item, claim, err)
	}
	if tag, err := c.Pool.Exec(ctx, `UPDATE tg_graph_projection_outbox SET status='APPLIED',claimed_by=NULL,claimed_at=NULL,last_error='',updated_at=now() WHERE projection_id=$1 AND claimed_by=$2`, item.ProjectionID, claim); err != nil {
		return "", fmt.Errorf("mark projection applied: %w", err)
	} else if tag.RowsAffected() != 1 {
		return "", fmt.Errorf("projection claim %s was lost before completion", claim)
	}
	return "APPLIED", nil
}

func (c ProjectionConsumer) fail(item ProjectionOutboxItem, claim string, cause error) error {
	_, updateErr := c.Pool.Exec(context.Background(), `UPDATE tg_graph_projection_outbox SET status='FAILED',claimed_by=NULL,claimed_at=NULL,last_error=$3,updated_at=now() WHERE projection_id=$1 AND claimed_by=$2`, item.ProjectionID, claim, cause.Error())
	if updateErr != nil {
		return fmt.Errorf("%v; record failure: %w", cause, updateErr)
	}
	return cause
}

func decisionMatchesOperation(status, operation string) bool {
	if operation == projectionOperationUpsert {
		return status == "CONFIRMED"
	}
	return status == "REJECTED" || status == "NEEDS_REVIEW"
}

func (c ProjectionConsumer) upsertConfirmed(ctx context.Context, item ProjectionOutboxItem) error {
	from, to, typ, err := analystEdge(item)
	if err != nil {
		return err
	}
	edge, err := projection.NewEdge(typ, projection.NodeRef(from.label, item.SourceGID), projection.NodeRef(to.label, item.TargetGID), "tg_relationship_decisions", fmt.Sprintf("%d:%d", item.CandidateID, item.DecisionVersion), time.Now().UnixMilli(), map[string]any{
		"status":           "CONFIRMED",
		"method":           "ANALYST_DECISION",
		"candidate_gid":    item.ProjectionGID,
		"decision_version": int64(item.DecisionVersion),
		"provenance_json":  string(item.Provenance),
	})
	if err != nil {
		return err
	}
	_, err = (projection.HydraWriter{Client: c.Graph, BatchSize: 1}).Write(ctx, projection.Batch{Edges: []projection.Edge{edge}})
	if err != nil {
		return err
	}
	return nil
}

func (c ProjectionConsumer) reconcilePreviousEdges(ctx context.Context, item ProjectionOutboxItem) error {
	// Remove only earlier analyst projections for this exact candidate and
	// endpoints. Source-derived relationships have different deterministic IDs
	// and are never targeted by this cleanup.
	rows, err := c.Pool.Query(ctx, `SELECT decision_version FROM tg_graph_projection_outbox WHERE candidate_id=$1 AND desired_operation=$2 AND decision_version < $3`, item.CandidateID, projectionOperationUpsert, item.DecisionVersion)
	if err != nil {
		return err
	}
	defer rows.Close()
	from, to, typ, err := analystEdge(item)
	if err != nil {
		return err
	}
	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if item.DesiredOperation == projectionOperationUpsert {
		// The current decision is authoritative; all older confirmed versions
		// are obsolete before the new one is written.
	} else {
		// A rejection/needs-review decision removes every prior confirmed edge.
	}
	for _, version := range versions {
		rid := projection.NumericID(projection.GID("EDGE", typ, item.SourceGID+":"+item.TargetGID+":"+fmt.Sprintf("%d:%d", item.CandidateID, version)))
		if err := c.deleteEdge(ctx, from.label, to.label, typ, projection.NodeRef(from.label, item.SourceGID).ID, projection.NodeRef(to.label, item.TargetGID).ID, rid); err != nil {
			return err
		}
	}
	return nil
}

type analystNode struct{ label string }

func analystEdge(item ProjectionOutboxItem) (analystNode, analystNode, string, error) {
	switch item.RelationshipType {
	case "VENDOR_CORPORATE_ENTITY":
		return analystNode{"Vendor"}, analystNode{"CorporateEntity"}, "IDENTIFIED_AS", nil
	case "TENDER_ANNOUNCEMENT":
		return analystNode{"Tender"}, analystNode{"CorporateAnnouncement"}, "MATCHED_TO", nil
	default:
		return analystNode{}, analystNode{}, "", fmt.Errorf("unsupported analyst relationship type %q", item.RelationshipType)
	}
}

func (c ProjectionConsumer) deleteEdge(ctx context.Context, fromLabel, toLabel, typ string, srcID, dstID, rid int64) error {
	// HydraDB's verified mutation grammar requires anonymous endpoints for
	// relationship-property deletion. The deterministic relationship id is
	// globally derived, so this remains an exact, bounded analyst-edge delete.
	query := fmt.Sprintf("UNWIND $rows AS row MATCH (a)-[r:%s {id: row.rid}]->(b) DELETE r", typ)
	_, err := c.Graph.DoQuery(ctx, query, map[string]any{"rows": []map[string]any{{"src": srcID, "dst": dstID, "rid": rid}}})
	return err
}

func (c ProjectionConsumer) verifyDesiredState(ctx context.Context, item ProjectionOutboxItem) error {
	from, to, typ, err := analystEdge(item)
	if err != nil {
		return err
	}
	srcID := projection.NodeRef(from.label, item.SourceGID).ID
	dstID := projection.NodeRef(to.label, item.TargetGID).ID
	query := fmt.Sprintf("MATCH (a:%s {id: $src})-[:%s]->(b:%s {id: $dst}) RETURN count(*) AS c", from.label, typ, to.label)
	res, err := c.Graph.DoQuery(ctx, query, map[string]any{"src": srcID, "dst": dstID})
	if err != nil {
		return err
	}
	count, err := res.Int64(0, 0)
	if err != nil {
		return err
	}
	if item.DesiredOperation == projectionOperationUpsert && count < 1 {
		return fmt.Errorf("confirmed analyst relationship not visible after write")
	}
	if item.DesiredOperation == projectionOperationNone && count != 0 {
		return fmt.Errorf("rejected analyst relationship remains visible: %d", count)
	}
	return nil
}
