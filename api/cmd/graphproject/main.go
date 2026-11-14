// graphproject loads one real PostgreSQL tender and optionally projects it to
// HydraDB. It is intentionally a vertical-slice tool, not a backfill worker.
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
	"vyuha/api/internal/projection"
)

type report struct {
	TenderID      int64          `json:"tender_id"`
	ExternalID    string         `json:"external_id"`
	Nodes         int            `json:"nodes"`
	Edges         int            `json:"edges"`
	NodeLabels    map[string]int `json:"node_labels"`
	Relationships map[string]int `json:"relationships"`
	Evidence      map[string]int `json:"winner_evidence"`
	HashAlgorithm string         `json:"id_algorithm"`
	IDAlgoV       int64          `json:"id_algo_v"`
	Written       bool           `json:"written"`
	Cleaned       bool           `json:"cleaned"`
}

func main() {
	tenderID := flag.Int64("tender-id", 16234, "real tenders.id to project")
	write := flag.Bool("write", false, "write the vertical slice to the configured HydraDB graph")
	cleanup := flag.Bool("cleanup", true, "delete and verify the slice after a write")
	flag.Parse()

	appCfg := cfg.Load()
	if appCfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, appCfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	tender, bids, vendor, award, err := (projection.SourceAdapter{Pool: pool}).LoadVerticalSlice(ctx, *tenderID)
	if err != nil {
		log.Fatal(err)
	}
	batch, err := projection.BuildVerticalSlice(tender, bids, vendor, award, time.Now().UnixMilli())
	if err != nil {
		log.Fatal(err)
	}

	r := report{TenderID: tender.ID, ExternalID: tender.ExternalID, Nodes: len(batch.Nodes), Edges: len(batch.Edges), NodeLabels: map[string]int{}, Relationships: map[string]int{}, Evidence: projection.WinnerEvidenceSummary(bids, award), HashAlgorithm: projection.IDAlgorithmName(), IDAlgoV: projection.IDAlgoV}
	for _, n := range batch.Nodes {
		r.NodeLabels[n.Label]++
	}
	for _, e := range batch.Edges {
		r.Relationships[e.Type]++
	}

	if *write {
		graphCfg := graph.LoadGraphConfig()
		clients, err := graph.Open(graphCfg)
		if err != nil {
			log.Fatal(err)
		}
		defer clients.Close(context.Background())
		writer := projection.HydraWriter{Client: clients.HTTP}
		if err := writer.EnsureAbsent(ctx, batch.Nodes); err != nil {
			log.Fatal(err)
		}
		if _, err := writer.Write(ctx, batch); err != nil {
			log.Fatal(err)
		}
		r.Written = true
		if *cleanup {
			if err := writer.DeleteAndVerify(ctx, batch.Nodes); err != nil {
				log.Fatal(err)
			}
			r.Cleaned = true
		}
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
}
