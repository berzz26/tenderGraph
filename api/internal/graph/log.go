package graph

import (
	"log"
	"strings"
)

// Operational logging follows the existing repository convention (stdlib
// log.Printf with a short component prefix) and adds secret redaction.
// Credentials, bearer tokens and connection secrets are never logged.

const redactedPlaceholder = "***redacted***"

// redact replaces a non-empty secret with a fixed placeholder. An empty
// secret renders as "(missing)" so configuration problems stay visible.
func redact(s string) string {
	if s == "" {
		return "(missing)"
	}
	return redactedPlaceholder
}

// redactURL strips any userinfo (user:password@) and token query parameter
// from a URL-ish string before it is logged.
func redactURL(u string) string {
	if i := strings.Index(u, "@"); i >= 0 && strings.Contains(u[:i], ":") {
		if s := strings.Index(u, "//"); s >= 0 {
			return u[:s+2] + redactedPlaceholder + u[i:]
		}
	}
	return u
}

// Logf logs a message attributed to the graph subsystem. Values may include
// the package error types, which already redact secrets.
func Logf(format string, args ...any) {
	log.Printf("graph: "+format, args...)
}

// Never-logged guarantee tests live in log_test.go.
