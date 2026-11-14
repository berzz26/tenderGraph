package ingest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"vyuha/api/internal/projection"
)

type Engine struct {
	Scanner     SourceScanner
	Writer      projection.HydraWriter
	Control     ControlStore
	Registry    Registry
	Verify      bool
	MaxAttempts int
	BatchSize   int
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
	AfterWrite  func(context.Context, RunSummary, projection.Batch) error
}

func (e *Engine) RunAll(ctx context.Context, mode string) ([]RunSummary, error) {
	var out []RunSummary
	for _, spec := range e.Registry.Order() {
		summary, err := e.RunDataset(ctx, spec.Name, mode)
		out = append(out, summary)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Preview scans bounded source batches and performs no control-plane or graph
// writes. It is the operational dry-run path.
func (e *Engine) Preview(ctx context.Context, name string, mode string) (RunSummary, error) {
	spec, ok := e.Registry.Get(name)
	if !ok {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED"}, fmt.Errorf("unknown dataset %s", name)
	}
	high, err := e.Scanner.HighWatermark(ctx, spec)
	if err != nil {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: err.Error()}, err
	}
	if high.Empty() {
		return RunSummary{Dataset: name, Mode: mode, Status: "SUCCEEDED"}, nil
	}
	e.Scanner.RecomputePairs = mode == "incremental" || mode == "retry"
	wm, err := e.Control.GetWatermark(ctx, name)
	if err != nil {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: err.Error()}, err
	}
	current := wm.Cursor
	if mode == "initial" || mode == "selected" {
		current = Cursor{}
	} else if mode == "incremental" || mode == "retry" {
		current = current.WithLookback(spec.Lookback)
	}
	limit := e.BatchSize
	if limit <= 0 {
		limit = spec.BatchSize
	}
	if limit > 1000 {
		limit = 1000
	}
	summary := RunSummary{Dataset: name, Mode: mode, Status: "SUCCEEDED"}
	for {
		batch, err := e.Scanner.Scan(ctx, spec, current, high, limit, time.Now().UnixMilli())
		if err != nil {
			return summary, err
		}
		if batch.Items == 0 {
			break
		}
		summary.RecordsScanned += int64(batch.Items)
		deduped, err := deduplicate(batch.Batch)
		if err != nil {
			return summary, err
		}
		summary.RecordsProjected += int64(len(deduped.Nodes))
		summary.Batches++
		if !current.Empty() && !current.Less(batch.Cursor) {
			return summary, fmt.Errorf("preview cursor did not advance")
		}
		current = batch.Cursor
	}
	return summary, nil
}

