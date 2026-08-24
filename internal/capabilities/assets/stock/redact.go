package stock

import (
	"net/url"
	"strings"
)

// redactURL returns a token-safe rendering of a URL for operator-facing
// error messages (PR-STOCK-ERROR-LEAKS-TOKEN, DoD §16 GAP-C). The raw URL
// is NEVER echoed verbatim: query strings, fragments, and userinfo are
// stripped unconditionally (they are the canonical credential carriers in
// signed URLs), and path segments that look like credentials (Bearer
// shapes, whitespace, common marker words, or long opaque runs such as
// JWTs) are masked. The host and the remaining path survive so operators
// can still identify the offending endpoint.
func redactURL(raw string) string {
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		// Parse failed (e.g. malformed escape). Query strings and
		// fragments can still carry credentials even when the rest of
		// the URL is unparseable, so mask anything with a token-like
		// shape OR with a query/fragment present — never echo verbatim.
		if looksLikeToken(raw) || strings.ContainsAny(raw, "?#") {
			return "[REDACTED]"
		}
		return raw
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	segments := strings.Split(parsed.Path, "/")
	masked := false
	for i, seg := range segments {
		if looksLikeToken(seg) {
			segments[i] = "[REDACTED]"
			masked = true
		}
	}
	if masked {
		parsed.Path = strings.Join(segments, "/")
	}
	return parsed.String()
}

// looksLikeToken reports whether s is plausibly a credential value that
// must never surface in logs or error responses. It is deliberately
// conservative (fail-closed): a false positive only masks part of an
// error message, whereas a false negative leaks a secret.
func looksLikeToken(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	// Bearer/whitespace shapes and bare marker words (Bearer <token>,
	// api_key=..., access_token=...) are credential-shaped.
	if strings.Contains(lower, "bearer") || strings.ContainsAny(s, " \t\n") {
		return true
	}
	for _, marker := range []string{
		"token", "secret", "apikey", "api_key", "access_token",
		"refresh_token", "signature", "credential", "password", "passwd",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Long opaque runs (JWTs, signed payloads) are credential-shaped.
	if len([]rune(s)) >= 64 {
		return true
	}
	return false
}
