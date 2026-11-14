package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ControlStore struct{ Pool *pgxpool.Pool }

type Watermark struct {
	Dataset string
	Cursor  Cursor
	RunID   int64
	Status  string
}

type RunSummary struct {
	RunID            int64  `json:"run_id"`
	Dataset          string `json:"dataset"`
	Mode             string `json:"mode"`
	Status           string `json:"status"`
	RecordsScanned   int64  `json:"records_scanned"`
	RecordsProjected int64  `json:"records_projected"`
	RecordsFailed    int64  `json:"records_failed"`
	Batches          int64  `json:"batches"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
	Error            string `json:"error,omitempty"`
}

func (s ControlStore) Seed(ctx context.Context, registry Registry) error {
	for _, item := range registry.All() {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO tg_datasets(dataset_name, source_table, strategy, batch_size, description)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (dataset_name) DO UPDATE SET source_table=EXCLUDED.source_table, strategy=EXCLUDED.strategy, description=EXCLUDED.description`,
			item.Name, item.SourceTable, string(item.Strategy), item.BatchSize, item.Description); err != nil {
			return fmt.Errorf("seed dataset %s: %w", item.Name, err)
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO tg_sync_watermarks(dataset_name) VALUES ($1) ON CONFLICT DO NOTHING`, item.Name); err != nil {
			return fmt.Errorf("seed watermark %s: %w", item.Name, err)
		}
	}
	return nil
}

func (s ControlStore) GetWatermark(ctx context.Context, dataset string) (Watermark, error) {
	var w Watermark
	var ts *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT dataset_name, cursor_time, COALESCE(cursor_pk,''), COALESCE(last_run_id,0), status FROM tg_sync_watermarks WHERE dataset_name=$1`, dataset).
		Scan(&w.Dataset, &ts, &w.Cursor.PK, &w.RunID, &w.Status); err != nil {
		if err == pgx.ErrNoRows {
			return Watermark{Dataset: dataset, Status: "NEVER_RUN"}, nil
		}
		return w, fmt.Errorf("get watermark %s: %w", dataset, err)
	}
	w.Cursor.Time = ts
	return w, nil
}

func (s ControlStore) SetWatermarkRunning(ctx context.Context, dataset string, runID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_watermarks SET status='RUNNING', last_run_id=$2, updated_at=now() WHERE dataset_name=$1`, dataset, runID)
	return err
}

func (s ControlStore) AdvanceWatermark(ctx context.Context, dataset string, cursor Cursor, runID int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE tg_sync_watermarks SET cursor_time=$2, cursor_pk=$3, last_run_id=$4, status='RUNNING', updated_at=now() WHERE dataset_name=$1 AND last_run_id=$4`, dataset, cursor.Time, cursor.PK, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("watermark %s is no longer owned by run %d", dataset, runID)
	}
	return nil
}

func (s ControlStore) SetWatermarkSucceeded(ctx context.Context, dataset string, runID int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE tg_sync_watermarks SET status='SUCCEEDED', last_run_id=$2, updated_at=now() WHERE dataset_name=$1 AND last_run_id=$2`, dataset, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("watermark %s is no longer owned by run %d", dataset, runID)
	}
	return nil
}

func (s ControlStore) StartRun(ctx context.Context, dataset, mode string) (int64, time.Time, error) {
	_, _ = s.Pool.Exec(ctx, `UPDATE tg_sync_batches b SET status='FAILED', finished_at=now(), error='superseded by a later run'
		FROM tg_sync_runs r WHERE b.run_id=r.run_id AND r.dataset_name=$1 AND r.status='RUNNING' AND b.status='RUNNING'`, dataset)
	_, _ = s.Pool.Exec(ctx, `UPDATE tg_sync_runs SET status='FAILED', finished_at=now(), error='superseded by a later run' WHERE dataset_name=$1 AND status='RUNNING'`, dataset)
	var id int64
	var started time.Time
	err := s.Pool.QueryRow(ctx, `INSERT INTO tg_sync_runs(dataset_name, mode) VALUES ($1,$2) RETURNING run_id, started_at`, dataset, mode).Scan(&id, &started)
	return id, started, err
}

func (s ControlStore) UpdateRunProgress(ctx context.Context, summary RunSummary) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_runs SET records_scanned=$2, records_projected=$3, records_failed=$4 WHERE run_id=$1`, summary.RunID, summary.RecordsScanned, summary.RecordsProjected, summary.RecordsFailed)
	return err
}

func (s ControlStore) IncrementBatchAttempt(ctx context.Context, batchID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_batches SET attempt_count=attempt_count+1 WHERE batch_id=$1`, batchID)
	return err
}

func (s ControlStore) FinishRun(ctx context.Context, summary RunSummary) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_runs SET finished_at=now(), status=$2, records_scanned=$3, records_projected=$4, records_failed=$5, error=$6 WHERE run_id=$1`, summary.RunID, summary.Status, summary.RecordsScanned, summary.RecordsProjected, summary.RecordsFailed, summary.Error)
	return err
}

