// Package intel implements evidence-backed procurement network investigations.
// It deliberately treats participant_normalized as an observed source key,
// not as proof of a legal-company identity.
package intel

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"vyuha/api/internal/graph"
	"vyuha/api/internal/projection"
)

const defaultLimit = 25

type Service struct {
	Pool  *pgxpool.Pool
	Graph *graph.HTTPClient
}

type Evidence struct {
	SourceTable string `json:"source_table"`
	SourcePK    string `json:"source_pk"`
	GID         string `json:"gid"`
}

type BidRef struct {
	ID          int64  `json:"bid_id"`
	GID         string `json:"bid_gid"`
	Name        string `json:"participant_name"`
	ObservedKey string `json:"observed_participant_key"`
	TenderID    int64  `json:"tender_id"`
	TenderGID   string `json:"tender_gid"`
}

type Participation struct {
	BidID           int64      `json:"bid_id"`
	BidGID          string     `json:"bid_gid"`
	TenderID        int64      `json:"tender_id"`
	TenderGID       string     `json:"tender_gid"`
	ExternalTender  string     `json:"external_tender_id"`
	Organization    string     `json:"organization"`
	SubmittedAt     *time.Time `json:"submitted_at,omitempty"`
	FinancialRank   string     `json:"financial_rank,omitempty"`
	FinancialValue  string     `json:"financial_bid_value,omitempty"`
	TechnicalStatus string     `json:"technical_status,omitempty"`
	FinancialStatus string     `json:"financial_status,omitempty"`
	IsWinner        bool       `json:"is_winner"`
	Evidence        Evidence   `json:"evidence"`
}

type CoBidder struct {
	ObservedKey   string     `json:"observed_participant_key"`
	ExampleName   string     `json:"example_name"`
	SharedTenders int        `json:"shared_tenders"`
	FirstObserved *time.Time `json:"first_observed,omitempty"`
	LastObserved  *time.Time `json:"last_observed,omitempty"`
}

type Profile struct {
	Bid                   BidRef          `json:"selected_bid"`
	IdentityNote          string          `json:"identity_note"`
	BidRecords            int             `json:"bid_records"`
	DistinctTenders       int             `json:"distinct_tenders"`
	DistinctOrganizations int             `json:"distinct_organizations"`
	FirstObserved         *time.Time      `json:"first_observed,omitempty"`
	LastObserved          *time.Time      `json:"last_observed,omitempty"`
	CoBidTenderCount      int             `json:"co_bid_tender_count"`
	CoBidders             []CoBidder      `json:"top_co_bidders"`
	History               []Participation `json:"participation_history"`
	Calculation           string          `json:"calculation"`
}

type SharedTender struct {
	TenderID       int64      `json:"tender_id"`
	TenderGID      string     `json:"tender_gid"`
	ExternalTender string     `json:"external_tender_id"`
	Organization   string     `json:"organization"`
	ObservedAt     *time.Time `json:"observed_at,omitempty"`
	Evidence       []Evidence `json:"evidence"`
}

type PairAnalysis struct {
	LeftObservedKey   string         `json:"left_observed_participant_key"`
	RightObservedKey  string         `json:"right_observed_participant_key"`
	SharedTenderCount int            `json:"shared_tender_count"`
	LeftTenderCount   int            `json:"left_tender_count"`
	RightTenderCount  int            `json:"right_tender_count"`
	JaccardSimilarity float64        `json:"jaccard_similarity"`
	LeftCoverage      float64        `json:"left_coverage"`
	RightCoverage     float64        `json:"right_coverage"`
	SharedTenders     []SharedTender `json:"shared_tenders"`
	Calculation       string         `json:"calculation"`
}

type RankingRow struct {
	ObservedKey           string `json:"observed_participant_key"`
	ExampleName           string `json:"example_name"`
	BidRecords            int    `json:"bid_records"`
	DistinctTenders       int    `json:"distinct_tenders"`
	DistinctOrganizations int    `json:"distinct_organizations"`
	DistinctCoBidders     int    `json:"distinct_co_bidders,omitempty"`
}

