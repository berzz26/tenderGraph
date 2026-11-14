package intel

import (
	"context"
	"fmt"

	"vyuha/api/internal/projection"
)

// DegreeRow is a native HydraDB degree result. Degree counts graph
// relationships, not distinct participant names or distinct source tenders.
type DegreeRow struct {
	BidID              int64  `json:"bid_id"`
	BidGID             string `json:"bid_gid"`
	ObservedKey        string `json:"observed_participant_key,omitempty"`
	RelationshipDegree int64  `json:"relationship_degree"`
}

type DegreeResult struct {
	Method        string      `json:"method"`
	MetricMeaning string      `json:"metric_meaning"`
	Limit         int         `json:"limit"`
	Rows          []DegreeRow `json:"rows"`
}

type NativePathResult struct {
	Method        string `json:"method"`
	MetricMeaning string `json:"metric_meaning"`
	SourceBidID   int64  `json:"source_bid_id"`
	TargetBidID   *int64 `json:"target_bid_id,omitempty"`
	MaxDepth      int    `json:"max_depth"`
	ResultLimit   int    `json:"result_limit"`
	Paths         []any  `json:"paths"`
	Complete      bool   `json:"complete"`
}

func (s Service) NativeDegree(ctx context.Context, limit int) (DegreeResult, error) {
	if s.Graph == nil {
		return DegreeResult{}, fmt.Errorf("HydraDB graph client is required")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	q := `CALL algo.degree({label: 'Bid', relTypes: ['CO_BID_IN'], relDirection: 'undirected', minDegree: 1, limit: $limit}) YIELD node, degree RETURN node.id AS id, node.gid AS gid, node.participant_normalized AS participant, degree`
	res, err := s.Graph.DoQuery(ctx, q, map[string]any{"limit": limit})
	if err != nil {
		return DegreeResult{}, err
	}
	out := DegreeResult{
		Method:        "HydraDB algo.degree",
		MetricMeaning: "Undirected CO_BID_IN relationship degree of Bid nodes. Each Bid-to-CoBidObservation relationship counts once; this is not a distinct legal bidder or distinct co-bidder count.",
		Limit:         limit,
		Rows:          make([]DegreeRow, 0, len(res.Rows)),
	}
	for i := range res.Rows {
		id, err := res.Int64(i, 0)
		if err != nil {
			return DegreeResult{}, err
		}
		gid, err := res.String(i, 1)
		if err != nil {
			return DegreeResult{}, err
		}
		participant, err := res.String(i, 2)
		if err != nil {
			return DegreeResult{}, err
		}
		degree, err := res.Int64(i, 3)
		if err != nil {
			return DegreeResult{}, err
		}
		if id != projection.NumericID(gid) {
			return DegreeResult{}, fmt.Errorf("degree row identity mismatch for %s: graph id %d", gid, id)
		}
		out.Rows = append(out.Rows, DegreeRow{BidID: sourceIDFromGID(gid), BidGID: gid, ObservedKey: participant, RelationshipDegree: degree})
	}
	return out, nil
}

func (s Service) NativeShortestPaths(ctx context.Context, leftID, rightID int64, maxDepth, limit int) (NativePathResult, error) {
	if s.Graph == nil {
		return NativePathResult{}, fmt.Errorf("HydraDB graph client is required")
	}
	if maxDepth < 1 || maxDepth > 4 {
		return NativePathResult{}, fmt.Errorf("path depth must be between 1 and 4")
	}
	if limit <= 0 {
		limit = 1
	}
	left := projection.NumericID(projection.GID("BID", "v", fmt.Sprint(leftID)))
	right := projection.NumericID(projection.GID("BID", "v", fmt.Sprint(rightID)))
	q := `CALL algo.SPpaths({sourceNode: $source, targetNode: $target, relTypes: ['CO_BID_IN'], relDirection: 'both', maxLen: $depth, pathCount: $paths}) YIELD path RETURN path`
	res, err := s.Graph.DoQuery(ctx, q, map[string]any{"source": left, "target": right, "depth": maxDepth, "paths": limit})
	if err != nil {
		return NativePathResult{}, err
	}
	paths := make([]any, 0, len(res.Rows))
	for i := range res.Rows {
		paths = append(paths, res.Value(i, 0))
	}
	return NativePathResult{
		Method:        "HydraDB algo.SPpaths",
		MetricMeaning: "Bounded simple paths between two Bid records through CO_BID_IN observations; both directions are required because graph edges point Bid -> CoBidObservation.",
		SourceBidID:   leftID,
		TargetBidID:   &rightID,
		MaxDepth:      maxDepth,
		ResultLimit:   limit,
		Paths:         paths,
		Complete:      len(paths) < limit,
	}, nil
}

func (s Service) NativeConnections(ctx context.Context, bidID int64, maxDepth, limit int) (NativePathResult, error) {
	if s.Graph == nil {
		return NativePathResult{}, fmt.Errorf("HydraDB graph client is required")
	}
	if maxDepth < 1 || maxDepth > 4 {
		return NativePathResult{}, fmt.Errorf("path depth must be between 1 and 4")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	source := projection.NumericID(projection.GID("BID", "v", fmt.Sprint(bidID)))
	q := `CALL algo.SSpaths({sourceNode: $source, relTypes: ['CO_BID_IN'], relDirection: 'both', maxLen: $depth, pathCount: 1, resultLimit: $limit}) YIELD path RETURN path`
	res, err := s.Graph.DoQuery(ctx, q, map[string]any{"source": source, "depth": maxDepth, "limit": limit})
	if err != nil {
		return NativePathResult{}, err
	}
	paths := make([]any, 0, len(res.Rows))
	for i := range res.Rows {
		paths = append(paths, res.Value(i, 0))
	}
	return NativePathResult{
		Method:        "HydraDB algo.SSpaths",
		MetricMeaning: "Bounded simple paths from one Bid record through CO_BID_IN; resultLimit caps returned paths and does not prove graph-wide completeness.",
		SourceBidID:   bidID,
		MaxDepth:      maxDepth,
		ResultLimit:   limit,
		Paths:         paths,
		Complete:      len(paths) < limit,
	}, nil
}

func sourceIDFromGID(gid string) int64 {
	const prefix = "BID:v:"
	if len(gid) > len(prefix) && gid[:len(prefix)] == prefix {
		var id int64
		for _, r := range gid[len(prefix):] {
			if r < '0' || r > '9' {
				return 0
			}
			id = id*10 + int64(r-'0')
		}
		return id
	}
	return 0
}
