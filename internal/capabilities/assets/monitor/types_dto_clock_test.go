// Package monitor — types_dto_clock_test.go: deterministic-clock tests
// for DateAfterFromCursor (PR-DETERMINISTIC-CLOCK-INJECTION).
//
// These tests pin the clock-injection contract landed in
// PR-DETERMINISTIC-CLOCK-INJECTION. The fixed clock at 2026-01-15 UTC
// makes the LookbackDays fallback path byte-stable across test runs —
// pre-fix, the same call against real system time could roll over a
// calendar boundary between invocations and produce non-deterministic
// YYYYMMDD output.
//
// Tests are NOT t.Parallel()-safe: SetDefaultNowForTests mutates the
// package-level defaultNowFn, so concurrent execution against the
// rest of the monitor package would race. Sequence them sequentially
// by convention.
package assets

import (
	"testing"
	"time"
)

// fixedClock20260115 returns a deterministic time.Time pinned at
// 2026-01-15 12:00:00 UTC. Used across all tests in this file.
func fixedClock20260115() time.Time {
	return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
}

// TestDateAfterFromCursor_LastCursorPrecedence pins the canonical
// precedence: a parseable RFC3339 LastCursor always wins, regardless
// of lookbackDays. The fixedClock is defensive — the doc says
// LastCursor wins, so the clock should be unreachable. Pin it.
func TestDateAfterFromCursor_LastCursorPrecedence(t *testing.T) {
	got := DateAfterFromCursor("2026-06-30T15:04:05Z", 999, fixedClock20260115)
	want := "20260630"
	if got != want {
		t.Fatalf("DateAfterFromCursor(RFC3339, 999, fixedClock) = %q, want %q", got, want)
	}
}

// TestDateAfterFromCursor_LookbackDaysWithFixedClock pins the
// fallback: with the clock held at 2026-01-15 UTC, 7-day lookback
// returns "20260108" deterministically. 1-day lookback returns
// "20260114". The user's spec calls out fixed-2026-01-15 as the
// canonical deterministic-clock anchor for this PR.
func TestDateAfterFromCursor_LookbackDaysWithFixedClock(t *testing.T) {
	cases := []struct {
		name string
		days int
		want string
	}{
		{"1-day-lookback", 1, "20260114"},
		{"7-day-lookback", 7, "20260108"},
		{"30-day-lookback", 30, "20251216"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DateAfterFromCursor("", tc.days, fixedClock20260115)
			if got != tc.want {
				t.Fatalf("DateAfterFromCursor(empty, %d, fixedClock-2026-01-15) = %q, want %q",
					tc.days, got, tc.want)
			}
		})
	}
}

// TestDateAfterFromCursor_EmptyBothReturnsEmpty pins the no-op path:
// empty LastCursor + zero LookbackDays → empty DateAfter (yt-dlp
// no-filter path). Defensive: confirm the function returns "" and
// does not consult the clock seam.
func TestDateAfterFromCursor_EmptyBothReturnsEmpty(t *testing.T) {
	got := DateAfterFromCursor("", 0, fixedClock20260115)
	if got != "" {
		t.Fatalf("DateAfterFromCursor(empty, 0) = %q, want empty string", got)
	}
}

// TestDateAfterFromCursor_NilNowResolvesToLazyDefault pins the
// lazy-default contract: nil now → defaultNowFn → time.Now in
// production. RFC3339 cursor branch should be deterministic (no
// clock consumed); LookbackDays branch produces a 2026XXXX date
// near wall-clock time (the test only asserts shape, not exact
// digits, to avoid flakiness).
func TestDateAfterFromCursor_NilNowResolvesToLazyDefault(t *testing.T) {
	// RFC3339 cursor + nil clock: deterministic, no clock consulted.
	got := DateAfterFromCursor("2026-06-30T15:04:05Z", 0, nil)
	if got != "20260630" {
		t.Fatalf("nil clock + RFC3339 cursor = %q, want %q", got, "20260630")
	}
	// Empty + 0 + nil clock: returns empty string (no clock consulted).
	got = DateAfterFromCursor("", 0, nil)
	if got != "" {
		t.Fatalf("nil clock + (empty, 0) = %q, want empty string", got)
	}
	// Empty + 7 + nil clock: lookbackDays branch + real time.Now.
	// We assert shape only (YYYYMMDD format) since the value depends
	// on when the test runs.
	got = DateAfterFromCursor("", 7, nil)
	if len(got) != 8 {
		t.Fatalf("nil clock + (empty, 7) returned %q (len %d), want 8-char YYYYMMDD string",
			got, len(got))
	}
}

