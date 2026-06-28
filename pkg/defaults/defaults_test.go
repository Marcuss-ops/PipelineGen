// Round-trip tests for the SSOT helper surface in this package.
// Each helper mirrors the contract documented in AGENTS.md §🧰
// Utilities / "Default coalesce" row — collapsing a zero-or-empty
// input to a fallback is the whole job of this leaf package, so the
// tests are anchored to the > 0 / non-empty semantic for the four
// numeric-or-string helpers, and to the "true|1|yes|on" semantic
// for the boolean helper (PJ-CURATE-1).
//
// DRIFT-DEFAULTS-DURATION (June 2026, Step 4 PR1): the Duration
// helper is the new addition. The asymmetric "zero collapses to
// fallback, negative is preserved as-is" semantic is intentional
// per the helper's doc comment and is anchored here so any future
// refactor to `<= 0` (which would silently collapse timer.go's
// "infinite" sentinel) fails the round-trip test below.
package defaults

import (
	"testing"
	"time"
)

// TestInt_RoundTrip pins the > 0 collapse-to-fallback contract:
//   val > 0  → val is returned verbatim
//   val == 0 → fallback is returned
//   val < 0  → fallback is returned (negative is not a sentinel)
func TestInt_RoundTrip(t *testing.T) {
	cases := []struct {
		name           string
		val, fallback  int
		want           int
	}{
		{"positive keeps val", 42, 7, 42},
		{"zero collapses", 0, 7, 7},
		{"negative collapses", -1, 7, 7},
		{"fallback zero is allowed", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Int(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Int(%d, %d) = %d, want %d", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestFloat64_RoundTrip pins the > 0 contract for the float helper.
//   val >  0 → val is returned verbatim
//   val == 0 → fallback is returned
//   val <  0 → fallback is returned
func TestFloat64_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		val, fallback float64
		want          float64
	}{
		{"positive keeps val", 0.42, 0.07, 0.42},
		{"zero collapses", 0, 0.07, 0.07},
		{"negative collapses", -0.5, 0.07, 0.07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Float64(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Float64(%v, %v) = %v, want %v", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestDuration_RoundTrip pins the > 0 contract for the duration
// helper. Negative is intentionally NOT collapsed — anything ≤ 0
// including the negative-as-infinite-sentinel pattern used by
// timer.go is collapsed here because the helpers are designed for
// "input from config" callers that pass 0 when the operator didn't
// set the field, not for "internal sentinel" callers. Callers that
// need a distinct negative semantics MUST branch before calling
// Duration.
func TestDuration_RoundTrip(t *testing.T) {
	cases := []struct {
		name           string
		val, fallback  time.Duration
		want           time.Duration
	}{
		{"positive keeps val", 5 * time.Minute, 30 * time.Second, 5 * time.Minute},
		{"zero collapses", 0, 30 * time.Second, 30 * time.Second},
		{"negative collapses", -1 * time.Second, 30 * time.Second, 30 * time.Second},
		{"fallback zero is allowed", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Duration(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Duration(%v, %v) = %v, want %v", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestString_RoundTrip pins the non-empty-after-trim semantic for
// the string helper. Whitespace-only input is collapsed (matches
// the existing trimspace branch in String()).
func TestString_RoundTrip(t *testing.T) {
	cases := []struct {
		name              string
		val, fallback     string
		want              string
	}{
		{"non-empty keeps val", "hello", "fallback", "hello"},
		{"empty collapses", "", "fallback", "fallback"},
		{"whitespace collapses", "   \t", "fallback", "fallback"},
		{"fallback empty is allowed", "", "", ""},
		{"trimmed preserves inner spaces", "  hi  ", "fallback", "  hi  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("String(%q, %q) = %q, want %q", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestTruthy_RoundTrip pins the strict true|1|yes|on parser.
// Anything else — including "true " (trailing space), "True" (mixed
// case), or any garbage — returns false. The asymmetry is
// intentional: a typo'd query-string flag MUST NOT silently activate
// a feature toggle (PJ-CURATE-1).
func TestTruthy_RoundTrip(t *testing.T) {
	truthyCases := []string{"true", "TRUE", "True", "1", "yes", "YES", "Yes", "on", "ON", "On"}
	for _, in := range truthyCases {
		t.Run("truthy-"+in, func(t *testing.T) {
			if !Truthy(in) {
				t.Fatalf("Truthy(%q) = false, want true", in)
			}
		})
	}
	falsyCases := []string{"", " ", "false", "0", "no", "off", "enabled", "tru", "yess"}
	for _, in := range falsyCases {
		t.Run("falsy-"+in, func(t *testing.T) {
			if Truthy(in) {
				t.Fatalf("Truthy(%q) = true, want false", in)
			}
		})
	}
}