type TenderParticipant struct {
	BidID           int64    `json:"bid_id"`
	BidGID          string   `json:"bid_gid"`
	Name            string   `json:"participant_name"`
	ObservedKey     string   `json:"observed_participant_key"`
	FinancialRank   string   `json:"financial_rank,omitempty"`
	FinancialValue  string   `json:"financial_bid_value,omitempty"`
	TechnicalStatus string   `json:"technical_status,omitempty"`
	FinancialStatus string   `json:"financial_status,omitempty"`
	IsWinner        bool     `json:"is_winner"`
	Evidence        Evidence `json:"evidence"`
}

type TenderAnalysis struct {
	TenderID          int64               `json:"tender_id"`
	TenderGID         string              `json:"tender_gid"`
	ExternalTender    string              `json:"external_tender_id"`
	Organization      string              `json:"organization"`
	Participants      []TenderParticipant `json:"participants"`
	CoBidObservations int                 `json:"co_bid_observations"`
	HistoricalPairs   []PairAnalysis      `json:"historical_pairs"`
}

type NetworkNode struct {
	BidID       int64  `json:"bid_id"`
	BidGID      string `json:"bid_gid"`
	ObservedKey string `json:"observed_participant_key"`
	Name        string `json:"participant_name"`
	Depth       int    `json:"depth"`
}

type Network struct {
	RootBidID int64         `json:"root_bid_id"`
	Depth     int           `json:"depth"`
	Limit     int           `json:"limit"`
	Nodes     []NetworkNode `json:"nodes"`
	Method    string        `json:"method"`
}

func syntheticObservedKey(key string) bool { return strings.HasPrefix(key, "bid:") }

func (s Service) requirePool() error {
	if s.Pool == nil {
		return fmt.Errorf("intel: nil PostgreSQL pool")
	}
	return nil
}

func (s Service) loadBid(ctx context.Context, id int64) (BidRef, error) {
	if err := s.requirePool(); err != nil {
		return BidRef{}, err
	}
	var b BidRef
	var name, key string
	err := s.Pool.QueryRow(ctx, `SELECT id::bigint, COALESCE(participant_name,''), COALESCE(participant_normalized,''), tender_id::bigint FROM tender_participants WHERE id=$1`, id).Scan(&b.ID, &name, &key, &b.TenderID)
	if err != nil {
		return b, fmt.Errorf("load bid %d: %w", id, err)
	}
	b.Name, b.ObservedKey = name, key
	if b.ObservedKey == "" {
		b.ObservedKey = fmt.Sprintf("bid:%d", b.ID)
	}
	b.GID = projection.GID("BID", "v", fmt.Sprint(b.ID))
	b.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(b.TenderID))
	return b, nil
}

func observedWhere(alias string, b BidRef) (string, []any) {
	if b.ObservedKey == fmt.Sprintf("bid:%d", b.ID) {
		return alias + ".id=$1", []any{b.ID}
	}
	return alias + ".participant_normalized=$1 AND COALESCE(" + alias + ".participant_normalized,'')<>''", []any{b.ObservedKey}
}

func (s Service) Profile(ctx context.Context, bidID int64, limit int) (Profile, error) {
	return s.ProfileWindow(ctx, bidID, limit, TimeWindow{})
}

func addWindow(where, expression string, next int, window TimeWindow) (string, []any) {
	parts := []string{where}
	args := []any{}
	if window.From != nil {
		parts = append(parts, fmt.Sprintf("%s >= $%d", expression, next))
		args = append(args, *window.From)
		next++
	}
	if window.To != nil {
		parts = append(parts, fmt.Sprintf("%s < $%d", expression, next))
		args = append(args, *window.To)
	}
	return strings.Join(parts, " AND "), args
}

