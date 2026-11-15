package srv

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vyuha/api/internal/intel"
)

func pathInt(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(r.PathValue(name)), 10, 64)
}

func (s *Server) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(qp(r, "q", ""))
	if len(q) < 2 {
		JSON(w, http.StatusOK, map[string]any{"data": []any{}, "query": q})
		return
	}
	like := "%" + q + "%"
	rows, err := s.Query(r.Context(), `
		SELECT 'company' AS type, e.id::text AS id, e.canonical_name AS label,
		       COALESCE(e.ticker_nse,'') AS meta FROM entities e
		WHERE e.canonical_name ILIKE $1 OR e.normalized_name ILIKE $1 OR e.ticker_nse ILIKE $1
		ORDER BY e.canonical_name LIMIT 8`, like)
	if err != nil {
		sendErr(w, err)
		return
	}
	tenders, err := s.Query(r.Context(), `SELECT 'tender' type, t.tender_id::text id, COALESCE(m.title,t.tender_id) label, COALESCE(t.organization,'') meta FROM tenders t LEFT JOIN LATERAL (SELECT title FROM tender_extracted_meta WHERE tender_id=t.id ORDER BY updated_at DESC NULLS LAST,id DESC LIMIT 1) m ON true WHERE t.tender_id ILIKE $1 OR m.title ILIKE $1 OR t.organization ILIKE $1 ORDER BY t.created_at DESC NULLS LAST LIMIT 8`, like)
	if err != nil {
		sendErr(w, err)
		return
	}
	participants, err := s.Query(r.Context(), `SELECT 'bidder' type, p.id::text id, COALESCE(p.participant_name,p.participant_normalized,'') label, COALESCE(p.participant_normalized,'') meta FROM tender_participants p WHERE p.participant_name ILIKE $1 OR p.participant_normalized ILIKE $1 ORDER BY p.id DESC LIMIT 8`, like)
	if err != nil {
		sendErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"data": append(append(rows, tenders...), participants...), "query": q})
}

func (s *Server) BidderProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "bidID")
	if err != nil {
		fail(w, 400, "invalid bidder id")
		return
	}
	profile, err := s.Intel.Profile(r.Context(), id, qpInt(r, "limit", 25, 100))
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": profile})
}
func (s *Server) BidderCoBidders(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "bidID")
	if err != nil {
		fail(w, 400, "invalid bidder id")
		return
	}
	rows, err := s.Intel.CoBidders(r.Context(), id, qpInt(r, "limit", 25, 100))
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": rows, "limit": qpInt(r, "limit", 25, 100)})
}
func (s *Server) BidderNetwork(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "bidID")
	if err != nil {
		fail(w, 400, "invalid bidder id")
		return
	}
	depth := qpInt(r, "depth", 2, 3)
	limit := qpInt(r, "limit", 50, 200)
	network, err := s.Intel.Network(r.Context(), id, depth, limit)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": network})
}
func (s *Server) BidderPair(w http.ResponseWriter, r *http.Request) {
	left, err1 := strconv.ParseInt(qp(r, "left", ""), 10, 64)
	right, err2 := strconv.ParseInt(qp(r, "right", ""), 10, 64)
	if err1 != nil || err2 != nil {
		fail(w, 400, "left and right bidder ids are required")
		return
	}
	pair, err := s.Intel.Pair(r.Context(), left, right, qpInt(r, "limit", 50, 200))
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": pair})
}
func (s *Server) CompanyIntelligence(w http.ResponseWriter, r *http.Request) {
	limit := qpInt(r, "limit", 50, 200)
	report, err := s.Intel.Company(r.Context(), strings.TrimSpace(r.PathValue("entityId")), limit)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": report})
}
func (s *Server) ReviewCandidates(w http.ResponseWriter, r *http.Request) {
	filter := intel.CandidateFilter{RelationshipType: qp(r, "type", ""), Status: qp(r, "status", ""), Method: qp(r, "method", ""), Limit: qpInt(r, "limit", 25, 100), Offset: qpOff(r)}
	if qp(r, "ranked", "") == "1" {
		// Ranked queue: ordered by the active ranker (baseline while no
		// learned model is promoted). Baseline scores are always attached.
		items, err := s.Ranking.RankedQueue(r.Context(), filter)
		if err != nil {
			sendWorkspaceErr(w, err)
			return
		}
		JSON(w, 200, map[string]any{"data": items, "limit": filter.Limit, "offset": filter.Offset})
		return
	}
	items, err := s.Review.ListCandidates(r.Context(), filter)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": items, "limit": qpInt(r, "limit", 25, 100), "offset": qpOff(r)})
}

