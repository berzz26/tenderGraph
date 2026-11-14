// graphsync is the operational Phase 3 synchronization CLI. It is deliberately
// sequential per HydraDB cell; no worker pool or broker is introduced.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/ingest"
	"vyuha/api/internal/projection"
)

func main() {
	op := flag.String("op", "list", "list|initial|incremental|selected|retry|status|failed|reconcile|repair")
	dataset := flag.String("dataset", "", "dataset name; empty means all for initial/incremental")
	batchSize := flag.Int("batch-size", 500, "PostgreSQL/HydraDB batch size, 1..1000")
	verify := flag.Bool("verify", true, "verify projected nodes and edges through HydraDB reads")
	dryRun := flag.Bool("dry-run", false, "scan and report without graph or control-plane writes")
	reconcileMode := flag.String("reconcile-mode", "detect", "detect|repair")
	flag.Parse()
	if *batchSize < 1 || *batchSize > 1000 {
		log.Fatal("batch-size must be between 1 and 1000")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	appCfg := cfg.Load()
	if appCfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, appCfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	registry := ingest.ApprovedRegistry()
	control := ingest.ControlStore{Pool: pool}
	if err := control.Seed(ctx, registry); err != nil {
		log.Fatal(err)
	}

	if *op == "list" {
		printJSON(registry.All())
		return
	}
	if *op == "status" {
		v, err := control.Status(ctx)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(v)
		return
	}
	if *op == "failed" {
		v, err := control.FailedBatches(ctx, *dataset, 100)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(v)
		return
	}

	if *op == "reconcile" || *op == "repair" {
		if *dataset == "" {
			log.Fatal("-dataset is required for reconciliation")
		}
		clients := openGraph()
		defer clients.Close(context.Background())
		r := ingest.Reconciler{Scanner: ingest.SourceScanner{Pool: pool}, Writer: projection.HydraWriter{Client: clients.HTTP, BatchSize: *batchSize}, Control: control, Registry: registry, BatchSize: *batchSize}
		mode := *reconcileMode
		if *op == "repair" {
			mode = "repair"
		}
		report, err := r.Run(ctx, *dataset, mode)
		if err != nil {
			printJSON(report)
			os.Exit(1)
		}
		printJSON(report)
		return
	}

	if *op != "initial" && *op != "incremental" && *op != "selected" && *op != "retry" {
		log.Fatalf("unknown op %s", *op)
	}
	clients := openGraph()
	defer clients.Close(context.Background())
	engine := &ingest.Engine{Scanner: ingest.SourceScanner{Pool: pool}, Writer: projection.HydraWriter{Client: clients.HTTP, BatchSize: *batchSize}, Control: control, Registry: registry, Verify: *verify, BatchSize: *batchSize}
	if *dryRun {
		if *dataset == "" {
			log.Fatal("-dataset is required with -dry-run")
		}
		summary, err := engine.Preview(ctx, *dataset, *op)
		if err != nil {
			log.Fatal(err)
		}
		printJSON(summary)
		return
	}
	if *dataset != "" {
		summary, err := engine.RunDataset(ctx, *dataset, *op)
		if err != nil {
			printJSON(summary)
			os.Exit(1)
		}
		printJSON(summary)
		return
	}
	summaries, err := engine.RunAll(ctx, *op)
	if err != nil {
		printJSON(summaries)
		os.Exit(1)
	}
	printJSON(summaries)
}

func openGraph() *graph.Clients {
	clients, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		log.Fatal(err)
	}
	return clients
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(b))
}