// TestDateAfterFromCursor_MalformedCursorFallsBackToLookbackDays
// pins the cursor-rejection contract: a LastCursor that fails the
// length/dash sanity check (len < 10 OR wrong characters at indices
// 4 and 7) falls through to the LookbackDays branch.
//
// Note: a cursor whose first 10 characters are valid YYYY-MM-DD form
// (e.g. "2026-06-30X15:04:05Z" — bad tail after position 10) is
// ACCEPTED because the sanity check only inspects datePart[0..10].
// See TestDateAfterFromCursor_CursorTailIgnored for that contract.
func TestDateAfterFromCursor_MalformedCursorFallsBackToLookbackDays(t *testing.T) {
	cases := []struct {
		name   string
		cursor string
		days   int
		want   string
	}{
		{"garbage-not-rfc3339", "garbage-not-rfc3339", 3, "20260112"},
		{"too-short", "too-short", 1, "20260114"},
		{"wrong-dash-position", "2026/06/30T15:04:05Z", 7, "20260108"},
		{"no-dashes-in-date", "20260630T15:04:05Z", 4, "20260111"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DateAfterFromCursor(tc.cursor, tc.days, fixedClock20260115)
			if got != tc.want {
				t.Errorf("DateAfterFromCursor(%q, %d) = %q, want %q",
					tc.cursor, tc.days, got, tc.want)
			}
		})
	}
}

// TestDateAfterFromCursor_CursorTailIgnored pins the cursor-acceptance
// lenient side: a LastCursor whose first 10 characters are valid
// YYYY-MM-DD form is accepted REGARDLESS of the tail characters. This
// is the function's len-10-inspect semantic; the original 511-LoC
// god-service shape had this same leniency and PR-DETERMINISTIC-CLOCK-
// INJECTION preserves it byte-equivalently.
func TestDateAfterFromCursor_CursorTailIgnored(t *testing.T) {
	cases := []struct {
		name   string
		cursor string
		want   string
	}{
		{"valid-RFC3339", "2026-06-30T15:04:05Z", "20260630"},
		{"non-T-tail", "2026-06-30X15:04:05Z", "20260630"},
		{"extra-trailing-chars", "2026-06-30T15:04:05Z-extra", "20260630"},
		{"exactly-10-chars", "2026-06-30", "20260630"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DateAfterFromCursor(tc.cursor, 999, fixedClock20260115)
			if got != tc.want {
				t.Errorf("DateAfterFromCursor(%q, 999) = %q, want %q (len-10 inspect promoted to first-10, rest ignored)",
					tc.cursor, got, tc.want)
			}
		})
	}
}

// TestSetDefaultNowForTests pins the audit-trail helper: passing
// nil restores production default (time.Now); passing a function
// replaces it for subsequent DateAfterFromCursor calls.
// t.Cleanup restores the default to avoid cross-test pollution.
func TestSetDefaultNowForTests(t *testing.T) {
	// Restore production default at end of test to avoid cross-test
	// pollution. Even if this test panics mid-way, defaultNowFn
	// returns to time.Now via t.Cleanup.
	t.Cleanup(func() { SetDefaultNowForTests(nil) })

	// Swap in a fixed clock.
	clock := func() time.Time { return time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC) }
	SetDefaultNowForTests(clock)
	got := DateAfterFromCursor("", 7, nil) // nil → defaultNowFn → fixed clock
	want := "20260108"
	if got != want {
		t.Fatalf("set-fixed-clock + DateAfterFromCursor(empty, 7, nil) = %q, want %q", got, want)
	}

	// Restore via nil (production default).
	SetDefaultNowForTests(nil)
	// Don't assert time.Now output — just confirm no panic / no UAF.
	_ = DateAfterFromCursor("", 7, nil)
}
