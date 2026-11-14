package cfg

import (
	"os"
	"strings"
)

// Config resolves environment configuration; a minimal .env file is also read
// (KEY=VALUE lines, '#' comments). Secrets are never logged.
type Config struct {
	DatabaseURL string
	ServerAddr  string
}

func Load() Config {
	loadDotEnv(".env")
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		ServerAddr:  envOr("PORT", "8080"),
	}
	if c.ServerAddr[0] != ':' {
		c.ServerAddr = ":" + c.ServerAddr
	}
	return c
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}
