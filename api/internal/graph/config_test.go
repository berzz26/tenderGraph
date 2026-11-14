package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGraphConfigDefaults(t *testing.T) {
	t.Setenv("HYDRADB_AUTH_TOKEN", "tok")
	for _, k := range []string{
		"HYDRADB_BOLT_ADDR", "HYDRADB_HTTP_ADDR", "HYDRADB_ADMIN_ADDR",
		"HYDRADB_DATABASE", "HYDRADB_NAMESPACE", "HYDRADB_CELL_ID",
	} {
		t.Setenv(k, "")
	}
	c := LoadGraphConfig()
	if c.BoltAddr != "127.0.0.1:7687" {
		t.Errorf("bolt default = %q", c.BoltAddr)
	}
	if c.HTTPAddr != "http://127.0.0.1:8443" {
		t.Errorf("http default = %q", c.HTTPAddr)
	}
	if c.AdminAddr != "http://127.0.0.1:19091" {
		t.Errorf("admin default = %q (verified running instance)", c.AdminAddr)
	}
	if c.Database != "default" || c.Namespace != "default" || c.CellID != "cell-0" {
		t.Errorf("graph defaults = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestConfigTokenFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "auth-token")
	if err := os.WriteFile(p, []byte("file-token-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYDRADB_AUTH_TOKEN", "")
	t.Setenv("HYDRADB_AUTH_TOKEN_FILE", p)
	c := LoadGraphConfig()
	if c.AuthToken != "file-token-123" {
		t.Errorf("token from file = %q", c.AuthToken)
	}
}

func TestConfigValidateMissing(t *testing.T) {
	c := Config{BoltAddr: "x", HTTPAddr: "x", AdminAddr: "x", CellID: "c", Database: "d"}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing token")
	}
}

func TestConfigStringRedactsToken(t *testing.T) {
	c := Config{BoltAddr: "b", HTTPAddr: "h", AdminAddr: "a", AuthToken: "super-secret", CellID: "c", Database: "d"}
	s := c.String()
	if strings.Contains(s, "super-secret") {
		t.Errorf("config String leaked token: %s", s)
	}
	if !strings.Contains(s, redactedPlaceholder) {
		t.Errorf("expected redaction placeholder in %s", s)
	}
}

func TestRedact(t *testing.T) {
	if redact("") != "(missing)" {
		t.Error("empty secret should be visible as missing")
	}
	if redact("abc") != redactedPlaceholder {
		t.Error("secret not redacted")
	}
}

func TestRedactURLStripsUserinfo(t *testing.T) {
	in := "postgres://user:secretpass@host:5432/db"
	out := redactURL(in)
	if strings.Contains(out, "secretpass") {
		t.Errorf("userinfo leaked: %s", out)
	}
	if redactURL("http://host:8443/x") != "http://host:8443/x" {
		t.Error("plain URL should be unchanged")
	}
}

func TestBoltSchemeless(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7687": "127.0.0.1:7687",
		"bolt://h:7687":  "h:7687",
		"neo4j://h:7687": "h:7687",
		"neo4j+s://h":    "h",
	} {
		c := Config{BoltAddr: in}
		if got := c.boltSchemeless(); got != want {
			t.Errorf("boltSchemeless(%q) = %q, want %q", in, got, want)
		}
	}
}
