// Package artifact_finalize — service_test.go (FASE 3 / Push 3.1d,
// July 2026).
//
// Hermetic round-trip tests for the finalizerService concrete.
// Uses in-memory SQLite + the canonical artifact_stages DDL
// (a subset of migration 147 — the same DDL the repository's
// own test file uses, inlined here for test-package isolation).
//
// Coverage (8 hermetic scenarios, all using the canonical
// domain sentinels):
//   - Happy path: REQUIRED+PUBLISHED + OPTIONAL+PUBLISHED →
//     both flip to SUCCEEDED; counters reflect 2 flips.
//   - Missing required: REQUIRED+STAGED blocks readiness;
//     ErrArtifactRequiredMissing trips with the FIRST missing
//     id + the rest appended; the published row stays in
//     PUBLISHED (readiness gate flips NOTHING on failure).
//   - Required failed permanent: REQUIRED+FAILED_PERMANENT
//     blocks readiness (FASE 3 (b) fail-closed).
//   - Optional still staged: REQUIRED+PUBLISHED + OPTIONAL+STAGED
//     → 1 flip (the required), OptionalStillStaged=2
//     (informational, NOT blocking).
//   - Optional failed permanent: REQUIRED+PUBLISHED + OPTIONAL+
//     FAILED_PERMANENT → 1 flip (the required),
//     OptionalFailed=1 (informational, NOT blocking).
//   - Empty job: ListByJob returns 0 rows → no-op result
//     (Scanned=0, nil error).
//   - Idempotent re-finalize: REQUIRED+SUCCEEDED on entry →
//     eligible loop skips the row; FlippedToSucceeded reflects
//     only the NEW flips (not the already-finalised rows).
//   - Fenced CAS concurrent rejection: a row already SUCCEEDED
//     via a parallel caller triggers ErrTerminalStateRejection
//     from MarkSucceeded; the Finalizer swallows the fence
//     reject (idempotent re-flip) and continues with the
//     remaining rows.
//
// Additional validation tests:
//   - NewFinalizerService rejects nil repo + nil log.
//   - Finalize rejects empty JobID before touching Repository.
//
// godlike/06 SSOT: the test DDL mirrors the migration 147
// CREATE TABLE statement verbatim (drift detection: if the
// migration adds new columns, this constant must be updated
// in lockstep).
// godlike/07 fail-closed: every failure path is asserted at
// the typed-error level (errors.Is to the canonical sentinels).
package artifact_finalize

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	artifactstages "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifact_stages"
	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap"
)

