package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/intel"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	limit := fs.Int("limit", 25, "maximum result rows")
	depth := fs.Int("depth", 2, "network depth, 1..3")
	by := fs.String("by", "participation", "ranking: participation|organizations|co-bidding")
	from := fs.String("from", "", "inclusive RFC3339 observation time")
	to := fs.String("to", "", "exclusive RFC3339 observation time")
	flagArgs, positional := splitArgs(os.Args[2:])
	_ = fs.Parse(flagArgs)
	args := positional
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	config := cfg.Load()
	if config.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	pool, err := db.Connect(ctx, config.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	clients, err := graph.Open(graph.LoadGraphConfig())
	if err != nil {
		log.Fatal(err)
	}
	defer clients.Close(context.Background())
	svc := intel.Service{Pool: pool, Graph: clients.HTTP}
	window, err := parseWindow(*from, *to)
	if err != nil {
		log.Fatal(err)
	}
	var result any
	switch os.Args[1] {
	case "bidder":
		requireArgs(args, 1)
		id := parseID(args[0])
		result, err = svc.ProfileWindow(ctx, id, *limit, window)
	case "co-bidders":
		requireArgs(args, 1)
		id := parseID(args[0])
		result, err = svc.CoBidders(ctx, id, *limit)
	case "pair":
		requireArgs(args, 2)
		result, err = svc.Pair(ctx, parseID(args[0]), parseID(args[1]), *limit)
	case "network":
		requireArgs(args, 1)
		result, err = svc.Network(ctx, parseID(args[0]), *depth, *limit)
	case "tender":
		requireArgs(args, 1)
		result, err = svc.Tender(ctx, parseID(args[0]), *limit)
	case "rankings":
		result, err = svc.Rankings(ctx, *by, *limit)
	case "organization":
		requireArgs(args, 1)
		result, err = svc.Organization(ctx, args[0], *limit)
	case "company":
		requireArgs(args, 1)
		result, err = svc.Company(ctx, args[0], *limit)
	case "exposure":
		requireArgs(args, 1)
		result, err = svc.Exposure(ctx, args[0], *limit)
	case "announcements":
		requireArgs(args, 1)
		result, err = svc.Announcements(ctx, args[0], *limit)
	case "link-candidates":
		requireArgs(args, 1)
		result, err = svc.LinkCandidates(ctx, args[0], *limit)
	case "award":
		requireArgs(args, 1)
		result, err = svc.Award(ctx, parseID(args[0]))
	case "degree":
		result, err = svc.NativeDegree(ctx, *limit)
	case "paths":
		requireArgs(args, 2)
		result, err = svc.NativeShortestPaths(ctx, parseID(args[0]), parseID(args[1]), *depth, *limit)
	case "connections":
		requireArgs(args, 1)
		result, err = svc.NativeConnections(ctx, parseID(args[0]), *depth, *limit)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
}

func parseID(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("invalid numeric source id %q", v)
	}
	return n
}
func requireArgs(args []string, n int) {
	if len(args) < n {
		log.Fatalf("expected %d positional argument(s)", n)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: graphintel bidder|co-bidders|pair|network|tender|rankings|organization|company|exposure|announcements|link-candidates|award|degree|paths|connections [args] [--limit N] [--depth N] [--from RFC3339 --to RFC3339]")
}

func parseWindow(from, to string) (intel.TimeWindow, error) {
	var w intel.TimeWindow
	if from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return w, fmt.Errorf("invalid --from: %w", err)
		}
		w.From = &t
	}
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return w, fmt.Errorf("invalid --to: %w", err)
		}
		w.To = &t
	}
	if w.From != nil && w.To != nil && !w.From.Before(*w.To) {
		return w, fmt.Errorf("--from must be before --to")
	}
	return w, nil
}

func splitArgs(raw []string) ([]string, []string) {
	var flags, positional []string
	for i := 0; i < len(raw); i++ {
		if strings.HasPrefix(raw[i], "-") {
			flagToken := raw[i]
			flags = append(flags, flagToken)
			if !strings.Contains(flagToken, "=") && i+1 < len(raw) && !strings.HasPrefix(raw[i+1], "-") {
				i++
				flags = append(flags, raw[i])
			}
			continue
		}
		positional = append(positional, raw[i])
	}
	return flags, positional
}