func (s Service) ProfileWindow(ctx context.Context, bidID int64, limit int, window TimeWindow) (Profile, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	b, err := s.loadBid(ctx, bidID)
	if err != nil {
		return Profile{}, err
	}
	where, args := observedWhere("p", b)
	windowArgs := []any{}
	where, windowArgs = addWindow(where, "COALESCE(p.submitted_date,t.created_at)", len(args)+1, window)
	args = append(args, windowArgs...)
	p := Profile{Bid: b, IdentityNote: "Grouped only by the stored participant_normalized value; this is an observed key, not a confirmed legal identity.", CoBidders: []CoBidder{}, History: []Participation{}, Calculation: "distinct_tenders=count(DISTINCT tender_participants.tender_id); co-bid counts=count(DISTINCT shared tender_id)"}
	var first, last *time.Time
	err = s.Pool.QueryRow(ctx, `SELECT COUNT(*), COUNT(DISTINCT p.tender_id), COUNT(DISTINCT t.organization), MIN(COALESCE(p.submitted_date,t.created_at)), MAX(COALESCE(p.submitted_date,t.created_at)) FROM tender_participants p JOIN tenders t ON t.id=p.tender_id WHERE `+where, args...).Scan(&p.BidRecords, &p.DistinctTenders, &p.DistinctOrganizations, &first, &last)
	if err != nil {
		return p, fmt.Errorf("profile summary: %w", err)
	}
	p.FirstObserved, p.LastObserved = first, last
	historyQ := fmt.Sprintf(`SELECT p.id::bigint,p.tender_id::bigint,t.tender_id,COALESCE(t.organization,''),p.submitted_date,COALESCE(p.financial_rank,''),COALESCE(p.financial_bid_value,''),COALESCE(p.technical_status,''),COALESCE(p.financial_status,''),COALESCE(p.is_winner,false) FROM tender_participants p JOIN tenders t ON t.id=p.tender_id WHERE %s ORDER BY COALESCE(p.submitted_date,t.created_at) DESC,p.id DESC LIMIT $%d`, where, len(args)+1)
	historyArgs := append(append([]any{}, args...), limit)
	rows, err := s.Pool.Query(ctx, historyQ, historyArgs...)
	if err != nil {
		return p, fmt.Errorf("profile history: %w", err)
	}
	for rows.Next() {
		var h Participation
		var ext string
		if err := rows.Scan(&h.BidID, &h.TenderID, &ext, &h.Organization, &h.SubmittedAt, &h.FinancialRank, &h.FinancialValue, &h.TechnicalStatus, &h.FinancialStatus, &h.IsWinner); err != nil {
			rows.Close()
			return p, err
		}
		h.ExternalTender = ext
		h.BidGID = projection.GID("BID", "v", fmt.Sprint(h.BidID))
		h.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(h.TenderID))
		h.Evidence = Evidence{"tender_participants", fmt.Sprint(h.BidID), h.BidGID}
		p.History = append(p.History, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return p, err
	}
	selectedWhere, selectedArgs := observedWhere("p", b)
	selectedWhere, selectedWindowArgs := addWindow(selectedWhere, "COALESCE(p.submitted_date,t0.created_at)", len(selectedArgs)+1, window)
	selectedArgs = append(selectedArgs, selectedWindowArgs...)
	coKey := b.ObservedKey
	if b.ObservedKey == fmt.Sprintf("bid:%d", b.ID) {
		coKey = ""
	}
	coKeyParam := len(selectedArgs) + 1
	limitParam := coKeyParam + 1
	coQ := fmt.Sprintf(`WITH selected AS (SELECT p.tender_id FROM tender_participants p JOIN tenders t0 ON t0.id=p.tender_id WHERE %s) SELECT p2.participant_normalized,MAX(p2.participant_name),COUNT(DISTINCT p2.tender_id),MIN(COALESCE(p2.submitted_date,t.created_at)),MAX(COALESCE(p2.submitted_date,t.created_at)) FROM tender_participants p2 JOIN tenders t ON t.id=p2.tender_id JOIN selected s ON s.tender_id=p2.tender_id WHERE COALESCE(p2.participant_normalized,'')<>'' AND p2.participant_normalized<>$%d GROUP BY p2.participant_normalized ORDER BY COUNT(DISTINCT p2.tender_id) DESC,p2.participant_normalized LIMIT $%d`, selectedWhere, coKeyParam, limitParam)
	coArgs := append(append([]any{}, selectedArgs...), coKey, limit)
	totalCoQ := fmt.Sprintf(`WITH selected AS (SELECT p.tender_id FROM tender_participants p JOIN tenders t0 ON t0.id=p.tender_id WHERE %s) SELECT COUNT(DISTINCT p2.tender_id) FROM tender_participants p2 JOIN selected s ON s.tender_id=p2.tender_id WHERE COALESCE(p2.participant_normalized,'')<>'' AND p2.participant_normalized<>$%d`, selectedWhere, coKeyParam)
	if err := s.Pool.QueryRow(ctx, totalCoQ, append(append([]any{}, selectedArgs...), coKey)...).Scan(&p.CoBidTenderCount); err != nil {
		return p, fmt.Errorf("profile co-bid total: %w", err)
	}
	rows, err = s.Pool.Query(ctx, coQ, coArgs...)
	if err != nil {
		return p, fmt.Errorf("profile co-bidders: %w", err)
	}
	for rows.Next() {
		var c CoBidder
		if err := rows.Scan(&c.ObservedKey, &c.ExampleName, &c.SharedTenders, &c.FirstObserved, &c.LastObserved); err != nil {
			rows.Close()
			return p, err
		}
		p.CoBidders = append(p.CoBidders, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return p, err
	}
	return p, nil
}

func (s Service) Pair(ctx context.Context, leftID, rightID int64, limit int) (PairAnalysis, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	left, err := s.loadBid(ctx, leftID)
	if err != nil {
		return PairAnalysis{}, err
	}
	right, err := s.loadBid(ctx, rightID)
	if err != nil {
		return PairAnalysis{}, err
	}
	if left.ObservedKey == right.ObservedKey {
		return PairAnalysis{}, fmt.Errorf("selected bids resolve to the same observed participant key %q", left.ObservedKey)
	}
	if syntheticObservedKey(left.ObservedKey) || syntheticObservedKey(right.ObservedKey) {
		return PairAnalysis{}, fmt.Errorf("pair analysis requires non-empty participant_normalized values; ambiguous participant identities are not grouped")
	}
	a, b := left.ObservedKey, right.ObservedKey
	if a > b {
		a, b = b, a
	}
	pa := PairAnalysis{LeftObservedKey: a, RightObservedKey: b, SharedTenders: []SharedTender{}, Calculation: "Jaccard=shared_tenders/(left_tenders+right_tenders-shared_tenders); coverage=shared_tenders/side_tenders"}
	err = s.Pool.QueryRow(ctx, `SELECT COUNT(DISTINCT tender_id) FILTER (WHERE participant_normalized=$1),COUNT(DISTINCT tender_id) FILTER (WHERE participant_normalized=$2) FROM tender_participants WHERE participant_normalized IN ($1,$2)`, a, b).Scan(&pa.LeftTenderCount, &pa.RightTenderCount)
	if err != nil {
		return pa, fmt.Errorf("pair totals: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id::bigint,t.tender_id,COALESCE(t.organization,''),MAX(COALESCE(p.submitted_date,t.created_at)),MIN(p.id) FILTER (WHERE p.participant_normalized=$1),MIN(p.id) FILTER (WHERE p.participant_normalized=$2) FROM tenders t JOIN tender_participants p ON p.tender_id=t.id WHERE p.participant_normalized IN ($1,$2) GROUP BY t.id,t.tender_id,t.organization HAVING COUNT(DISTINCT p.participant_normalized)=2 ORDER BY MAX(COALESCE(p.submitted_date,t.created_at)),t.id LIMIT $3`, a, b, limit)
	if err != nil {
		return pa, fmt.Errorf("pair history: %w", err)
	}
	for rows.Next() {
		var st SharedTender
		var ext string
		var leftBid, rightBid int64
		if err := rows.Scan(&st.TenderID, &ext, &st.Organization, &st.ObservedAt, &leftBid, &rightBid); err != nil {
			rows.Close()
			return pa, err
		}
		st.ExternalTender = ext
		st.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(st.TenderID))
		st.Evidence = []Evidence{{"tender_participants", fmt.Sprint(leftBid), projection.GID("BID", "v", fmt.Sprint(leftBid))}, {"tender_participants", fmt.Sprint(rightBid), projection.GID("BID", "v", fmt.Sprint(rightBid))}, {"tenders", fmt.Sprint(st.TenderID), st.TenderGID}}
		pa.SharedTenders = append(pa.SharedTenders, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return pa, err
	}
	var shared int
	err = s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT tender_id FROM tender_participants WHERE participant_normalized=$1 INTERSECT SELECT DISTINCT tender_id FROM tender_participants WHERE participant_normalized=$2) x`, a, b).Scan(&shared)
	if err != nil {
		return pa, err
	}
	pa.SharedTenderCount = shared
	pa.JaccardSimilarity = Jaccard(shared, pa.LeftTenderCount, pa.RightTenderCount)
	pa.LeftCoverage = Coverage(shared, pa.LeftTenderCount)
	pa.RightCoverage = Coverage(shared, pa.RightTenderCount)
	return pa, nil
}

func (s Service) Rankings(ctx context.Context, by string, limit int) ([]RankingRow, error) {
	if err := s.requirePool(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	var q string
	switch by {
	case "participation":
		q = `SELECT p.participant_normalized,MAX(p.participant_name),COUNT(*),COUNT(DISTINCT p.tender_id),COUNT(DISTINCT t.organization),0 FROM tender_participants p JOIN tenders t ON t.id=p.tender_id WHERE COALESCE(p.participant_normalized,'')<>'' GROUP BY p.participant_normalized ORDER BY COUNT(DISTINCT p.tender_id) DESC,p.participant_normalized LIMIT $1`
	case "organizations":
		q = `SELECT p.participant_normalized,MAX(p.participant_name),COUNT(*),COUNT(DISTINCT p.tender_id),COUNT(DISTINCT t.organization),0 FROM tender_participants p JOIN tenders t ON t.id=p.tender_id WHERE COALESCE(p.participant_normalized,'')<>'' GROUP BY p.participant_normalized ORDER BY COUNT(DISTINCT t.organization) DESC,p.participant_normalized LIMIT $1`
	case "co-bidding":
		q = `WITH bidders AS (SELECT DISTINCT tender_id,participant_normalized FROM tender_participants WHERE COALESCE(participant_normalized,'')<>''), pairs AS (SELECT a.participant_normalized a,b.participant_normalized b FROM bidders a JOIN bidders b ON b.tender_id=a.tender_id AND a.participant_normalized<b.participant_normalized GROUP BY a.participant_normalized,b.participant_normalized), ranked AS (SELECT a AS observed_key,COUNT(*) AS distinct_cobidders FROM pairs GROUP BY a UNION ALL SELECT b AS observed_key,COUNT(*) AS distinct_cobidders FROM pairs GROUP BY b), totals AS (SELECT observed_key,SUM(distinct_cobidders)::bigint AS distinct_cobidders FROM ranked GROUP BY observed_key) SELECT p.participant_normalized,MAX(p.participant_name),COUNT(*),COUNT(DISTINCT p.tender_id),COUNT(DISTINCT t.organization),COALESCE(totals.distinct_cobidders,0) FROM tender_participants p JOIN tenders t ON t.id=p.tender_id LEFT JOIN totals ON totals.observed_key=p.participant_normalized WHERE COALESCE(p.participant_normalized,'')<>'' GROUP BY p.participant_normalized,totals.distinct_cobidders ORDER BY COALESCE(totals.distinct_cobidders,0) DESC,p.participant_normalized LIMIT $1`
	default:
		return nil, fmt.Errorf("unknown ranking %q (use participation, organizations, or co-bidding)", by)
	}
	rows, err := s.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RankingRow
	for rows.Next() {
		var r RankingRow
		if err := rows.Scan(&r.ObservedKey, &r.ExampleName, &r.BidRecords, &r.DistinctTenders, &r.DistinctOrganizations, &r.DistinctCoBidders); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s Service) Tender(ctx context.Context, tenderID int64, limit int) (TenderAnalysis, error) {
	if err := s.requirePool(); err != nil {
		return TenderAnalysis{}, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	var out TenderAnalysis
	var ext string
	if err := s.Pool.QueryRow(ctx, `SELECT id::bigint,tender_id,COALESCE(organization,'') FROM tenders WHERE id=$1`, tenderID).Scan(&out.TenderID, &ext, &out.Organization); err != nil {
		return out, err
	}
	out.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(tenderID))
	out.ExternalTender = ext
	rows, err := s.Pool.Query(ctx, `SELECT id::bigint,COALESCE(participant_name,''),COALESCE(participant_normalized,''),COALESCE(financial_rank,''),COALESCE(financial_bid_value,''),COALESCE(technical_status,''),COALESCE(financial_status,''),COALESCE(is_winner,false) FROM tender_participants WHERE tender_id=$1 ORDER BY id LIMIT $2`, tenderID, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p TenderParticipant
		if err := rows.Scan(&p.BidID, &p.Name, &p.ObservedKey, &p.FinancialRank, &p.FinancialValue, &p.TechnicalStatus, &p.FinancialStatus, &p.IsWinner); err != nil {
			rows.Close()
			return out, err
		}
		p.BidGID = projection.GID("BID", "v", fmt.Sprint(p.BidID))
		p.Evidence = Evidence{"tender_participants", fmt.Sprint(p.BidID), p.BidGID}
		out.Participants = append(out.Participants, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	var c int
	if err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT p1.id FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id=p1.tender_id AND p2.id>p1.id WHERE p1.tender_id=$1) x`, tenderID).Scan(&c); err != nil {
		return out, err
	}
	out.CoBidObservations = c
	pairRows, err := s.Pool.Query(ctx, `SELECT p1.id::bigint,p2.id::bigint FROM tender_participants p1 JOIN tender_participants p2 ON p2.tender_id=p1.tender_id AND p2.id>p1.id WHERE p1.tender_id=$1 AND COALESCE(p1.participant_normalized,'')<>'' AND COALESCE(p2.participant_normalized,'')<>'' ORDER BY p1.id,p2.id LIMIT $2`, tenderID, limit*limit)
	if err != nil {
		return out, err
	}
	var pairIDs [][2]int64
	for pairRows.Next() {
		var a, b int64
		if err := pairRows.Scan(&a, &b); err != nil {
			pairRows.Close()
			return out, err
		}
		pairIDs = append(pairIDs, [2]int64{a, b})
	}
	pairRows.Close()
	if err := pairRows.Err(); err != nil {
		return out, err
	}
	seenPairs := map[string]bool{}
	for _, ids := range pairIDs {
		pair, err := s.Pair(ctx, ids[0], ids[1], limit)
		if err != nil {
			continue
		}
		key := pair.LeftObservedKey + "\x00" + pair.RightObservedKey
		if !seenPairs[key] {
			seenPairs[key] = true
			out.HistoricalPairs = append(out.HistoricalPairs, pair)
		}
	}
	return out, nil
}

func (s Service) Organization(ctx context.Context, name string, limit int) (map[string]any, error) {
	if err := s.requirePool(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tenders WHERE LOWER(organization)=LOWER($1)`, name).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id::bigint,t.tender_id,COALESCE(t.organization,''),COUNT(DISTINCT NULLIF(p.participant_normalized,'')) FROM tenders t LEFT JOIN tender_participants p ON p.tender_id=t.id WHERE LOWER(t.organization)=LOWER($1) GROUP BY t.id,t.tender_id,t.organization ORDER BY t.id LIMIT $2`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		TenderID        int64  `json:"tender_id"`
		TenderGID       string `json:"tender_gid"`
		ExternalTender  string `json:"external_tender_id"`
		Organization    string `json:"organization"`
		DistinctBidders int    `json:"distinct_bidders"`
	}
	var tenders []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.TenderID, &r.ExternalTender, &r.Organization, &r.DistinctBidders); err != nil {
			return nil, err
		}
		r.TenderGID = projection.GID("TENDER", "v", fmt.Sprint(r.TenderID))
		tenders = append(tenders, r)
	}
	return map[string]any{"organization": name, "tender_count": total, "returned_tenders": len(tenders), "tenders": tenders, "calculation": "distinct_bidders=count(DISTINCT tender_participants.participant_normalized); source=tenders + tender_participants"}, rows.Err()
}

