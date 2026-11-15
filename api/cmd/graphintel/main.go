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
	relationshipType := fs.String("type", "", "review relationship type")
	status := fs.String("status", "", "review status")
	method := fs.String("method", "", "candidate generation method")
	offset := fs.Int("offset", 0, "review queue offset")
	reason := fs.String("reason", "", "analyst decision rationale")
	analyst := fs.String("analyst", "", "analyst identifier, when available")
	actorKind := fs.String("actor-kind", "OPERATOR_SUPPLIED", "actor kind: AUTHENTICATED|OPERATOR_SUPPLIED|SYSTEM")
	evidenceJSON := fs.String("evidence-json", "[]", "JSON evidence references")
	expectedVersion := fs.Int("expected-version", -1, "expected candidate version for optimistic concurrency")
	requestID := fs.String("request-id", "", "idempotency key for a decision request")
	dryRun := fs.Bool("dry-run", false, "show projection work without mutating HydraDB")
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
	needsGraph := commandNeedsGraph(os.Args[1])
	if os.Args[1] == "projection" && len(args) > 0 && (args[0] == "apply" && !*dryRun || args[0] == "reconcile") {
		needsGraph = true
	}
	var clients *graph.Clients
	if needsGraph {
		clients, err = graph.Open(graph.LoadGraphConfig())
		if err != nil {
			log.Fatal(err)
		}
		defer clients.Close(context.Background())
	}
	var graphClient *graph.HTTPClient
	if clients != nil {
		graphClient = clients.HTTP
	}
	svc := intel.Service{Pool: pool, Graph: graphClient}
	review := intel.ReviewStore{Pool: pool}
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
	case "refresh-candidates":
		result, err = review.RefreshCandidates(ctx)
	case "review-queue":
		queueStatus := *status
		if queueStatus == "" {
			queueStatus = "PENDING"
		}
		result, err = review.ListCandidates(ctx, intel.CandidateFilter{RelationshipType: *relationshipType, Status: queueStatus, Method: *method, Limit: *limit, Offset: *offset})
	case "candidate":
		requireArgs(args, 1)
		result, err = review.GetCandidate(ctx, parseID(args[0]))
	case "decision":
		requireArgs(args, 1)
		if strings.TrimSpace(*analyst) == "" {
			log.Fatal("--analyst is required; use an authenticated or operator-supplied actor identifier")
		}
		input := intel.DecisionInput{CandidateID: parseID(args[0]), Status: *status, Rationale: *reason, AnalystID: *analyst, ActorID: *analyst, ActorKind: *actorKind, EvidenceRefs: []byte(*evidenceJSON), RequestID: *requestID}
		if *expectedVersion >= 0 {
			input.ExpectedVersion = expectedVersion
		}
		result, err = review.Decide(ctx, input)
	case "history":
		requireArgs(args, 1)
		result, err = review.History(ctx, parseID(args[0]))
	case "projection":
		requireArgs(args, 1)
		consumer := intel.ProjectionConsumer{Pool: pool, Graph: graphClient}
		switch args[0] {
		case "status":
			result, err = consumer.Status(ctx)
		case "list":
			result, err = consumer.List(ctx, intel.ProjectionFilter{Status: *status, Limit: *limit})
		case "apply":
			result, err = consumer.ApplyStatus(ctx, statusOrDefault(*status, "PENDING"), *limit, *dryRun)
		case "reconcile":
			result, err = consumer.Reconcile(ctx, *limit)
		default:
			log.Fatalf("unknown projection operation %q; use status|list|apply|reconcile", args[0])
		}
	case "ranking":
		ranking := intel.RankingStore{Pool: pool}
		if len(args) == 0 {
			usage()
			os.Exit(2)
		}
		switch args[0] {
		case "status":
			result, err = ranking.Status(ctx)
		case "refresh":
			scored, err2 := ranking.ScoreBaseline(ctx)
			if err2 != nil {
				log.Fatal(err2)
			}
			modelVersion, learnedScored, err2 := ranking.ScoreLearned(ctx)
			if err2 != nil {
				log.Fatal(err2)
			}
			result = map[string]any{"baseline_scored": scored, "learned_model_version": modelVersion, "learned_scored": learnedScored}
		case "train":
			model, decision, err2 := ranking.TrainAndEvaluate(ctx, true)
			if err2 != nil {
				log.Fatal(err2)
			}
			result = map[string]any{"model": model, "promotion_decision": decision}
		case "promote":
			requireArgs(args, 2)
			err = ranking.PromoteModel(ctx, args[1])
			if err == nil {
				result = map[string]any{"promoted": args[1]}
			}
		case "rollback":
			err = ranking.Rollback(ctx, *reason)
			if err == nil {
				result = map[string]any{"rolled_back": true, "reason": *reason}
			}
		case "queue":
			queueStatus := *status
			if queueStatus == "" {
				queueStatus = "PENDING"
			}
			result, err = ranking.RankedQueue(ctx, intel.CandidateFilter{RelationshipType: *relationshipType, Status: queueStatus, Method: *method, Limit: *limit, Offset: *offset})
		default:
			log.Fatalf("unknown ranking operation %q; use status|refresh|train|promote|rollback|queue", args[0])
		}
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
	fmt.Fprintln(os.Stderr, "usage: graphintel bidder|co-bidders|pair|network|tender|rankings|organization|company|exposure|announcements|link-candidates|award|degree|paths|connections|refresh-candidates|review-queue|candidate|decision|history|projection|ranking [args]")
}

func statusOrDefault(status, fallback string) string {
	if strings.TrimSpace(status) == "" {
		return fallback
	}
	return strings.ToUpper(strings.TrimSpace(status))
}

func commandNeedsGraph(command string) bool {
	switch command {
	case "co-bidders", "network", "degree", "paths", "connections":
		return true
	default:
		return false
	}
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
