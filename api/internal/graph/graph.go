package graph

import "context"

// Clients bundles the configured HydraDB integration surfaces.
type Clients struct {
	Bolt   *BoltClient
	HTTP   *HTTPClient
	Health *HealthChecker
}

// Open validates the configuration and initializes Bolt + HTTP clients and
// the health checker. Bolt connectivity is NOT verified here — call
// Clients.Bolt.VerifyConnectivity (or Clients.Health.Check) when ready, so
// that an unavailable HydraDB never blocks application startup.
func Open(cfg Config) (*Clients, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	bc, err := NewBoltClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Clients{
		Bolt:   bc,
		HTTP:   NewHTTPClient(cfg),
		Health: NewHealthChecker(cfg, bc),
	}, nil
}

// Close releases the Bolt driver.
func (c *Clients) Close(ctx context.Context) error {
	if c == nil || c.Bolt == nil {
		return nil
	}
	return c.Bolt.Close(ctx)
}