func (s ControlStore) StartBatch(ctx context.Context, runID int64, dataset string, before Cursor, rows int) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO tg_sync_batches(run_id,dataset_name,cursor_before_time,cursor_before_pk,row_count,attempt_count)
		VALUES ($1,$2,$3,$4,$5,1) RETURNING batch_id`, runID, dataset, before.Time, before.PK, rows).Scan(&id)
	return id, err
}

func (s ControlStore) SucceedBatch(ctx context.Context, batchID int64, after Cursor) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_batches SET status='SUCCEEDED', cursor_after_time=$2, cursor_after_pk=$3, finished_at=now() WHERE batch_id=$1`, batchID, after.Time, after.PK)
	return err
}

func (s ControlStore) FailBatch(ctx context.Context, batchID int64, cause error) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_batches SET status='FAILED', error=$2, finished_at=now() WHERE batch_id=$1`, batchID, cause.Error())
	return err
}

func (s ControlStore) MarkDatasetFailed(ctx context.Context, dataset string, runID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tg_sync_watermarks SET status='FAILED', last_run_id=$2, updated_at=now() WHERE dataset_name=$1 AND last_run_id=$2`, dataset, runID)
	return err
}

func (s ControlStore) FailedBatches(ctx context.Context, dataset string, limit int) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT batch_id, run_id, dataset_name, cursor_before_time, cursor_before_pk, row_count, attempt_count, error, started_at
		FROM tg_sync_batches WHERE dataset_name=$1 AND status='FAILED' ORDER BY batch_id LIMIT $2`, dataset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, runID int64
		var name, errorText string
		var pk *string
		var ts *time.Time
		var started time.Time
		var count, attempts int
		if err := rows.Scan(&id, &runID, &name, &ts, &pk, &count, &attempts, &errorText, &started); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"batch_id": id, "run_id": runID, "dataset": name, "cursor_before_time": ts, "cursor_before_pk": pk, "row_count": count, "attempt_count": attempts, "error": errorText, "started_at": started})
	}
	return out, rows.Err()
}

func (s ControlStore) Status(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT d.dataset_name, d.source_table, d.strategy, d.enabled, d.batch_size, w.cursor_time, COALESCE(w.cursor_pk,''), COALESCE(w.status,'NEVER_RUN'), w.last_run_id, w.updated_at
		FROM tg_datasets d LEFT JOIN tg_sync_watermarks w USING(dataset_name) ORDER BY d.dataset_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, source, strategy, status, pk string
		var enabled bool
		var size int
		var ts, updated *time.Time
		var runID *int64
		if err := rows.Scan(&name, &source, &strategy, &enabled, &size, &ts, &pk, &status, &runID, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"dataset": name, "source_table": source, "strategy": strategy, "enabled": enabled, "batch_size": size, "cursor_time": ts, "cursor_pk": pk, "status": status, "last_run_id": runID, "updated_at": updated})
	}
	return out, rows.Err()
}

func (s ControlStore) ReconciliationRecord(ctx context.Context, dataset, mode, status string, missingNodes, missingEdges, staleNodes, invalidProvenance, duplicateEdges int64, examples []any, errText string) error {
	b, _ := json.Marshal(examples)
	_, err := s.Pool.Exec(ctx, `INSERT INTO tg_reconciliation_runs(dataset_name,mode,finished_at,status,missing_nodes,missing_edges,stale_nodes,invalid_provenance,duplicate_logical_edges,examples,error)
		VALUES ($1,$2,now(),$3,$4,$5,$6,$7,$8,$9,$10)`, dataset, mode, status, missingNodes, missingEdges, staleNodes, invalidProvenance, duplicateEdges, b, errText)
	return err
}

func (s ControlStore) LastReconciliation(ctx context.Context, dataset string) (map[string]any, error) {
	var id int64
	var status, mode string
	var started time.Time
	var finished *time.Time
	var missingNodes, missingEdges, staleNodes, invalid, duplicate int64
	var examples []byte
	err := s.Pool.QueryRow(ctx, `SELECT reconciliation_id,mode,status,started_at,finished_at,missing_nodes,missing_edges,stale_nodes,invalid_provenance,duplicate_logical_edges,examples FROM tg_reconciliation_runs WHERE dataset_name=$1 ORDER BY reconciliation_id DESC LIMIT 1`, dataset).
		Scan(&id, &mode, &status, &started, &finished, &missingNodes, &missingEdges, &staleNodes, &invalid, &duplicate, &examples)
	if err != nil {
		return nil, err
	}
	return map[string]any{"reconciliation_id": id, "dataset": dataset, "mode": mode, "status": status, "started_at": started, "finished_at": finished, "missing_nodes": missingNodes, "missing_edges": missingEdges, "stale_nodes": staleNodes, "invalid_provenance": invalid, "duplicate_logical_edges": duplicate, "examples": json.RawMessage(examples)}, nil
}