func (e *Engine) RunDataset(ctx context.Context, name, mode string) (RunSummary, error) {
	spec, ok := e.Registry.Get(name)
	if !ok {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: "unknown dataset"}, fmt.Errorf("unknown dataset %s", name)
	}
	if mode != "initial" && mode != "incremental" && mode != "selected" && mode != "retry" {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: "invalid mode"}, fmt.Errorf("invalid mode %s", mode)
	}
	e.Scanner.RecomputePairs = mode == "incremental" || mode == "retry"
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Sleep == nil {
		e.Sleep = sleepContext
	}
	if e.MaxAttempts <= 0 {
		e.MaxAttempts = 3
	}
	limit := e.BatchSize
	if limit <= 0 {
		limit = spec.BatchSize
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if err := e.Control.Seed(ctx, e.Registry); err != nil {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: err.Error()}, err
	}
	runID, started, err := e.Control.StartRun(ctx, name, mode)
	if err != nil {
		return RunSummary{Dataset: name, Mode: mode, Status: "FAILED", Error: err.Error()}, err
	}
	summary := RunSummary{RunID: runID, Dataset: name, Mode: mode, Status: "RUNNING", StartedAt: started.UTC().Format(time.RFC3339Nano)}
	_ = e.Control.SetWatermarkRunning(ctx, name, runID)
	defer func() {
		if summary.Status == "RUNNING" {
			summary.Status = "FAILED"
			summary.Error = "run ended without completion"
		}
		_ = e.Control.FinishRun(context.Background(), summary)
	}()

	w, err := e.Control.GetWatermark(ctx, name)
	if err != nil {
		return e.failRun(ctx, summary, err)
	}
	start := w.Cursor
	if mode == "initial" || mode == "selected" {
		start = Cursor{}
	} else if mode == "incremental" || mode == "retry" {
		start = start.WithLookback(spec.Lookback)
	}
	high, err := e.Scanner.HighWatermark(ctx, spec)
	if err != nil {
		return e.failRun(ctx, summary, err)
	}
	if high.Empty() {
		summary.Status = "SUCCEEDED"
		return summary, nil
	}
	current := start
	for {
		if !current.Empty() && !current.Less(high) && !current.Equal(high) {
			break
		}
		scanStarted := e.Now()
		scanned, err := e.Scanner.Scan(ctx, spec, current, high, limit, e.Now().UnixMilli())
		if err != nil {
			return e.failRun(ctx, summary, err)
		}
		scanElapsedMS := e.Now().Sub(scanStarted).Milliseconds()
		if scanned.Items == 0 {
			break
		}
		if scanned.Cursor.Empty() || (!current.Empty() && !current.Less(scanned.Cursor)) {
			return e.failRun(ctx, summary, fmt.Errorf("dataset %s cursor did not advance: before=%s after=%s", name, current.String(), scanned.Cursor.String()))
		}
		batch, err := deduplicate(scanned.Batch)
		if err != nil {
			return e.failRun(ctx, summary, err)
		}
		batchID, err := e.Control.StartBatch(ctx, runID, name, current, scanned.Items)
		if err != nil {
			return e.failRun(ctx, summary, err)
		}
		batchStarted := e.Now()
		var writeErr error
		var writeReport projection.WriteReport
		writeElapsedMS := int64(0)
		verifyStarted := time.Time{}
		verifyElapsedMS := int64(0)
		for attempt := 1; attempt <= e.MaxAttempts; attempt++ {
			if attempt > 1 {
				_ = e.Control.IncrementBatchAttempt(ctx, batchID)
			}
			writeStarted := e.Now()
			writeReport, writeErr = e.Writer.Write(ctx, batch)
			writeElapsedMS = e.Now().Sub(writeStarted).Milliseconds()
			if writeErr == nil && e.Verify {
				verifyStarted = e.Now()
				writeErr = e.Writer.VerifyBatch(ctx, batch)
				verifyElapsedMS = e.Now().Sub(verifyStarted).Milliseconds()
			}
			if writeErr == nil && e.AfterWrite != nil {
				writeErr = e.AfterWrite(ctx, summary, batch)
			}
			if writeErr == nil {
				break
			}
			if attempt < e.MaxAttempts {
				_ = e.Sleep(ctx, time.Duration(attempt)*250*time.Millisecond)
			}
		}
		if writeErr != nil {
			_ = e.Control.FailBatch(ctx, batchID, writeErr)
			summary.RecordsFailed += int64(scanned.Items)
			return e.failRun(ctx, summary, writeErr)
		}
		checkpointStarted := e.Now()
		if err := e.Control.SucceedBatch(ctx, batchID, scanned.Cursor); err != nil {
			return e.failRun(ctx, summary, err)
		}
		if err := e.Control.AdvanceWatermark(ctx, name, scanned.Cursor, runID); err != nil {
			return e.failRun(ctx, summary, err)
		}
		summary.RecordsScanned += int64(scanned.Items)
		summary.RecordsProjected += int64(scanned.Items)
		summary.Batches++
		_ = e.Control.UpdateRunProgress(ctx, summary)
		current = scanned.Cursor
		log.Printf(`{"component":"tendergraph-ingest","dataset":%q,"run_id":%d,"batch_id":%d,"records":%d,"nodes":%d,"edges":%d,"scan_ms":%d,"write_ms":%d,"verify_ms":%d,"checkpoint_ms":%d,"elapsed_ms":%d,"cursor":%q}`, name, runID, batchID, scanned.Items, writeReport.Nodes, writeReport.Edges, scanElapsedMS, writeElapsedMS, verifyElapsedMS, e.Now().Sub(checkpointStarted).Milliseconds(), e.Now().Sub(batchStarted).Milliseconds(), current.String())
	}
	summary.Status = "SUCCEEDED"
	if err := e.Control.SetWatermarkSucceeded(ctx, name, runID); err != nil {
		return e.failRun(ctx, summary, err)
	}
	return summary, nil
}

func (e *Engine) failRun(ctx context.Context, summary RunSummary, err error) (RunSummary, error) {
	summary.Status = "FAILED"
	summary.Error = err.Error()
	_ = e.Control.MarkDatasetFailed(ctx, summary.Dataset, summary.RunID)
	_ = e.Control.FinishRun(ctx, summary)
	return summary, err
}

func deduplicate(batch projection.Batch) (projection.Batch, error) {
	nodes := make([]projection.Node, 0, len(batch.Nodes))
	nodeIndex := map[int64]int{}
	for _, n := range batch.Nodes {
		if i, ok := nodeIndex[n.ID]; ok {
			if nodes[i].GID == n.GID {
				continue
			}
			return projection.Batch{}, &projection.CollisionError{ID: n.ID, ExistingGID: nodes[i].GID, IncomingGID: n.GID}
		} else {
			nodeIndex[n.ID] = len(nodes)
			nodes = append(nodes, n)
		}
	}
	edges := make([]projection.Edge, 0, len(batch.Edges))
	seen := map[string]bool{}
	for _, e := range batch.Edges {
		key := fmt.Sprintf("%s:%d:%d", e.Type, e.FromID, e.ToID)
		if !seen[key] {
			seen[key] = true
			edges = append(edges, e)
		}
	}
	return projection.Batch{Nodes: nodes, Edges: edges}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errNotImplemented = errors.New("not implemented")
