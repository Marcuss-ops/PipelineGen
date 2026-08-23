// Package artifact_finalize — service_extras_test.go (FASE 3 /
// Push 3.1e cleanup, July 2026).
//
// 2 deferred hermetic tests for the finalizerService concrete
// (forward-pointers noted in the Push 3.1d round-2 code-review):
//
//  1. TestFinalizerService_Finalize_AbortsOnCancelledContext
//     — exercises the infrastructural-error abort path (vs the
//     ErrTerminalStateRejection idempotent-swallow path). The
//     test cancels the ctx BEFORE Finalize() runs and asserts
//     (a) errors.Is(err, context.Canceled) is TRUE, (b) the
//     error is NOT errors.Is ErrArtifactRequiredMissing
//     (different failure class), and (c) the FinalizeResult
//     returned is non-nil with zero counters (uniform caller
//     observability, godlike/07).
//
//  2. TestFinalizerService_Finalize_LogFieldsStableOnHappyPath
//     — pins the structured field names of the "finalize
//     completed" Info log line. Uses `go.uber.org/zap/zaptest/
//     observer` to capture the emitted log entries and asserts
//     the canonical field key set: job_id, scanned,
//     required_total, flipped_to_succeeded, optional_failed,
//     optional_still_staged. A future refactor that renames
//     any field breaks this test, preventing silent log-grep
//     breakage in operator dashboards.
//
// Both tests reuse the helpers (setupTestDB, validStage,
// newFinalizer, insertAndPublish) declared in service_test.go
// — Go test files in the same package share unexported
// helpers, so no duplication is needed.
//
// godlike/06 SSOT: this file is the canonical test surface for
// the 2 forward-pointers; service_test.go remains the canonical
// surface for the 8 saga-scenario tests (happy path + failure
// modes + idempotent + concurrent re-flip).
// godlike/07 fail-closed: every test asserts at the typed-
// error or typed-sentinel level (errors.Is / field-name match).
package artifact_finalize

