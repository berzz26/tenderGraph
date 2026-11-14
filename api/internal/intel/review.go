package intel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"vyuha/api/internal/projection"
)

const analystCandidateGeneratorVersion = "phase7-v1"

var validDecisionStatuses = map[string]bool{
	"CONFIRMED":    true,
	"REJECTED":     true,
	"NEEDS_REVIEW": true,
}

type ReviewStore struct{ Pool *pgxpool.Pool }

func CandidateGID(relationshipType, sourceGID, targetGID string) string {
	return "CANDIDATE:" + relationshipType + ":" + sourceGID + ":" + targetGID
}

func CandidateID(candidateGID string) int64 { return projection.NumericID(candidateGID) }

func AnalystProjectionGID(candidateID int64, decisionVersion int) string {
	return fmt.Sprintf("TGPROJ:v:%d:%d", candidateID, decisionVersion)
}

type Candidate struct {
	CandidateID      int64           `json:"candidate_id"`
	CandidateGID     string          `json:"candidate_gid"`
	RelationshipType string          `json:"relationship_type"`
	SourceType       string          `json:"source_type"`
	SourceID         string          `json:"source_id"`
	SourceGID        string          `json:"source_gid"`
	TargetType       string          `json:"target_type"`
	TargetID         string          `json:"target_id"`
	TargetGID        string          `json:"target_gid"`
	GenerationMethod string          `json:"generation_method"`
	InitialEvidence  json.RawMessage `json:"initial_evidence"`
	LatestEvidence   json.RawMessage `json:"latest_evidence"`
	GeneratorVersion string          `json:"generator_version"`
	CurrentStatus    string          `json:"current_status"`
	CurrentVersion   int             `json:"current_version"`
	FirstDetectedAt  time.Time       `json:"first_detected_at"`
	LastEvaluatedAt  time.Time       `json:"last_evaluated_at"`
}

type CandidateFilter struct {
	RelationshipType string
	Status           string
	Method           string
	Limit            int
	Offset           int
}

type GenerationSummary struct {
	VendorCorporateCandidates    int    `json:"vendor_corporate_candidates"`
	TenderAnnouncementCandidates int    `json:"tender_announcement_candidates"`
	GeneratorVersion             string `json:"generator_version"`
}

type DecisionInput struct {
	CandidateID     int64
	Status          string
	Rationale       string
	AnalystID       string
	EvidenceRefs    json.RawMessage
	ExpectedVersion *int
	RequestID       string
}

type DecisionResult struct {
	CandidateID      int64  `json:"candidate_id"`
	CandidateGID     string `json:"candidate_gid"`
	PreviousStatus   string `json:"previous_status"`
	Status           string `json:"status"`
	DecisionVersion  int    `json:"decision_version"`
	DecisionID       int64  `json:"decision_id,omitempty"`
	HistoryID        int64  `json:"history_id,omitempty"`
	ProjectionID     int64  `json:"projection_id,omitempty"`
	ProjectionGID    string `json:"projection_gid,omitempty"`
	DesiredOperation string `json:"desired_operation"`
	RequestID        string `json:"request_id"`
	Idempotent       bool   `json:"idempotent"`
}

type DecisionHistory struct {
	HistoryID       int64           `json:"history_id"`
	CandidateID     int64           `json:"candidate_id"`
	PreviousStatus  *string         `json:"previous_status,omitempty"`
	NewStatus       string          `json:"new_status"`
	AnalystID       string          `json:"analyst_id,omitempty"`
	Rationale       string          `json:"rationale"`
	EvidenceRefs    json.RawMessage `json:"evidence_refs"`
	DecisionVersion int             `json:"decision_version"`
	RequestID       string          `json:"request_id"`
	CreatedAt       time.Time       `json:"created_at"`
}

func (s ReviewStore) requirePool() error {
	if s.Pool == nil {
		return errors.New("PostgreSQL pool is required")
	}
	return nil
}

