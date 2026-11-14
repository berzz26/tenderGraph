// graphhealth is a standalone CLI that prints the HydraDB dependency health
// report as JSON and exits 0 (healthy) / 1 (degraded or unavailable).
// It exists so the integration can be exercised without touching the main
// Vyuha API process, which must never depend on HydraDB availability.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"vyuha/api/internal/graph"
)

func main() {
	timeout := flag.Int("timeout", 5, "per-probe timeout in seconds")
	flag.Parse()

	cfg := graph.LoadGraphConfig()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	graph.Logf("config %s", cfg) // token redacted by Config.String

	if cfg.HealthTimeoutSec == 0 {
		cfg.HealthTimeoutSec = *timeout
	}
	clients, err := graph.Open(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open error:", err)
		os.Exit(1)
	}
	defer clients.Close(context.Background())

	rep := clients.Health.Check(context.Background())
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	if rep.Status != "healthy" {
		os.Exit(1)
	}
}
