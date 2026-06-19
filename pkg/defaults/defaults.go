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