// RefreshCandidates is idempotent. It updates latest evidence and evaluation
// timestamps, but never changes current_status or current_version.
func (s ReviewStore) RefreshCandidates(ctx context.Context) (GenerationSummary, error) {
	if err := s.requirePool(); err != nil {
		return GenerationSummary{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return GenerationSummary{}, err
	}
	defer tx.Rollback(ctx)
	summary := GenerationSummary{GeneratorVersion: analystCandidateGeneratorVersion}
	rows, err := tx.Query(ctx, `
WITH gst_counts AS (
  SELECT gstin, COUNT(DISTINCT entity_id)::int AS entity_count
  FROM entity_gst WHERE NULLIF(BTRIM(gstin),'') IS NOT NULL GROUP BY gstin
), gst_matches AS (
  SELECT c.id::text AS vendor_id, e.id::text AS entity_id,
    CASE WHEN gc.entity_count=1 THEN 'GSTIN_EXACT_UNIQUE' ELSE 'GSTIN_EXACT_AMBIGUOUS' END AS method,
    jsonb_build_object(
      'method', CASE WHEN gc.entity_count=1 THEN 'GSTIN_EXACT_UNIQUE' ELSE 'GSTIN_EXACT_AMBIGUOUS' END,
      'source_table','entity_gst', 'source_pk', eg.id::text,
      'source_gid','GST:v:'||eg.id::text, 'gstin', eg.gstin,
      'entity_count', gc.entity_count) AS evidence
  FROM contractor_v2 c JOIN entity_gst eg ON eg.gstin=c.gst
  JOIN entities e ON e.id=eg.entity_id JOIN gst_counts gc ON gc.gstin=eg.gstin
), name_matches AS (
  SELECT c.id::text AS vendor_id, e.id::text AS entity_id,
    'NAME_NORMALIZED_EXACT'::text AS method,
    jsonb_build_object(
      'method','NAME_NORMALIZED_EXACT',
      'source_table','contractor_v2', 'source_pk', c.id::text,
      'source_gid','VENDOR:cp:'||c.id::text,
      'target_table','entities', 'target_pk', e.id::text,
      'target_gid','ENTITY:v:'||e.id::text) AS evidence
  FROM contractor_v2 c JOIN entities e ON
    LOWER(regexp_replace(COALESCE(c.business_name,''),'[^a-zA-Z0-9]+','','g')) =
    LOWER(regexp_replace(COALESCE(e.normalized_name,''),'[^a-zA-Z0-9]+','','g'))
  WHERE NULLIF(BTRIM(e.normalized_name),'') IS NOT NULL
), matches AS (
  SELECT * FROM gst_matches UNION ALL SELECT * FROM name_matches
)
SELECT vendor_id, entity_id, array_agg(DISTINCT method), jsonb_agg(DISTINCT evidence)
FROM matches GROUP BY vendor_id, entity_id ORDER BY vendor_id, entity_id`)
	if err != nil {
		return summary, err
	}
	type vendorEntityMatch struct {
		vendorID string
		entityID string
		methods  []string
		evidence []byte
	}
	var vendorMatches []vendorEntityMatch
	for rows.Next() {
		var vendorID, entityID string
		var methods []string
		var evidence []byte
		if err := rows.Scan(&vendorID, &entityID, &methods, &evidence); err != nil {
			return summary, err
		}
		vendorMatches = append(vendorMatches, vendorEntityMatch{vendorID: vendorID, entityID: entityID, methods: methods, evidence: evidence})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return summary, err
	}
	rows.Close()
	for _, match := range vendorMatches {
		vendorID, entityID, methods, evidence := match.vendorID, match.entityID, match.methods, match.evidence
		method := methods[0]
		if len(methods) > 1 {
			method = "MULTI_EVIDENCE"
		}
		vendorGID := projection.GID("VENDOR", "cp", vendorID)
		entityGID := projection.GID("ENTITY", "v", entityID)
		candidateGID := CandidateGID("VENDOR_CORPORATE_ENTITY", vendorGID, entityGID)
		candidateID := CandidateID(candidateGID)
		_, err = tx.Exec(ctx, `INSERT INTO tg_relationship_candidates
 (candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version)
 VALUES ($1,$2,'VENDOR_CORPORATE_ENTITY','Vendor',$3,$4,'CorporateEntity',$5,$6,$7,$8::jsonb,$8::jsonb,$9)
 ON CONFLICT (candidate_gid) DO UPDATE SET latest_evidence=EXCLUDED.latest_evidence,last_evaluated_at=now(),updated_at=now(),generator_version=EXCLUDED.generator_version`,
			candidateID, candidateGID, vendorID, vendorGID, entityID, entityGID, method, evidence, analystCandidateGeneratorVersion)
		if err != nil {
			return summary, fmt.Errorf("upsert vendor/entity candidate: %w", err)
		}
		summary.VendorCorporateCandidates++
	}

	rows, err = tx.Query(ctx, `SELECT id::text,tender_id::text,announcement_id::text,COALESCE(match_method,''),match_status FROM tender_announcement_matches ORDER BY id`)
	if err != nil {
		return summary, err
	}
	type tenderAnnouncementMatch struct {
		matchID, tenderID, announcementID, method, sourceStatus string
	}
	var announcementMatches []tenderAnnouncementMatch
	for rows.Next() {
		var matchID, tenderID, announcementID, method, sourceStatus string
		if err := rows.Scan(&matchID, &tenderID, &announcementID, &method, &sourceStatus); err != nil {
			return summary, err
		}
		announcementMatches = append(announcementMatches, tenderAnnouncementMatch{matchID, tenderID, announcementID, method, sourceStatus})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return summary, err
	}
	rows.Close()
	for _, match := range announcementMatches {
		matchID, tenderID, announcementID, method, sourceStatus := match.matchID, match.tenderID, match.announcementID, match.method, match.sourceStatus
		tenderGID := projection.GID("TENDER", "v", tenderID)
		announcementGID := projection.GID("ANN", "v", announcementID)
		candidateGID := CandidateGID("TENDER_ANNOUNCEMENT", tenderGID, announcementGID)
		candidateID := CandidateID(candidateGID)
		evidence, _ := json.Marshal([]map[string]any{{"method": method, "source_table": "tender_announcement_matches", "source_pk": matchID, "source_gid": "TAM:v:" + matchID, "source_match_status": sourceStatus, "tender_gid": tenderGID, "announcement_gid": announcementGID}})
		if method == "" {
			method = "EXISTING_TENDER_ANNOUNCEMENT_MATCH"
		}
		_, err = tx.Exec(ctx, `INSERT INTO tg_relationship_candidates
 (candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version)
 VALUES ($1,$2,'TENDER_ANNOUNCEMENT','Tender',$3,$4,'Announcement',$5,$6,$7,$8::jsonb,$8::jsonb,$9)
 ON CONFLICT (candidate_gid) DO UPDATE SET latest_evidence=EXCLUDED.latest_evidence,last_evaluated_at=now(),updated_at=now(),generator_version=EXCLUDED.generator_version`,
			candidateID, candidateGID, tenderID, tenderGID, announcementID, announcementGID, method, evidence, analystCandidateGeneratorVersion)
		if err != nil {
			return summary, fmt.Errorf("upsert tender/announcement candidate: %w", err)
		}
		summary.TenderAnnouncementCandidates++
	}
	if err := tx.Commit(ctx); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s ReviewStore) ListCandidates(ctx context.Context, filter CandidateFilter) ([]Candidate, error) {
	if err := s.requirePool(); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := `SELECT candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version,current_status,current_version,first_detected_at,last_evaluated_at FROM tg_relationship_candidates WHERE 1=1`
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(" AND %s=$%d", condition, len(args))
	}
	if filter.RelationshipType != "" {
		add("relationship_type", filter.RelationshipType)
	}
	if filter.Status != "" {
		add("current_status", filter.Status)
	}
	if filter.Method != "" {
		add("generation_method", filter.Method)
	}
	query += fmt.Sprintf(" ORDER BY candidate_id LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Candidate, 0)
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s ReviewStore) GetCandidate(ctx context.Context, candidateID int64) (Candidate, error) {
	if err := s.requirePool(); err != nil {
		return Candidate{}, err
	}
	row := s.Pool.QueryRow(ctx, `SELECT candidate_id,candidate_gid,relationship_type,source_type,source_id,source_gid,target_type,target_id,target_gid,generation_method,initial_evidence,latest_evidence,generator_version,current_status,current_version,first_detected_at,last_evaluated_at FROM tg_relationship_candidates WHERE candidate_id=$1`, candidateID)
	return scanCandidate(row)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanCandidate(row rowScanner) (Candidate, error) {
	var c Candidate
	var initial, latest []byte
	err := row.Scan(&c.CandidateID, &c.CandidateGID, &c.RelationshipType, &c.SourceType, &c.SourceID, &c.SourceGID, &c.TargetType, &c.TargetID, &c.TargetGID, &c.GenerationMethod, &initial, &latest, &c.GeneratorVersion, &c.CurrentStatus, &c.CurrentVersion, &c.FirstDetectedAt, &c.LastEvaluatedAt)
	c.InitialEvidence = json.RawMessage(initial)
	c.LatestEvidence = json.RawMessage(latest)
	return c, err
}

func (s ReviewStore) History(ctx context.Context, candidateID int64) ([]DecisionHistory, error) {
	if err := s.requirePool(); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT history_id,candidate_id,previous_status,new_status,COALESCE(analyst_id,''),rationale,evidence_refs,decision_version,request_id,created_at FROM tg_relationship_decision_history WHERE candidate_id=$1 ORDER BY decision_version,history_id`, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DecisionHistory, 0)
	for rows.Next() {
		var h DecisionHistory
		var previous *string
		var evidence []byte
		if err := rows.Scan(&h.HistoryID, &h.CandidateID, &previous, &h.NewStatus, &h.AnalystID, &h.Rationale, &evidence, &h.DecisionVersion, &h.RequestID, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.PreviousStatus = previous
		h.EvidenceRefs = json.RawMessage(evidence)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s ReviewStore) Decide(ctx context.Context, input DecisionInput) (DecisionResult, error) {
	if err := s.requirePool(); err != nil {
		return DecisionResult{}, err
	}
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	if !validDecisionStatuses[input.Status] {
		return DecisionResult{}, fmt.Errorf("invalid decision status %q", input.Status)
	}
	input.Rationale = strings.TrimSpace(input.Rationale)
	if input.Rationale == "" {
		return DecisionResult{}, errors.New("decision rationale is required")
	}
	if len(input.EvidenceRefs) == 0 {
		input.EvidenceRefs = json.RawMessage(`[]`)
	}
	if !json.Valid(input.EvidenceRefs) {
		return DecisionResult{}, errors.New("evidence references must be valid JSON")
	}
	if input.RequestID == "" {
		input.RequestID = uuid.NewString()
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer tx.Rollback(ctx)
	var existingVersion int
	var existingStatus string
	var candidateGID, sourceGID, targetGID, relationshipType string
	if err := tx.QueryRow(ctx, `SELECT candidate_gid,current_status,current_version,source_gid,target_gid,relationship_type FROM tg_relationship_candidates WHERE candidate_id=$1 FOR UPDATE`, input.CandidateID).Scan(&candidateGID, &existingStatus, &existingVersion, &sourceGID, &targetGID, &relationshipType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DecisionResult{}, fmt.Errorf("candidate %d not found", input.CandidateID)
		}
		return DecisionResult{}, err
	}
	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tg_relationship_decision_history WHERE candidate_id=$1 AND request_id=$2)`, input.CandidateID, input.RequestID).Scan(&already); err != nil {
		return DecisionResult{}, err
	}
	if already {
		return DecisionResult{CandidateID: input.CandidateID, CandidateGID: candidateGID, PreviousStatus: existingStatus, Status: existingStatus, DecisionVersion: existingVersion, RequestID: input.RequestID, Idempotent: true}, nil
	}
	if input.ExpectedVersion != nil && *input.ExpectedVersion != existingVersion {
		return DecisionResult{}, fmt.Errorf("candidate version conflict: expected %d, current %d", *input.ExpectedVersion, existingVersion)
	}
	if err := validateDecisionTransition(existingStatus, existingVersion, input.Status); err != nil {
		return DecisionResult{}, err
	}
	version := existingVersion + 1
	var historyID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tg_relationship_decision_history(candidate_id,previous_status,new_status,analyst_id,rationale,evidence_refs,decision_version,request_id) VALUES ($1,NULLIF($2,''),$3,NULLIF($4,''),$5,$6::jsonb,$7,$8) RETURNING history_id`, input.CandidateID, existingStatus, input.Status, input.AnalystID, input.Rationale, input.EvidenceRefs, version, input.RequestID).Scan(&historyID); err != nil {
		return DecisionResult{}, err
	}
	var decisionID int64
	err = tx.QueryRow(ctx, `INSERT INTO tg_relationship_decisions(candidate_id,decision_status,analyst_id,rationale,evidence_refs,decision_version,request_id) VALUES ($1,$2,NULLIF($3,''),$4,$5::jsonb,$6,$7) ON CONFLICT(candidate_id) DO UPDATE SET decision_status=EXCLUDED.decision_status,analyst_id=EXCLUDED.analyst_id,rationale=EXCLUDED.rationale,evidence_refs=EXCLUDED.evidence_refs,decision_version=EXCLUDED.decision_version,request_id=EXCLUDED.request_id,decided_at=now(),updated_at=now() RETURNING decision_id`, input.CandidateID, input.Status, input.AnalystID, input.Rationale, input.EvidenceRefs, version, input.RequestID).Scan(&decisionID)
	if err != nil {
		return DecisionResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tg_relationship_candidates SET current_status=$2,current_version=$3,last_evaluated_at=now(),updated_at=now() WHERE candidate_id=$1`, input.CandidateID, input.Status, version); err != nil {
		return DecisionResult{}, err
	}
	operation := "NO_CONFIRMED_RELATIONSHIP"
	if input.Status == "CONFIRMED" {
		operation = "UPSERT_CONFIRMED_RELATIONSHIP"
	}
	projectionGID := AnalystProjectionGID(input.CandidateID, version)
	projectionID := projection.NumericID(projectionGID)
	provenance, _ := json.Marshal(map[string]any{"candidate_gid": candidateGID, "decision_status": input.Status, "decision_version": version, "decision_id": decisionID, "history_id": historyID, "evidence_refs": json.RawMessage(input.EvidenceRefs)})
	if _, err := tx.Exec(ctx, `INSERT INTO tg_graph_projection_outbox(projection_id,projection_gid,candidate_id,decision_version,desired_operation,relationship_type,source_gid,target_gid,provenance) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb) ON CONFLICT(candidate_id,decision_version) DO NOTHING`, projectionID, projectionGID, input.CandidateID, version, operation, relationshipType, sourceGID, targetGID, provenance); err != nil {
		return DecisionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{CandidateID: input.CandidateID, CandidateGID: candidateGID, PreviousStatus: existingStatus, Status: input.Status, DecisionVersion: version, DecisionID: decisionID, HistoryID: historyID, ProjectionID: projectionID, ProjectionGID: projectionGID, DesiredOperation: operation, RequestID: input.RequestID}, nil
}

func validateDecisionTransition(previous string, version int, next string) error {
	if !validDecisionStatuses[next] {
		return fmt.Errorf("invalid decision status %q", next)
	}
	if version > 0 && previous == next {
		return fmt.Errorf("candidate already has decision %s; reopen with NEEDS_REVIEW or supersede it with a different status", previous)
	}
	return nil
}
