// Package jobs — RED-2 / JOBS-T01-001 canonical scanner regression tests.
//
// godlike/06 SSOT (one canonical owner per fact): the
// `rfc3339TimeScanner` type lives in `repository_scanner.go`
// (declared commit `721653de` on origin/main). These tests live
// here so the scanner's invariants are pinned alongside the
// scanner's source. DB-layer regressions are pinned in
// `repository_events_test.go::TestListEvents_StrftimeCanonicalScan`.
// Together they pin both the scanner-type behaviour AND the
// strftime-wrap integration boundary.
//
// godlike/07 no-fake-availability: each test below FAIL-CLOSED on
// regression. A failure here is real; a pass is real. The legacy
// `TestListEvents_ScanErrorSentinel` (which logged "unexpected" on
// driver success without failing) is REMOVED — godlike/07 prefers
// SILENCE over FALSE-POSITIVE coverage.
//
// RED-2 / JOBS-T01-001 closure, 2026-07-04 (Phase 9 Battery).
// Canonical ship_sha: 721653de. godlike/06 wire-integration pin
// lives in `repository.go::ListEvents`'s scan target.
package jobs

import (
	"testing"
	"time"
)

// newTestScanner returns a fresh scanner wired to a fresh time.Time target.
// Factored out so the 7 test cases below share one construction path.
// Per godlike/07 minimum-blast-radius: the target stays as a *time.Time
// so callers can pre-set AND post-verify the value (the EmptyStringAllowed
// test reads the target post-Scan to assert no-op behaviour).
func newTestScanner(t *testing.T) (*rfc3339TimeScanner, *time.Time) {
	t.Helper()
	var got time.Time
	return &rfc3339TimeScanner{t: &got}, &got
}

// TestRfc3339TimeScanner_NilValue: a nil SQL value (NULL column) leaves
// the target time.Time untouched (default zero value stays). Mirrors the
// DEFAULT branch in the scanner's Scan method (`if value == nil { return nil }`).
func TestRfc3339TimeScanner_NilValue(t *testing.T) {
	s, got := newTestScanner(t)
	if err := s.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) returned non-nil error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("Scan(nil) modified target: got = %v, want zero time.Time", *got)
	}
}

// TestRfc3339TimeScanner_AcceptsCanonicalRFC3339Nano: the canonical
// happy path. strftime('%Y-%m-%dT%H:%M:%fZ', created_at) produces
// exactly the RFC3339Nano text format the scanner accepts. This
// test pins that contract.
func TestRfc3339TimeScanner_AcceptsCanonicalRFC3339Nano(t *testing.T) {
	want := time.Date(2026, 7, 4, 18, 30, 0, 0, time.UTC)
	str := want.Format(time.RFC3339Nano)

	s, got := newTestScanner(t)
	if err := s.Scan(str); err != nil {
		t.Fatalf("Scan(%q) returned non-nil error: %v (canonical strftime output should parse)", str, err)
	}
	if !got.Equal(want) {
		t.Errorf("Scan(%q) = %v, want %v", str, *got, want)
	}
}

// TestRfc3339TimeScanner_AcceptsByteSlice: the driver may return
// []byte for TEXT columns (some paths return string, others byte
// slice). The scanner MUST accept both forms identically.
func TestRfc3339TimeScanner_AcceptsByteSlice(t *testing.T) {
	want := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	bytes := []byte(want.Format(time.RFC3339Nano))

	s, got := newTestScanner(t)
	if err := s.Scan(bytes); err != nil {
		t.Fatalf("Scan([]byte) returned non-nil error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("Scan([]byte) = %v, want %v", *got, want)
	}
}

// TestRfc3339TimeScanner_AcceptsDriverTimeTime: if the driver
// (with parseTime=true elsewhere) returns time.Time directly, the
// scanner MUST short-circuit without re-parsing. Defensive path
// for future migrations that flip parseTime to true.
func TestRfc3339TimeScanner_AcceptsDriverTimeTime(t *testing.T) {
	want := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)

	s, got := newTestScanner(t)
	if err := s.Scan(want); err != nil {
		t.Fatalf("Scan(time.Time) returned non-nil error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("Scan(time.Time) = %v, want %v", *got, want)
	}
}

// TestRfc3339TimeScanner_RejectsMalformed is a scanner-CORRECTNESS
// test: it pins that the scanner rejects non-RFC3339Nano string inputs
// (godlike/07 typed-error contract).
//
// The strftime-wrap regression in `repository.go::ListEvents` is pinned
// separately by `TestListEvents_StrftimeCanonicalScan` (integration,
// lives in repository_events_test.go) — see that test's docstring.
func TestRfc3339TimeScanner_RejectsMalformed(t *testing.T) {
	s, got := newTestScanner(t)
	if err := s.Scan("2026-07-04 18:30:00"); err == nil {
		t.Fatalf("Scan(malformed) returned nil error; should reject legacy DATETIME format. got = %v (zero/untouched)", *got)
	}
}

// TestRfc3339TimeScanner_RejectsUnsupportedType: integer / float /
// struct values fail with typed error naming the unsupported type.
// Pins the godlike/07 typed-error contract for non-string types.
func TestRfc3339TimeScanner_RejectsUnsupportedType(t *testing.T) {
	s, _ := newTestScanner(t)
	if err := s.Scan(42); err == nil {
		t.Fatalf("Scan(int) returned nil error; should reject unsupported type per godlike/07 typed-error contract")
	}
}

// TestRfc3339TimeScanner_EmptyStringAllowed: empty-string is a no-op
// (matches the driver's behaviour on empty TIMESTAMP columns). Pins
// the early-return path inside the string-branch.
func TestRfc3339TimeScanner_EmptyStringAllowed(t *testing.T) {
	s, target := newTestScanner(t)
	preset := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC) // pre-set to non-zero
	*target = preset
	if err := s.Scan(""); err != nil {
		t.Fatalf("Scan(\"\") returned non-nil error: %v (empty-string should be no-op)", err)
	}
	if !target.Equal(preset) {
		t.Errorf("Scan(\"\") modified target: got = %v, want pre-set %v", *target, preset)
	}
}