func (s Service) directCoBidders(ctx context.Context, bidID int64, limit int) ([]int64, error) {
	if s.Graph == nil {
		return nil, fmt.Errorf("intel: HydraDB client is required for network exploration")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	q := `MATCH (start:Bid)-[:CO_BID_IN]->(o:CoBidObservation)<-[:CO_BID_IN]-(other:Bid) WHERE start.src_pk = $bid_pk AND other.src_pk <> $bid_pk RETURN DISTINCT other.src_pk AS bid_pk ORDER BY bid_pk LIMIT $limit`
	res, err := s.Graph.DoQuery(ctx, q, map[string]any{"bid_pk": fmt.Sprint(bidID), "limit": limit})
	if err != nil {
		return nil, fmt.Errorf("direct co-bidders: %w", err)
	}
	var out []int64
	for i := range res.Rows {
		pk, e := res.String(i, 0)
		if e != nil {
			return nil, e
		}
		id, e := strconv.ParseInt(pk, 10, 64)
		if e != nil {
			return nil, fmt.Errorf("decode co-bid source pk %q: %w", pk, e)
		}
		out = append(out, id)
	}
	return out, nil
}

// CoBidders is the interactive graph-backed lookup. The returned values are
// Bid records, not companies; repeated observed keys are intentionally kept.
func (s Service) CoBidders(ctx context.Context, bidID int64, limit int) ([]NetworkNode, error) {
	ids, err := s.directCoBidders(ctx, bidID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]NetworkNode, 0, len(ids))
	for _, id := range ids {
		out = append(out, NetworkNode{BidID: id, BidGID: projection.GID("BID", "v", fmt.Sprint(id)), Depth: 1})
	}
	if len(ids) == 0 || s.Pool == nil {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::bigint,COALESCE(participant_name,''),COALESCE(participant_normalized,'') FROM tender_participants WHERE id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]int, len(out))
	for i := range out {
		byID[out[i].BidID] = i
	}
	for rows.Next() {
		var id int64
		var name, key string
		if err := rows.Scan(&id, &name, &key); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Name, out[i].ObservedKey = name, key
		}
	}
	return out, rows.Err()
}