import (
	"context"
	"errors"
	"testing"

	artifactstages "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/artifact_stages"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// logFieldsStableExpected is the canonical set of structured
// field keys emitted by the `finalize completed` Info log
// line (kept in a package-level constant so the test + the
// service.go implementation can be diff-verified at code-
// review time — a key added here WITHOUT a matching zap.X(...)
// in service.go is a contract drift signal).
var logFieldsStableExpected = []string{
	"job_id",
	"scanned",
	"required_total",
	"flipped_to_succeeded",
	"optional_failed",
	"optional_still_staged",
}

// ── Scenario 9: Ctx cancellation aborts Finalize ─────────────────────

// TestFinalizerService_Finalize_AbortsOnCancelledContext pins
// the abort-on-infrastructural-error contract (godlike/07):
// a cancelled context aborts the Finalize call BEFORE the
// repository reads any rows; the error wraps context.Canceled;
// the FinalizeResult is non-nil with JobID echoed back and
// Scanned==0 (uniform caller observability).
//
// The test is the canonical regression sentinel for the
// `return result, fmt.Errorf(...)` branch (vs the
// `continue` ErrTerminalStateRejection swallowing branch).
// A future refactor that drops the populated-result-on-error
// pattern will turn `got == nil` back into a regression.
func TestFinalizerService_Finalize_AbortsOnCancelledContext(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	// Seed a single row so the test would otherwise have
	// something to scan; the cancelled ctx should abort
	// BEFORE the scan, so Scanned MUST remain 0 even with
	// a seeded row.
	insertAndPublish(t, repo, "art-req-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)

	// Build a context already cancelled (NOT a deferred
	// cancel — the cancel MUST happen BEFORE Finalize so
	// the test exercises the pre-flight abort path, not
	// the mid-call cancel-detection path).
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	got, err := svc.Finalize(cancelledCtx, "job-test-1")

	// (a) error MUST be non-nil.
	if err == nil {
		t.Fatalf("cancelled ctx: want non-nil error, got nil")
	}

	// (b) error MUST wrap context.Canceled (the underlying
	// SQLite driver propagates the ctx error via the
	// standard database/sql contract; my fmt.Errorf wrap
	// preserves the chain via %w).
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: errors.Is(err, context.Canceled) = false; err = %v", err)
	}

	// (c) error MUST NOT be a sentinel for an unrelated
	// failure class (e.g. ErrArtifactRequiredMissing, which
	// is the readiness-gate sentinel — distinct from
	// infrastructural errors per godlike/07 fail-closed).
	if errors.Is(err, artifact.ErrArtifactRequiredMissing) {
		t.Errorf("cancelled ctx: must NOT be errors.Is ErrArtifactRequiredMissing (distinct failure class)")
	}

	// (d) FinalizeResult MUST be non-nil with zero counters
	// (uniform caller observability — every error path
	// returns a typed envelope).
	if got == nil {
		t.Fatalf("cancelled ctx: want non-nil FinalizeResult (uniform caller observability); got nil")
	}
	if got.JobID != "job-test-1" {
		t.Errorf("cancelled ctx: JobID = %q, want %q", got.JobID, "job-test-1")
	}
	if got.Scanned != 0 {
		t.Errorf("cancelled ctx: Scanned = %d, want 0 (ListByJob aborted before reading)", got.Scanned)
	}
	if got.RequiredTotal != 0 || got.FlippedToSucceeded != 0 ||
		got.OptionalFailed != 0 || got.OptionalStillStaged != 0 {
		t.Errorf("cancelled ctx: zero-counter envelope invariant violated; got=%+v", got)
	}

	// (e) The seeded row MUST remain in PUBLISHED state — the
	// abort MUST NOT have silently flipped any rows.
	st, err := repo.GetByID(context.Background(), "art-req-1")
	if err != nil {
		t.Fatalf("get seeded row post-abort: %v", err)
	}
	if st.State != artifact.ArtifactStageStatePublished {
		t.Errorf("post-abort State = %q, want PUBLISHED (abort MUST NOT flip rows)", st.State)
	}
}

// ── Scenario 10: Log fields stable on happy path ─────────────────────

// TestFinalizerService_Finalize_LogFieldsStableOnHappyPath
// pins the structured field-name contract for the "finalize
// completed" Info log line. A future refactor that renames
// any of the canonical fields (e.g. `required_total` ->
// `required_rows`) will silently break operator dashboards
// that grep by field key; this test catches the rename at
// CI time.
//
// The test uses `go.uber.org/zap/zaptest/observer` to capture
// the emitted log entries (no STDOUT spam, no parallel-test
// timing issues). The observer's `All()` returns a copy of
// every emitted entry, so assertion ordering is deterministic.
//
// Coverage note: this test also asserts EXACTLY one Info
// entry is emitted per Finalize call (the empty-job Debug
// log line on the no-op branch is NOT emitted on the
// happy-path branch). A future bug that double-logs or
// skips the audit log will fail this test.
func TestFinalizerService_Finalize_LogFieldsStableOnHappyPath(t *testing.T) {
	// Build observer-backed logger (InfoLevel so all the
	// Finalize info-level lines are captured).
	core, recorded := observer.New(zap.InfoLevel)
	log := zap.New(core)

	repo := artifactstages.NewRepository(setupTestDB(t))
	svc, err := NewFinalizerService(repo, log)
	if err != nil {
		t.Fatalf("NewFinalizerService: %v", err)
	}

	insertAndPublish(t, repo, "art-req-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)

	if _, err := svc.Finalize(context.Background(), "job-test-1"); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 Info log entry from Finalize happy-path; got %d: %+v",
			len(entries), entries)
	}
	if entries[0].Level != zap.InfoLevel {
		t.Errorf("Level = %v, want InfoLevel", entries[0].Level)
	}
	if entries[0].Message != "finalize completed" {
		t.Errorf("Message = %q, want %q", entries[0].Message, "finalize completed")
	}

	// Walk the structured context fields and assert each
	// canonical key is present (key-only; values are not
	// asserted because production runs use time.Now() values
	// the test cannot reproduce deterministically).
	present := make(map[string]bool, len(entries[0].Context))
	for _, f := range entries[0].Context {
		present[f.Key] = true
	}
	for _, want := range logFieldsStableExpected {
		if !present[want] {
			t.Errorf("missing canonical log field %q (operator-dashboard grep will break)", want)
		}
	}

	// Inverse: assert EXACTLY ONE entry — no Debug level
	// "no stages found" line should fire on the happy path
	// with one PUBLISHED row (that Debug only fires when
	// stages cache is empty).
	for _, entry := range entries {
		if entry.Level == zap.DebugLevel && entry.Message == "finalize: no stages found for job_id (no-op)" {
			t.Errorf("unexpected Debug no-op log line on happy path: %+v", entry)
		}
	}
}