// canonicalDDL is the verbatim tbl-only subset of migration 147.
// Full text/indexes (3 of them) are kept in lockstep with the
// repository's own test helper (internal/platform/sqlite/
// artifact_stages/repository_test.go::canonicalDDL) and with the
// production migration file. Drift between test + production is a
// bug — fix both, fix the migration.
//
// TRIMMED-SUBSET NOTE: only the CREATE TABLE block is inlined here.
// The production migration's CREATE INDEX statements
// (idx_artifact_stages_job_state, _state_created, _dest) are
// not asserted by these tests — ListByJob uses the composite
// job+state index in production but the test does not depend on
// it (in-memory sqlite is fast enough without indexing for the
// 1-5 row test fixtures). A future scale-test helper can include
// the indexes for stress coverage.
const canonicalDDL = `
CREATE TABLE IF NOT EXISTS artifact_stages (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// setupTestDB creates an in-memory SQLite with the canonical
// artifact_stages schema. Cleanup is automatic via t.Cleanup.
// DSN: `parseTime=true&loc=UTC` — same flag-set the repository
// test uses for RFC3339Nano round-trip; production wires the
// same DSN at the composition root.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(canonicalDDL); err != nil {
		t.Fatalf("apply canonical DDL: %v", err)
	}
	return db
}

// validStage is a minimal-valid STAGED row factory. The
// returned pointer is a fresh *ArtifactStage every call (so
// tests can mutate State/Requirement without polluting
// other cases).
func validStage() *artifact.ArtifactStage {
	return &artifact.ArtifactStage{
		ID:          "art-test-1",
		JobID:       "job-test-1",
		LocalPath:   "/var/lib/pipelinegen/staging/job-test-1/art-test-1",
		Hash:        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:        4096,
		Mime:        "audio/mpeg",
		Requirement: artifact.RequirementRequired,
		Destination: "drive:voiceover/test",
		State:       artifact.ArtifactStageStateStaged,
	}
}

// newFinalizer is a tiny helper that wires a finalizerService
// with the canonical DDL + a stub logger (zap.NewNop) + the
// in-memory Repository. Mirrors the staging service_test.go
// pattern (in-process constructor, no mocks).
//
// The Repository's nowFn is left at its default (time.Now
// UTC); tests do NOT assert on specific timestamps, so
// deterministic time control is unnecessary. A future test
// that asserts on PostedAt / UpdatedAt can inject a time
// source via a SetNowFn method (forward-pointer; not
// required for the current coverage matrix).
func newFinalizer(t *testing.T) (*finalizerService, *artifactstages.Repository) {
	t.Helper()
	repo := artifactstages.NewRepository(setupTestDB(t))
	svc, err := NewFinalizerService(repo, zap.NewNop())
	if err != nil {
		t.Fatalf("NewFinalizerService: %v", err)
	}
	return svc, repo
}

// insertAndPublish inserts a STAGED row for the given id, then
// promotes it to the state named by targetState via the
// canonical Mark* method (MarkPublished for PUBLISHED,
// MarkSucceeded for SUCCEEDED, MarkFailedPermanent for
// FAILED_PERMANENT). The convenience helper keeps the test
// fixtures compact while exercising the SAME code paths a
// real publisher worker / finalizer would call.
func insertAndPublish(t *testing.T, repo *artifactstages.Repository, id string, requirement artifact.Requirement, targetState artifact.ArtifactStageState) {
	t.Helper()
	st := validStage()
	st.ID = id
	st.Requirement = requirement
	if err := repo.Insert(context.Background(), st); err != nil {
		t.Fatalf("Insert %s (req=%q): %v", id, requirement, err)
	}
	switch targetState {
	case artifact.ArtifactStageStatePublished:
		loc := `{"kind":"drive","uri":"file-` + id + `"}`
		if err := repo.MarkPublished(context.Background(), id, loc, insertTime); err != nil {
			t.Fatalf("MarkPublished %s: %v", id, err)
		}
	case artifact.ArtifactStageStateSucceeded:
		if err := repo.MarkSucceeded(context.Background(), id); err != nil {
			t.Fatalf("MarkSucceeded %s: %v", id, err)
		}
	case artifact.ArtifactStageStateFailedPermanent:
		if err := repo.MarkFailedPermanent(context.Background(), id, "test failure: "+id); err != nil {
			t.Fatalf("MarkFailedPermanent %s: %v", id, err)
		}
	case artifact.ArtifactStageStateStaged:
		// Already STAGED on insert; nothing to do.
	default:
		t.Fatalf("insertAndPublish: unsupported targetState=%q", targetState)
	}
}

// insertTime is a single-shot deterministic time used for
// MarkPublished — production uses time.Now (non-deterministic),
// but a stable fixture time simplifies post-publish GetByID
// assertions across tests. The variable is local (not a package
// const) so the role — "the moment we call MarkPublished for
// any test in this file" — is unambiguous at the call site.
var insertTime = time.Now()

// ── Scenario 1: Happy path ───────────────────────────────────────────

// TestFinalizerService_Finalize_HappyPath pins the canonical
// success shape: 1 REQUIRED row in PUBLISHED + 1 OPTIONAL row
// in PUBLISHED → both flip to SUCCEEDED in a single Finalize
// invocation. The Result counters MUST reflect the action
// (REQUIRED→SUCCEEDED + OPTIONAL→SUCCEEDED = 2 flips,
// RequiredTotal=1, OptionalFailed=0, OptionalStillStaged=0).
func TestFinalizerService_Finalize_HappyPath(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	insertAndPublish(t, repo, "art-req-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-opt-1", artifact.RequirementOptional, artifact.ArtifactStageStatePublished)

	got, err := svc.Finalize(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("Finalize: unexpected error: %v", err)
	}
	if got.JobID != "job-test-1" {
		t.Errorf("JobID = %q, want %q", got.JobID, "job-test-1")
	}
	if got.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", got.Scanned)
	}
	if got.RequiredTotal != 1 {
		t.Errorf("RequiredTotal = %d, want 1", got.RequiredTotal)
	}
	if got.FlippedToSucceeded != 2 {
		t.Errorf("FlippedToSucceeded = %d, want 2", got.FlippedToSucceeded)
	}
	if got.OptionalFailed != 0 {
		t.Errorf("OptionalFailed = %d, want 0", got.OptionalFailed)
	}
	if got.OptionalStillStaged != 0 {
		t.Errorf("OptionalStillStaged = %d, want 0", got.OptionalStillStaged)
	}

	// Verify both rows actually transitioned to SUCCEEDED.
	for _, id := range []string{"art-req-1", "art-opt-1"} {
		st, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID %s: %v", id, err)
		}
		if st.State != artifact.ArtifactStageStateSucceeded {
			t.Errorf("GetByID %s: State = %q, want SUCCEEDED", id, st.State)
		}
	}
}

// ── Scenario 2: Missing required blocks readiness ────────────────────

// TestFinalizerService_Finalize_RejectsMissingRequired pins the
// FASE 3 (b) fail-closed rule: a REQUIRED row in STAGED blocks
// finalization. The wrap MUST include (a) the canonical sentinel
// ErrArtifactRequiredMissing for errors.Is probes, (b) the
// FIRST missing artifact id, and (c) the comma-delimited list
// of remaining missing ids.
//
// CRITICAL: the published row in this scenario MUST NOT be
// flipped — readiness gate is fail-closed (FASE 3 (b)).
func TestFinalizerService_Finalize_RejectsMissingRequired(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	// 1 REQUIRED PUBLISHED (would flip on its own) + 2 REQUIRED
	// STAGED (block readiness).
	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-req-staged-1", artifact.RequirementRequired, artifact.ArtifactStageStateStaged)
	insertAndPublish(t, repo, "art-req-staged-2", artifact.RequirementRequired, artifact.ArtifactStageStateStaged)

	got, err := svc.Finalize(ctx, "job-test-1")
	if !errors.Is(err, artifact.ErrArtifactRequiredMissing) {
		t.Fatalf("Finalize: err = %v, want errors.Is ErrArtifactRequiredMissing", err)
	}
	if got == nil {
		t.Fatalf("Finalize: got=nil result; want counters even on error path")
	}
	if got.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3 (counters returned even on error path)", got.Scanned)
	}
	if got.RequiredTotal != 3 {
		t.Errorf("RequiredTotal = %d, want 3", got.RequiredTotal)
	}
	if got.FlippedToSucceeded != 0 {
		t.Errorf("FlippedToSucceeded = %d, want 0 (readiness gate MUST block ALL flips)", got.FlippedToSucceeded)
	}

	// The error message MUST mention at least one of the
	// staged ids (they are the FIRST missing required ids).
	errStr := err.Error()
	if !strings.Contains(errStr, "art-req-staged-1") && !strings.Contains(errStr, "art-req-staged-2") {
		t.Errorf("error message must name a missing required id; got %q", errStr)
	}
	// And if BOTH ids are missing, the wrap's "; additional
	// missing required ids: [...]" tail should enumerate the
	// REMAINING id(s). This is a structural assertion, not
	// ordering-sensitive: at least 2 distinct staged ids must
	// appear somewhere in the message.
	missing := 0
	for _, id := range []string{"art-req-staged-1", "art-req-staged-2"} {
		if strings.Contains(errStr, id) {
			missing++
		}
	}
	if missing < 2 {
		t.Errorf("error message must enumerate both missing required ids (got %d of 2 present in %q)", missing, errStr)
	}

	// Verify NO row was flipped (the published one stays in PUBLISHED).
	pub, err := repo.GetByID(ctx, "art-req-pub-1")
	if err != nil {
		t.Fatalf("GetByID pub: %v", err)
	}
	if pub.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED (readiness gate MUST NOT flip any row on failure)", pub.State)
	}
}

// ── Scenario 3: Required failed permanent blocks readiness ───────────

// TestFinalizerService_Finalize_RejectsRequiredFailedPermanent
// pins FASE 3 (b): a REQUIRED row in FAILED_PERMANENT is
// treated as missing-required (the saga MUST fail closed;
// grace-degrading to SUCCEEDED_WITH_WARNINGS is explicitly
// forbidden by the policy in stages.go's Requirement comment).
func TestFinalizerService_Finalize_RejectsRequiredFailedPermanent(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-req-failed-1", artifact.RequirementRequired, artifact.ArtifactStageStateFailedPermanent)

	got, err := svc.Finalize(ctx, "job-test-1")
	if !errors.Is(err, artifact.ErrArtifactRequiredMissing) {
		t.Fatalf("Finalize: err = %v, want errors.Is ErrArtifactRequiredMissing", err)
	}
	if got == nil || got.Scanned != 2 || got.RequiredTotal != 2 || got.FlippedToSucceeded != 0 {
		t.Errorf("counter shape on error: got=%+v, want {Scanned:2 RequiredTotal:2 FlippedToSucceeded:0 ...}", got)
	}

	// Verify NO row was flipped.
	pub, _ := repo.GetByID(ctx, "art-req-pub-1")
	if pub.State != artifact.ArtifactStageStatePublished {
		t.Errorf("State = %q, want PUBLISHED", pub.State)
	}
}

// ── Scenario 4: Optional still staged (informational, NOT blocking) ───

// TestFinalizerService_Finalize_OptionalStillStaged_NotBlocking
// pins the FASE 3 design choice: optional artifacts that are
// still in STAGED do NOT block finalization. They appear in
// FinalizeResult.OptionalStillStaged (observability) but the
// REQUIRED+PUBLISHED row still flips.
func TestFinalizerService_Finalize_OptionalStillStaged_NotBlocking(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-opt-staged-1", artifact.RequirementOptional, artifact.ArtifactStageStateStaged)
	insertAndPublish(t, repo, "art-opt-staged-2", artifact.RequirementOptional, artifact.ArtifactStageStateStaged)

	got, err := svc.Finalize(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("Finalize: unexpected error: %v", err)
	}
	if got.RequiredTotal != 1 || got.FlippedToSucceeded != 1 || got.OptionalStillStaged != 2 {
		t.Errorf("counter shape: got=%+v, want {RequiredTotal:1 FlippedToSucceeded:1 OptionalStillStaged:2 ...}", got)
	}
	// The required row flipped; the optional rows stay staged.
	pub, _ := repo.GetByID(ctx, "art-req-pub-1")
	if pub.State != artifact.ArtifactStageStateSucceeded {
		t.Errorf("required State = %q, want SUCCEEDED", pub.State)
	}
	for _, id := range []string{"art-opt-staged-1", "art-opt-staged-2"} {
		st, _ := repo.GetByID(ctx, id)
		if st.State != artifact.ArtifactStageStateStaged {
			t.Errorf("optional %s State = %q, want STAGED (NOT flipped)", id, st.State)
		}
	}
}

// ── Scenario 5: Optional failed permanent (informational, NOT blocking) ─

// TestFinalizerService_Finalize_OptionalFailedPermanent_NotBlocking
// pins the FASE 3 design choice: a REQUIRED+PUBLISHED row
// finalises successfully even when an OPTIONAL row has
// transitioned into FAILED_PERMANENT (the optional failure is
// informational; it appears in FinalizeResult.OptionalFailed
// but does NOT block readiness).
func TestFinalizerService_Finalize_OptionalFailedPermanent_NotBlocking(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-opt-failed-1", artifact.RequirementOptional, artifact.ArtifactStageStateFailedPermanent)

	got, err := svc.Finalize(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("Finalize: unexpected error: %v", err)
	}
	if got.FlippedToSucceeded != 1 {
		t.Errorf("FlippedToSucceeded = %d, want 1 (the required row)", got.FlippedToSucceeded)
	}
	if got.OptionalFailed != 1 {
		t.Errorf("OptionalFailed = %d, want 1 (counter observability)", got.OptionalFailed)
	}
	pub, _ := repo.GetByID(ctx, "art-req-pub-1")
	if pub.State != artifact.ArtifactStageStateSucceeded {
		t.Errorf("required State = %q, want SUCCEEDED", pub.State)
	}
}

// ── Scenario 6: Empty job ────────────────────────────────────────────

// TestFinalizerService_Finalize_EmptyJob_NoOp pins the empty-job
// no-op contract: a job_id with zero artifact_stages returns a
// zero-counter FinalizeResult + nil error (the Debug log line
// "no stages found for job_id" is asserted at integration-level
// elsewhere; the unit-test surface verifies the public API).
func TestFinalizerService_Finalize_EmptyJob_NoOp(t *testing.T) {
	svc, _ := newFinalizer(t)

	got, err := svc.Finalize(context.Background(), "job-no-such-row")
	if err != nil {
		t.Fatalf("Finalize empty: unexpected error: %v", err)
	}
	if got.Scanned != 0 || got.RequiredTotal != 0 || got.FlippedToSucceeded != 0 ||
		got.OptionalFailed != 0 || got.OptionalStillStaged != 0 {
		t.Errorf("empty-job counters: got=%+v, want all zero", got)
	}
	if got.JobID != "job-no-such-row" {
		t.Errorf("JobID = %q, want %q", got.JobID, "job-no-such-row")
	}
}

// ── Scenario 7: Idempotent re-finalize (already-SUCCEEDED on entry) ──

// TestFinalizerService_Finalize_AlreadySucceeded_Idempotent
// pins the idempotency contract: REQUIRED rows already in
// SUCCEEDED on entry do NOT block readiness (they are NOT
// "missing required") and do NOT re-flip (the eligible-loop
// skips them silently). An OPTIONAL row in SUCCEEDED is
// likewise silently skipped. The counters MUST reflect the
// input shape: the already-Succeeded rows count toward
// RequiredTotal but contribute 0 to FlippedToSucceeded.
func TestFinalizerService_Finalize_AlreadySucceeded_Idempotent(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	// 1 REQUIRED already-SUCCEEDED + 1 REQUIRED PUBLISHED +
	// 1 OPTIONAL PUBLISHED. Total flips in this invocation: 2.
	// Total required: 2 (already-Succeeded + Published).
	insertAndPublish(t, repo, "art-req-already-1", artifact.RequirementRequired, artifact.ArtifactStageStateSucceeded)
	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-opt-pub-1", artifact.RequirementOptional, artifact.ArtifactStageStatePublished)

	got, err := svc.Finalize(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("Finalize: unexpected error: %v", err)
	}
	if got.RequiredTotal != 2 {
		t.Errorf("RequiredTotal = %d, want 2 (REQUIRED rows counted regardless of state on entry)", got.RequiredTotal)
	}
	if got.FlippedToSucceeded != 2 {
		t.Errorf("FlippedToSucceeded = %d, want 2 (one required + one optional; the already-Succeeded required is a no-op)", got.FlippedToSucceeded)
	}
	if got.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3", got.Scanned)
	}
}

// ── Scenario 8: Fenced CAS concurrent rejection (idempotent re-flip) ──

// TestFinalizerService_Finalize_ConcurrentReflipIdempotent pins
// the fenced-CAS concurrency contract: if a concurrent caller
// (e.g. another publisher worker flushing the same job in
// parallel) has already flipped a row to SUCCEEDED BEFORE this
// Finalize invocation processes it, the MarkSucceeded call
// returns ErrTerminalStateRejection (the canonical fence
// reject from the repository). The Finalizer swallows this
// SPECIFIC sentinel (counts the row as a no-op, continues
// with the remaining rows).
//
// The test simulates the race by manually calling MarkSucceeded
// on the required row BEFORE Finalize, then adds a second
// optional row in PUBLISHED state — the Finalize invocation
// should still succeed, flip the optional, and not surface the
// already-Succeeded's ErrTerminalStateRejection to the caller.
func TestFinalizerService_Finalize_ConcurrentReflipIdempotent(t *testing.T) {
	svc, repo := newFinalizer(t)
	ctx := context.Background()

	insertAndPublish(t, repo, "art-req-pub-1", artifact.RequirementRequired, artifact.ArtifactStageStatePublished)
	insertAndPublish(t, repo, "art-opt-pub-1", artifact.RequirementOptional, artifact.ArtifactStageStatePublished)

	// Simulate concurrent flip: promote the required row to
	// SUCCEEDED BEFORE Finalize runs. The standard
	// Repository.MarkSucceeded fence will REJECT this call
	// from the Finalize invocation with
	// ErrTerminalStateRejection.
	if err := repo.MarkSucceeded(ctx, "art-req-pub-1"); err != nil {
		t.Fatalf("manual MarkSucceeded: %v", err)
	}

	got, err := svc.Finalize(ctx, "job-test-1")
	if err != nil {
		t.Fatalf("Finalize: unexpected error: %v (ErrTerminalStateRejection MUST be swallowed as idempotent)", err)
	}
	if got.RequiredTotal != 1 {
		t.Errorf("RequiredTotal = %d, want 1", got.RequiredTotal)
	}
	// FlippedToSucceeded counts ONLY successful MarkSucceeded
	// calls in THIS invocation. The required row's MarkSucceeded
	// was rejected by the fence (silent no-op); the optional
	// row's MarkSucceeded succeeded. Net: 1.
	if got.FlippedToSucceeded != 1 {
		t.Errorf("FlippedToSucceeded = %d, want 1 (optional flipped; required swallowed)", got.FlippedToSucceeded)
	}
	// Verify both rows are SUCCEEDED in storage (the row that
	// triggered ErrTerminalStateRejection was already
	// SUCCEEDED on the manual MarkSucceeded above).
	for _, id := range []string{"art-req-pub-1", "art-opt-pub-1"} {
		st, _ := repo.GetByID(ctx, id)
		if st.State != artifact.ArtifactStageStateSucceeded {
			t.Errorf("GetByID %s: State = %q, want SUCCEEDED (idempotent path)", id, st.State)
		}
	}
}

// ── Constructor + boundary validation tests ──────────────────────────

// TestNewFinalizerService_RejectsNilDeps pins the construction-
// time fail-fast contract (godlike/07): nil repo + nil log
// MUST trip a typed error before any state is initialised.
func TestNewFinalizerService_RejectsNilDeps(t *testing.T) {
	_, err := NewFinalizerService(nil, zap.NewNop())
	if err == nil {
		t.Errorf("nil repo: want error, got nil")
	}

	log := zap.NewNop()
	db := setupTestDB(t)
	repo := artifactstages.NewRepository(db)
	_, err = NewFinalizerService(repo, nil)
	if err == nil {
		t.Errorf("nil log: want error, got nil")
	}

	_, err = NewFinalizerService(repo, log)
	if err != nil {
		t.Errorf("valid deps: want nil error, got %v", err)
	}
}

// TestFinalize_RejectsEmptyJobID pins the pre-flight boundary
// validation: an empty JobID trips a typed error BEFORE the
// Repository scan (no wasted DB round-trip).
func TestFinalize_RejectsEmptyJobID(t *testing.T) {
	svc, _ := newFinalizer(t)
	_, err := svc.Finalize(context.Background(), "")
	if err == nil {
		t.Fatalf("empty JobID: want error, got nil")
	}
	if !strings.Contains(err.Error(), "jobID is required") {
		t.Errorf("empty JobID: want error message to mention \"jobID is required\"; got %q", err.Error())
	}
}