// NotableBidders returns observed participant records with the widest
// distinct co-bidder networks. These are investigation entry points, not risk
// or wrongdoing rankings.
func (s *Server) NotableBidders(w http.ResponseWriter, r *http.Request) {
	limit := qpInt(r, "limit", 25, 100)
	rows, err := s.Intel.Rankings(r.Context(), "co-bidding", limit)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": rows, "limit": limit, "calculation": "distinct observed participant keys ranked by distinct co-bidders; this is an exploration index, not a risk score"})
}

// ReviewRankingStatus exposes the continual-learning system state: active
// ranker, label counts, exclusions, promotion state and current metrics.
func (s *Server) ReviewRankingStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.Ranking.Status(r.Context())
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": status})
}

// ReviewRankingQueue is the ranked review queue (equivalent to
// ReviewCandidates?ranked=1) kept as an explicit route for clarity.
func (s *Server) ReviewRankingQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.Ranking.RankedQueue(r.Context(), intel.CandidateFilter{RelationshipType: qp(r, "type", ""), Status: qp(r, "status", ""), Method: qp(r, "method", ""), Limit: qpInt(r, "limit", 25, 100), Offset: qpOff(r)})
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": items, "limit": qpInt(r, "limit", 25, 100), "offset": qpOff(r)})
}
func (s *Server) ReviewCandidate(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "candidateID")
	if err != nil {
		fail(w, 400, "invalid candidate id")
		return
	}
	c, err := s.Review.GetCandidate(r.Context(), id)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	h, err := s.Review.History(r.Context(), id)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": map[string]any{"candidate": c, "history": h}})
}
func (s *Server) ReviewHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "candidateID")
	if err != nil {
		fail(w, 400, "invalid candidate id")
		return
	}
	h, err := s.Review.History(r.Context(), id)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": h})
}
func (s *Server) ReviewDecision(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "candidateID")
	if err != nil {
		fail(w, 400, "invalid candidate id")
		return
	}
	var body struct {
		Status          string          `json:"status"`
		Rationale       string          `json:"rationale"`
		ActorID         string          `json:"actor_id"`
		ActorKind       string          `json:"actor_kind"`
		EvidenceRefs    json.RawMessage `json:"evidence_refs"`
		ExpectedVersion *int            `json:"expected_version"`
		RequestID       string          `json:"request_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "invalid JSON body")
		return
	}
	result, err := s.Review.Decide(r.Context(), intel.DecisionInput{CandidateID: id, Status: body.Status, Rationale: body.Rationale, ActorID: body.ActorID, ActorKind: body.ActorKind, EvidenceRefs: body.EvidenceRefs, ExpectedVersion: body.ExpectedVersion, RequestID: body.RequestID})
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": result})
}
func (s *Server) ProjectionStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Projection.Status(r.Context())
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": rows})
}
func (s *Server) ProjectionList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Projection.List(r.Context(), intel.ProjectionFilter{Status: qp(r, "status", ""), Limit: qpInt(r, "limit", 25, 100)})
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": rows})
}
func (s *Server) DataStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Query(r.Context(), `SELECT d.dataset_name AS dataset,COALESCE(w.status,'NEVER_RUN') AS status,w.updated_at,COALESCE(run.records_projected,0) AS records_projected,COALESCE(run.records_failed,0) AS records_failed FROM tg_datasets d LEFT JOIN tg_sync_watermarks w ON w.dataset_name=d.dataset_name LEFT JOIN LATERAL (SELECT records_projected,records_failed FROM tg_sync_runs WHERE dataset_name=d.dataset_name ORDER BY run_id DESC LIMIT 1) run ON true ORDER BY d.dataset_name`)
	if err != nil {
		sendWorkspaceErr(w, err)
		return
	}
	JSON(w, 200, map[string]any{"data": rows})
}
func (s *Server) WorkspaceHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbOK := s.Pool.Ping(ctx) == nil
	out := map[string]any{"status": "degraded", "postgres": dbOK, "hydradb": "not_checked"}
	if dbOK {
		out["status"] = "ready"
	}
	JSON(w, 200, out)
}
func sendWorkspaceErr(w http.ResponseWriter, err error) {
	httpLog(err)
	status := http.StatusInternalServerError
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		status = http.StatusNotFound
	}
	JSON(w, status, map[string]any{"error": "workspace request failed"})
}
