// Package defaults provides coalesce-style helper functions for applying
// default values when a variable is zero or empty. These are useful for
// simplifying the common pattern: if x == "" { x = y } or if x <= 0 { x = y }.
package defaults

import (
	"strings"
	"time"
)

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

// Duration returns val if it is strictly positive (val > 0),
// otherwise fallback. The strictly-positive semantic mirrors Int /
// Float64 so a caller reading any of the four helpers sees the same
// "zero collapses to fallback" contract — a `<= 0` collapse would
// be inconsistent and would also silently swallow caller-side
// negative sentinels (where some callers express "no timeout", "use
// parent context", etc. with a negative value). Callers that
// genuinely need a distinct negative semantic MUST branch on sign
// BEFORE calling Duration.
//
// DRIFT-DEFAULTS-DURATION (June 2026, Step 4 PR1): this helper closes
// the four-way type gap. Before Step 4 PR1, callers needing a
// duration default had to write the `if val > 0 { val } else {
// fallback }` pattern inline; the duplicate copies across the
// codebase were the drift class this helper targets (see e.g.
// internal/infrastructure/artlist/cache/cache.go::TTLDuration
// collapse for one concrete pre-Step-4 instance).
func Duration(val, fallback time.Duration) time.Duration {
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
