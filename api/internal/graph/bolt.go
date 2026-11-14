package graph

import (
	"context"
	"fmt"
	"strings"
	"time"

	neo4j "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// BoltClient is the Bolt-protocol client for HydraDB, built on the official
// Neo4j Go driver (used strictly as a Bolt client per the fixed architectural
// decisions). Raw driver objects do not leave this package.
//
// Verified constraints (live HydraDB 0.1.0+916637477f3f):
//   - Reads must be MATCH...RETURN (bare RETURN is rejected by the row engine).
//   - Writes are only accepted in the UNWIND batch mutation form.
//   - Auth uses the Bolt "bearer" scheme with the same token as HTTP
//     (a basic scheme with an empty principal is rejected — verified live).
type BoltClient struct {
	driver neo4j.DriverWithContext
	dbName string // graph id used as the Bolt "database"
	token  string
}

// NewBoltClient initializes the driver (no network I/O yet) using the
// neo4j:// routing scheme against the configured bolt address. Call
// VerifyConnectivity to establish actual connections.
func NewBoltClient(cfg Config) (*BoltClient, error) {
	if cfg.AuthToken == "" {
		return nil, fmt.Errorf("bolt: auth token is required")
	}
	uri := "neo4j://" + cfg.boltSchemeless()
	auth := neo4j.BearerAuth(cfg.AuthToken) // verified Bolt scheme "bearer": the same token as HTTP (crates/bolt-server/src/session.rs Credentials::from_logon)
	driver, err := neo4j.NewDriverWithContext(
		uri,
		auth,
		func(c *neo4j.Config) {
			c.ConnectionAcquisitionTimeout = time.Duration(cfg.ConnectTimeoutSec) * time.Second
			c.SocketConnectTimeout = time.Duration(cfg.ConnectTimeoutSec) * time.Second
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bolt: create driver %s: %w", redactURL(uri), err)
	}
	return &BoltClient{driver: driver, dbName: cfg.Database, token: cfg.AuthToken}, nil
}

// VerifyConnectivity performs the actual Bolt handshake and auth check.
func (b *BoltClient) VerifyConnectivity(ctx context.Context) error {
	if err := b.driver.VerifyConnectivity(ctx); err != nil {
		return fmt.Errorf("bolt: verify connectivity (database=%s): %w", b.dbName, err)
	}
	return nil
}

// Query executes a parameterized Cypher query with an explicit access mode.
// queryMode is neo4j.AccessModeRead or neo4j.AccessModeWrite.
//
// The result carries decoded scalar values (int64, float64, string, bool,
// []any, map[string]any). Node/relationship records are flattened to their
// element ids and properties to keep Phase 1B decoding predictable.
func (b *BoltClient) Query(ctx context.Context, cypher string, params map[string]any, mode neo4j.AccessMode) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	session := b.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: b.dbName,
		AccessMode:   mode,
	})
	defer session.Close(ctx)

	result, err := session.Run(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("bolt: run query (db=%s, mode=%v): %w", b.dbName, mode, err)
	}

	out := &Result{}
	rec, err := result.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("bolt: collect results (db=%s, mode=%v): %w", b.dbName, mode, err)
	}
	if len(rec) > 0 {
		out.Columns = rec[0].Keys
	}
	for _, r := range rec {
		row := make([]any, 0, len(r.Values))
		for _, v := range r.Values {
			row = append(row, decodeBoltValue(v))
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// ReadQuery runs a read (MATCH ... RETURN) query.
func (b *BoltClient) ReadQuery(ctx context.Context, cypher string, params map[string]any) (*Result, error) {
	return b.Query(ctx, cypher, params, neo4j.AccessModeRead)
}

// WriteQuery runs a write query. In the current HydraDB build, writes must be
// UNWIND-batch shaped (verified); this method only transports the query.
func (b *BoltClient) WriteQuery(ctx context.Context, cypher string, params map[string]any) (*Result, error) {
	return b.Query(ctx, cypher, params, neo4j.AccessModeWrite)
}

// Close releases driver resources.
func (b *BoltClient) Close(ctx context.Context) error {
	if err := b.driver.Close(ctx); err != nil {
		return fmt.Errorf("bolt: close driver: %w", err)
	}
	return nil
}

// decodeBoltValue converts driver values to plain Go values. Structured types
// are flattened (element id + properties) so callers never see driver types.
func decodeBoltValue(v any) any {
	switch x := v.(type) {
	case nil, bool, string, int64, float64, []byte, time.Time:
		return x
	case int, int8, int16, int32, uint8, uint16, uint32:
		return fmt.Sprintf("%d", x)
	case neo4j.Node:
		return map[string]any{
			"element_id": x.ElementId,
			"labels":     x.Labels,
			"props":      decodeProps(x.Props),
		}
	case neo4j.Relationship:
		return map[string]any{
			"element_id": x.ElementId,
			"type":       x.Type,
			"start_id":   x.StartElementId,
			"end_id":     x.EndElementId,
			"props":      decodeProps(x.Props),
		}
	case neo4j.Path:
		return map[string]any{"nodes": len(x.Nodes), "relationships": len(x.Relationships)}
	case neo4j.Date, neo4j.Time, neo4j.LocalDateTime, neo4j.LocalTime, neo4j.Duration:
		return fmt.Sprintf("%v", x)
	case neo4j.Point2D, neo4j.Point3D:
		return fmt.Sprintf("%v", x)
	default:
		if rv, ok := any(x).([]any); ok {
			out := make([]any, 0, len(rv))
			for _, e := range rv {
				out = append(out, decodeBoltValue(e))
			}
			return out
		}
		// Last resort: string representation, never a raw driver type.
		return fmt.Sprintf("%v", x)
	}
}

func decodeProps(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[strings.Clone(k)] = decodeBoltValue(v)
	}
	return out
}
