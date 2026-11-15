package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"time"

	"vyuha/api/internal/graph"
)

func main() {
	cfg := graph.LoadGraphConfig()
	fmt.Fprintln(os.Stderr, "cfg:", cfg.String())
	client := graph.NewHTTPClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// 1. do the same query through the real client
	res, err := client.DoQuery(ctx, `MATCH (start:Bid)-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid) WHERE start.src_pk = $bid_pk AND other.src_pk <> $bid_pk RETURN DISTINCT other.src_pk AS bid_pk ORDER BY bid_pk LIMIT $limit`,
		map[string]any{"bid_pk": 1760, "limit": 5})
	if err != nil {
		fmt.Fprintln(os.Stderr, "client query error:", err)
	} else {
		b, _ := json.MarshalIndent(res, "", " ")
		fmt.Println("client result:", string(b))
	}

	// 2. raw replay of exactly what the client would send
	base := "http://127.0.0.1:8443"
	body := map[string]any{
		"cell_id":    cfg.CellID,
		"query":      `MATCH (start:Bid)-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid) WHERE start.src_pk = $bid_pk AND other.src_pk <> $bid_pk RETURN DISTINCT other.src_pk AS bid_pk ORDER BY bid_pk LIMIT $limit`,
		"parameters": map[string]any{"bid_pk": 1760, "limit": 5},
		"page_size":  4096,
	}
	bj, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/v1/graphs/"+cfg.Database+"/query", bytes.NewReader(bj))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Graph-Namespace", cfg.Namespace)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	dump, _ := httputil.DumpRequestOut(req, true)
	fmt.Fprintln(os.Stderr, "--- wire request ---")
	fmt.Fprintln(os.Stderr, string(dump))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "raw error:", err)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	fmt.Fprintf(os.Stderr, "--- raw status %d ---\n", resp.StatusCode)
	fmt.Println(string(rb))
}
