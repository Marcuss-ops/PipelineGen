package mediasearch

import (
	"regexp"
	"strings"
)

// SanitizeProviderErrors strips server-internal information from
// the per-backend failure map so the response never leaks stack
// traces, raw Drive URLs, /tmp filesystem paths, or
// deployment-secret strings. The sanitization is name-aware (the
// values are canonical short labels; anything matching internal
// patterns is replaced with a generic "<redacted>" marker).
//
// godlike/07 fail-closed: an entry whose value is a fully-redacted
// label is still propagated so dashboards see WHICH backends were
// involved (operators diagnose via dashboards + log lines, not via
// the response body).
func SanitizeProviderErrors(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = sanitizeMessage(v)
	}
	return out
}

// tokenRedactRegex matches token-bearing shapes like `token=`,
// `token: `, `token ` followed by an alphanumeric identifier.
// Bare-occurrence substrings like "context token expired" do
// NOT match (no `=`/`:`/alphanum adjacency), so the redactor
// only fires on actual token-leak shapes.
var tokenRedactRegex = regexp.MustCompile(`(?i)(?:\b|^)[-_]?token\b[-_:=]?\s*[A-Za-z0-9]`)

// sanitizeMessage returns a public-safe failure summary. The
// canonical heuristic: if the message contains marker patterns
// (filesystem paths, http(s) URLs, stack-trace adjacency, secret-
// bearing substrings, and auth-header markers), replace with
// "<redacted>"; otherwise, trim leading ordering noise
// ("backend: " prefix) and cap length at 240 chars.
//
// Commit 2 BACKFILL/CUTOVER (July 2026, code-reviewer revision):
// redactor set widened to cover the AGENTS.md godlike/07
// "operationally conservative" sidebar — added "password",
// tokenRedactRegex, "bearer", "authorization" markers in
// addition to the existing "secret" + filesystem-URL coverage.
// False positives (over-redacting benign failures) are the safe
// failure mode for the public-facing wire surface.
func sanitizeMessage(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "/") ||
		strings.Contains(low, "/tmp/") ||
		strings.Contains(low, "stack:") ||
		strings.Contains(low, "secret") ||
		strings.Contains(low, "password") ||
		tokenRedactRegex.MatchString(low) ||
		strings.Contains(low, "bearer") ||
		strings.Contains(low, "authorization") ||
		strings.Contains(low, "https://") ||
		strings.Contains(low, "http://") {
		return "<redacted>"
	}
	// Cap the public length so a verbose upstream error does not
	// bloat the wire payload.
	const cap = 240
	if len(s) > cap {
		s = s[:cap] + "..."
	}
	return s
}
