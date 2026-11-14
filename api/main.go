package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"vyuha/api/internal/cfg"
	"vyuha/api/internal/db"
	"vyuha/api/internal/srv"
)

func main() {
	c := cfg.Load()
	if c.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required (e.g. postgres://user:pass@host:port/vyuha)")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, c.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	s := srv.New(pool)
	mux := http.NewServeMux()

	// Overview
	mux.HandleFunc("GET /api/v1/overview/kpis", s.OverviewKpis)
	mux.HandleFunc("GET /api/v1/intelligence/summary", s.IntelligenceSummary)
	mux.HandleFunc("GET /api/v1/intelligence/l1", s.IntelligenceL1)
	mux.HandleFunc("GET /api/v1/intelligence/awards", s.IntelligenceAwards)

	// Financial evaluation (participant level)
	mux.HandleFunc("GET /api/v1/financial-evaluation", s.ListFE)
	mux.HandleFunc("GET /api/v1/financial-evaluation/summary", s.SummaryFE)
	mux.HandleFunc("GET /api/v1/financial-evaluation/leaderboard", s.LeaderboardFE)

	// Awarded contracts (AOC stage)
	mux.HandleFunc("GET /api/v1/awards", s.ListAwards)
	mux.HandleFunc("GET /api/v1/awards/summary", s.SummaryAwards)
	mux.HandleFunc("GET /api/v1/awards/leaderboard", s.LeaderboardAwards)

	// Mapping methodology views
	mux.HandleFunc("GET /api/v1/mappings/summary", s.MappingsSummary)
	mux.HandleFunc("GET /api/v1/mappings/financial-evaluation", s.ListFE)
	mux.HandleFunc("GET /api/v1/mappings/awards", s.ListAwards)
	mux.HandleFunc("GET /api/v1/mappings/unresolved", s.UnresolvedNames)
	mux.HandleFunc("GET /api/v1/mappings/ambiguous", s.Ambiguous)

	// Companies
	mux.HandleFunc("GET /api/v1/companies", s.Companies)
	mux.HandleFunc("GET /api/v1/companies/{entityId}", s.CompanyDetail)
	mux.HandleFunc("GET /api/v1/market-data/{entityId}", s.MarketHistory)

	// Tender detail
	mux.HandleFunc("GET /api/v1/tenders/{tenderId}", s.TenderDetail)
	mux.HandleFunc("GET /api/v1/tenders/{tenderId}/files/{fileId}/download", s.TenderFileDownload)

	// Financial materiality and data quality
	mux.HandleFunc("GET /api/v1/materiality", s.Materiality)
	mux.HandleFunc("GET /api/v1/materiality/{entityId}", s.MaterialityCompany)
	mux.HandleFunc("GET /api/v1/data-quality", s.DataQuality)

	// Health
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// Static frontend build, when present.
	if st := infoStat("dist"); st {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "dist"+r.URL.Path)
		})
	}

	addr := "0.0.0.0" + c.ServerAddr
	log.Printf("vyuha-dash api listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func infoStat(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