func (s Service) Network(ctx context.Context, bidID int64, depth, limit int) (Network, error) {
	if depth < 1 || depth > 3 {
		return Network{}, fmt.Errorf("depth must be between 1 and 3")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	out := Network{RootBidID: bidID, Depth: depth, Limit: limit, Nodes: []NetworkNode{}, Method: "bounded breadth-first HydraDB CO_BID_IN traversal; no unrestricted path expansion"}
	seen := map[int64]bool{bidID: true}
	frontier := []int64{bidID}
	for d := 1; d <= depth && len(out.Nodes) < limit; d++ {
		var next []int64
		for _, id := range frontier {
			ids, err := s.directCoBidders(ctx, id, limit-len(out.Nodes))
			if err != nil {
				return out, err
			}
			for _, nid := range ids {
				if seen[nid] {
					continue
				}
				seen[nid] = true
				next = append(next, nid)
				out.Nodes = append(out.Nodes, NetworkNode{BidID: nid, BidGID: projection.GID("BID", "v", fmt.Sprint(nid)), Depth: d})
				if len(out.Nodes) >= limit {
					break
				}
			}
			if len(out.Nodes) >= limit {
				break
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}
	if len(out.Nodes) > 0 {
		ids := make([]int64, len(out.Nodes))
		for i := range out.Nodes {
			ids[i] = out.Nodes[i].BidID
		}
		rows, err := s.Pool.Query(ctx, `SELECT id::bigint,COALESCE(participant_name,''),COALESCE(participant_normalized,'') FROM tender_participants WHERE id=ANY($1::bigint[])`, ids)
		if err == nil {
			for rows.Next() {
				var id int64
				var name, key string
				if rows.Scan(&id, &name, &key) == nil {
					for i := range out.Nodes {
						if out.Nodes[i].BidID == id {
							out.Nodes[i].Name = name
							out.Nodes[i].ObservedKey = key
						}
					}
				}
			}
			rows.Close()
		}
	}
	return out, nil
}

// StableKeys is useful to callers that need deterministic result ordering.
func StableKeys(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
