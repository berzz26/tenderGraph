package graph

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the HydraDB connection and graph configuration. Secrets are
// never logged: String() redacts the auth token (see log.go).
//
// Defaults match the verified running development instance
// (graph-node 0.1.0+916637477f3f, admin port 19090).
type Config struct {
	// BoltAddr is host:port for the Bolt endpoint (Neo4j driver as Bolt client).
	BoltAddr string
	// HTTPAddr is the base URL of the query API (e.g. http://127.0.0.1:8443).
	HTTPAddr string
	// AdminAddr is the base URL of the admin endpoints (/readyz, /metrics).
	AdminAddr string
	// AuthToken is the bearer token; never logged.
	AuthToken string
	// Database is the graph id (HydraDB "database" in connection terms).
	Database string
	// Namespace is the graph namespace.
	Namespace string
	// CellID is mandatory on every HTTP query request.
	CellID string

	// Timeouts (seconds, env-overridable).
	ConnectTimeoutSec int // driver connect / auth / handshake
	QueryTimeoutSec   int // per query / request
	HealthTimeoutSec  int // health probe requests
}

// LoadGraphConfig resolves HYDRADB_* environment variables (a .env file is
// loaded by cfg.Load() in the existing app; this loader is standalone so the
// graph package stays independent).
func LoadGraphConfig() Config {
	c := Config{
		BoltAddr:          envOr("HYDRADB_BOLT_ADDR", "127.0.0.1:7687"),
		HTTPAddr:          envOr("HYDRADB_HTTP_ADDR", "http://127.0.0.1:8443"),
		AdminAddr:         envOr("HYDRADB_ADMIN_ADDR", "http://127.0.0.1:19090"),
		Database:          envOr("HYDRADB_DATABASE", "default"),
		Namespace:         envOr("HYDRADB_NAMESPACE", "default"),
		CellID:            envOr("HYDRADB_CELL_ID", "cell-0"),
		ConnectTimeoutSec: envIntOr("HYDRADB_CONNECT_TIMEOUT_SEC", 10),
		QueryTimeoutSec:   envIntOr("HYDRADB_QUERY_TIMEOUT_SEC", 30),
		HealthTimeoutSec:  envIntOr("HYDRADB_HEALTH_TIMEOUT_SEC", 5),
	}
	c.AuthToken = envOr("HYDRADB_AUTH_TOKEN", "")
	if c.AuthToken == "" {
		// Token-file variant keeps the secret out of the environment listing.
		if f := envOr("HYDRADB_AUTH_TOKEN_FILE", ""); f != "" {
			if b, err := os.ReadFile(strings.TrimSpace(f)); err == nil {
				c.AuthToken = strings.TrimSpace(string(b))
			}
		}
	}
	return c
}

// Validate reports configuration that cannot possibly work.
func (c Config) Validate() error {
	if c.BoltAddr == "" {
		return fmt.Errorf("HYDRADB_BOLT_ADDR is required")
	}
	if c.HTTPAddr == "" {
		return fmt.Errorf("HYDRADB_HTTP_ADDR is required")
	}
	if c.AdminAddr == "" {
		return fmt.Errorf("HYDRADB_ADMIN_ADDR is required")
	}
	if c.AuthToken == "" {
		return fmt.Errorf("HYDRADB_AUTH_TOKEN (or HYDRADB_AUTH_TOKEN_FILE) is required")
	}
	if c.CellID == "" {
		return fmt.Errorf("HYDRADB_CELL_ID is required (HydraDB HTTP API mandates it)")
	}
	if c.Database == "" {
		return fmt.Errorf("HYDRADB_DATABASE is required")
	}
	return nil
}

// String renders the config with the token redacted. Safe for logging.
func (c Config) String() string {
	return fmt.Sprintf(
		"bolt=%s http=%s admin=%s database=%s namespace=%s cell=%s auth=%s",
		c.BoltAddr, c.HTTPAddr, c.AdminAddr, c.Database, c.Namespace, c.CellID,
		redact(c.AuthToken),
	)
}

// boltSchemeless returns the bolt address without any scheme prefix.
func (c Config) boltSchemeless() string {
	a := c.BoltAddr
	for _, p := range []string{"bolt://", "neo4j://", "neo4j+s://", "bolt+s://"} {
		if strings.HasPrefix(a, p) {
			return strings.TrimPrefix(a, p)
		}
	}
	return a
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envIntOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
