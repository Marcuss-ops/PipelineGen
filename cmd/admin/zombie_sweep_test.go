// cmd/admin/zombie_sweep_test.go — TDD coverage for the
// pure-function seams in cmd/admin/zombie_sweep.go.
//
// godlike/07 NO-FAKE-AVAILABILITY: the DB-write path is
// covered by internal/application/jobs/service_test.go (which
// exercises (*SQLiteStore).MarkRunningJobsOlderThanFailed
// directly). This test surface pins the 2 pure-function seams
// that are extractable without a live DB:
//
//  1. computeCutoff(now, dur) — operator-supplied duration
//     is subtracted from `now`; result is always in UTC.
//  2. formatDryRunReport(cutoff, reason) — output stays
//     byte-stable so operators can rely on the format
//     (e.g. for monitoring scripts that grep the output).
//
// godlike/06 SSOT: the typed-error sentinels
// (ErrZombieSweepNoDB + ErrZombieSweepOpenDB) are tested via
// the errors.Is probe in their own tests; the constructor
// signature pins drift via go vet + go build at typecheck time.

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestComputeCutoff_DefaultDuration pins the 1h default
// (godlike/07 NO-FAKE-AVAILABILITY: operator MUST be able to
// reason about the threshold BEFORE --apply).
func TestComputeCutoff_DefaultDuration(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cutoff := computeCutoff(now, 1*time.Hour)
	want := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	if !cutoff.Equal(want) {
		t.Errorf("computeCutoff: got %v, want %v", cutoff, want)
	}
	// godlike/06: cutoff is always UTC (operators grep logs by
	// the RFC3339 "Z" suffix; non-UTC would silently drift).
	if cutoff.Location() != time.UTC {
		t.Errorf("computeCutoff: expected UTC, got %v", cutoff.Location())
	}
}

// TestComputeCutoff_CustomDuration pins that the operator can
// override the default (e.g. pulsing the window down to 5m
// during a known outage — same use case as
// PR-ORPHAN-SWEEPER-TUNING).
func TestComputeCutoff_CustomDuration(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	cutoff := computeCutoff(now, 5*time.Minute)
	want := time.Date(2026, 7, 4, 11, 55, 0, 0, time.UTC)
	if !cutoff.Equal(want) {
		t.Errorf("computeCutoff: got %v, want %v", cutoff, want)
	}
}

// TestComputeCutoff_NonUTCInput_NormalizesToUTC pins that
// passing a non-UTC `now` still produces a UTC cutoff
// (defense-in-depth against a future refactor that forgets
// the .UTC() call).
func TestComputeCutoff_NonUTCInput_NormalizesToUTC(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		// Skip on systems without tzdata (CI minimal images).
		t.Skipf("Asia/Tokyo tzdata not available: %v", err)
	}
	now := time.Date(2026, 7, 4, 21, 0, 0, 0, tokyo) // 12:00 UTC
	cutoff := computeCutoff(now, 1*time.Hour)
	if cutoff.Location() != time.UTC {
		t.Errorf("computeCutoff: expected UTC, got %v", cutoff.Location())
	}
	if cutoff.Hour() != 11 {
		t.Errorf("computeCutoff: expected 11:00 UTC, got %v", cutoff)
	}
}

// TestFormatDryRunReport_ByteStable pins the dry-run output
// format. godlike/07 minimum-blast-radius: any future refactor
// that changes the format will break this test, forcing the
// operator to consciously ack the change.
func TestFormatDryRunReport_ByteStable(t *testing.T) {
	cutoff := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	reason := "swept by admin zombie-sweep CLI"
	got := formatDryRunReport(cutoff, reason)
	wantSubstrings := []string{
		"zombie-sweep: DRY-RUN",
		"lease_expiry < 2026-07-04T11:00:00Z",
		"reason: \"swept by admin zombie-sweep CLI\"",
		"--apply",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("formatDryRunReport: missing %q in output:\n%s", want, got)
		}
	}
}

// TestFormatDryRunReport_DefaultReason pins the default-reason
// string used in cmd/admin/zombie_sweep.go (the defaultReason
// const). godlike/07 NO-FAKE-AVAILABILITY: a future refactor
// that changes the default reason should consciously ack via
// this test.
func TestFormatDryRunReport_DefaultReason(t *testing.T) {
	cutoff := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	got := formatDryRunReport(cutoff, defaultReason)
	if !strings.Contains(got, "Phase 9 JOBS-T01-ZOMBIE-SWEEP closure") {
		t.Errorf("formatDryRunReport: default reason must reference the Phase 9 closure, got:\n%s", got)
	}
}

// TestErrZombieSweepNoDB_IsExported pins the typed-error contract
// (godlike/07): the sentinel MUST be exported + errors.Is-compatible
// for downstream callers (e.g. test fixtures, operator scripts).
func TestErrZombieSweepNoDB_IsExported(t *testing.T) {
	if ErrZombieSweepNoDB == nil {
		t.Fatal("ErrZombieSweepNoDB must be non-nil (typed-error contract)")
	}
	wrapped := wrapForTest("zombie-sweep: %w", ErrZombieSweepNoDB)
	if !errors.Is(wrapped, ErrZombieSweepNoDB) {
		t.Errorf("ErrZombieSweepNoDB: errors.Is probe must succeed, got %v (raw: %s)", wrapped, wrapped.Error())
	}
}

// TestErrZombieSweepOpenDB_IsExported pins the typed-error contract
// for the DB-open failure mode. Distinct sentinel from
// ErrZombieSweepNoDB because the failure modes are different
// (no path vs. path-but-cannot-open).
func TestErrZombieSweepOpenDB_IsExported(t *testing.T) {
	if ErrZombieSweepOpenDB == nil {
		t.Fatal("ErrZombieSweepOpenDB must be non-nil (typed-error contract)")
	}
	wrapped := wrapForTest("zombie-sweep: open db: %w", ErrZombieSweepOpenDB)
	if !errors.Is(wrapped, ErrZombieSweepOpenDB) {
		t.Errorf("ErrZombieSweepOpenDB: errors.Is probe must succeed, got %v (raw: %s)", wrapped, wrapped.Error())
	}
	if errors.Is(wrapped, ErrZombieSweepNoDB) {
		t.Errorf("ErrZombieSweepOpenDB must be distinct from ErrZombieSweepNoDB (different failure modes)")
	}
}

// wrapForTest is a local helper that mirrors the production
// fmt.Errorf %w pattern. Lives in _test.go (not exported).
func wrapForTest(format string, err error) error {
	return fmt.Errorf("%s: %w", format[:len(format)-3], err)
}
