// Package defaults provides coalesce-style helper functions for applying
// default values when a variable is zero or empty. These are useful for
// simplifying the common pattern: if x == "" { x = y } or if x <= 0 { x = y }.
package defaults

import "strings"

// String returns val if it is non-empty (after trimming whitespace), otherwise fallback.
func String(val, fallback string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return fallback
}

// Int returns val if it is greater than zero, otherwise fallback.
func Int(val, fallback int) int {
	if val > 0 {
		return val
	}
	return fallback
}

// Float64 returns val if it is greater than zero, otherwise fallback.
func Float64(val, fallback float64) float64 {
	if val > 0 {
		return val
	}
	return fallback
}

// Truthy parses a string as boolean. Accepts true|1|yes|on (case-insensitive).
// Anything else (including empty string) returns false. The asymmetry is
// intentional: a missing or typo'd query-string flag MUST NOT silently
// activate a feature toggle. Used by `?search=`, `?allow_text_only=`,
// future-gate-style env-parsing patterns.
//
// PJ-CURATE-1 (June 2026): this helper was extracted from a one-off
// truthyQuery() in internal/api/script/handler_flow_ops.go so future
// flag-parsing handlers reuse the same canonical interpretation.
func Truthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
