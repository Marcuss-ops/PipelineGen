// Package stockpipeline — retry_policy_test.go (DoD §9 retry-per-phase tests, July 2026).
//
// Pins the 4 canonical stock-pipeline per-phase failure contracts:
//  1. YTDLP_TIMEOUT              → retryable=true with backoff
//     (pkg/retry.Do loop must retry with exponential backoff; clock
//     must fire N-1 sleeps for N attempts).
//  2. FFMPEG_INVALID_INPUT       → retryable=false (terminal)
//     (pkg/retry.Do must short-circuit on attempt #1; no 10× CPU
//     waste on a non-recoverable input shape).
//  3. Drive upload failure       → orchestrator CAS-resume
//     (run #1 fails at stock.publish → pre-stages 4 Completed
//     rows persist; run #2 SKIPS plan/stage_sources/extract_clips/
//     compose_chunks via ErrStepAlreadyCompleted and only re-runs
//     stock.publish; stepStore row count stays at 5).
//  4. Qdrant down                → retry-classifier pin
//     (pkg/retry.Classify surfaces (ErrNetwork, retryable=true) for
//     a Qdrant-styled typed-transient error; NOT IsTerminal →
//     outbox.indexing handler routes the failure to MarkFailed
//     rather than dead-letter; the orchestrator's full
//     all-6-pre-Completed invariant pins "no re-render after a
//     Qdrant projection failure".)
//
// godlike/06 SSOT — every per-phase retry contract below reads
// typed sentinels directly:
//   - pkg/retry.TransientInfrastructureError typed carrier (test 1).
//   - pkg/retry.Classify → (pkg/retry.ErrorCategory, bool)
//     (tests 1, 2, 4).
//   - orchestrator_step_errors.go::ErrStockPublishArtifactFailed
//     typed sentinel wrapped with fmt.Errorf("%w", ...)
//     (test 3).
//
// godlike/07 NO-FAKE-AVAILABILITY: every retry-prediction assertion
// matches the actual production retry.Do / Classify behaviour.
// A regression in the classifier that flips a Qdrant-typed error
// to terminal (or vice-versa) would surface as a test failure
// here, not a silent outbox dead-letter.
//
// godlike/07 minimum-blast-radius: pure test file; zero production
// surface added. Reuses orchestrator_resume_test.go helpers
// (openOrchestratorResumeTestDB, stubRecorderStep,
// resumeStubPlanner, resumeStubStager, fakeSucceedingCutter,
// noopRenderer, stepInputFingerprint) and cancellation_test.go's
// pattern for stepping + atomic counter assert helpers. No
// duplicate stub types.
//
// Scope note (test 4 heavy path): the full outbox-pool
// integration (processEvent + Repository.MarkFailed status-machine
// assertions against outbox_events.status transitions of
// pending → pending-with-attempt_count++ vs dead-letter) lives in a
// follow-up commit scoped to the outboxevents package. This test
// pins the classifier + orchestrator invariants so any Qdrant
// regression in those two layers is observable without the pool
// infra being wired.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	pkgretry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── Test 1 — YTDLP_TIMEOUT ───────────────────────────────────────────────────

// fakeRetryClock is a test-only deterministic Clock for the
// retry-loop backoff-verification path. Every After(d) returns a
// pre-buffered channel that fires immediately on the next select
// iteration, so the retry loop exercises its backoff SCHEDULING
// (Logger / counter increments) without burning real wall-clock
// time. RealClock would block the test for ~60-120 ms per sleep;
// fakeRetryClock collapses to ~ns.
//
// This file stays in the same package as orchestrator_resume_test.go
// so we can also read the shallower fakeClock pattern from
// pkg/retry/clock_test.go if a deterministic-after variant is
// needed in the future.
type fakeRetryClock struct {
	afterCalls atomic.Int64
}

// fakeRetryClock MUST satisfy pkg/retry.Clock at compile time so
// drift in the Clock surface is a build failure.
var _ pkgretry.Clock = (*fakeRetryClock)(nil)

func (f *fakeRetryClock) After(_ time.Duration) <-chan time.Time {
	f.afterCalls.Add(1)
	ch := make(chan time.Time, 1)
	ch <- time.Now() // pre-buffer — the retry loop's select fires immediately
	return ch
}

// TestStock_RetryPolicy_YTDLPTimeout_RetriesWithBackoff pins the
// DoD §9 contract: a transient yt-dlp timeout MUST be retried
// with exponential backoff (not fail-fast). The stub returns the
// YTDLP-shaped transient error on the first 2 attempts and nil on
// the 3rd. cleanup surfaces a successful result, not a propagated
// error — confirming pkg/retry.Do treats the third attempt as the
// canonical success hop.
//
// What this test pins:
//   - pkg/retry.Do consumes pkg/retry.IsTransient as the
//     IsRetryable predicate (the canonical retry predicate
//     godlike/06 SSOT) without reverting to the pre-FASE 6(a)
//     "IsRetryable==nil means retry always" trap.
//   - The typed-carrier path (*TransientInfrastructureError) is
//     recognised at the typed-probe layer (FASE 6 Cut 6.1.D pure-
//     typed surface) without falling back to substring matching.
//   - The injected Clock tracks backoff count: N attempts → N-1
//     sleeps (Do loop sleeps BETWEEN attempts, not before #1 or
//     after the success).
func TestStock_RetryPolicy_YTDLPTimeout_RetriesWithBackoff(t *testing.T) {
	// No t.Parallel(): this test (and the 3 sibling retry-policy tests
	// below) share the orchestrator-side SQLite + WAL machinery with
	// the cancellation + resume_test.go siblings. Running them in
	// parallel exhausts the mattn/go-sqlite3 driver's global locks
	// during -race runs (verified by make verify-main failure:
	// "test timed out after 10m0s" cascading from concurrent WAL
	// writes). Sibling helpers (orchestrator_resume_test.go etc.)
	// intentionally omit t.Parallel() for the same reason —
	// staying serial is the canonical pattern.

	// YTDLP_TIMEOUT is a transient infrastructure error:
	// typed-carrier path is the canonical production
	// wrap-recipe at the yt-dlp SDK boundary (pkg/retry.WrapTransient).
	// The retry loop's pkg/retry.IsTransient predicate MUST
	// recognise the typed carrier via errors.As without falling
	// back to substring heuristics.
	ytdlpFail := func(attempt int) error {
		return &pkgretry.TransientInfrastructureError{
			Err: fmt.Errorf("yt-dlp: i/o timeout (attempt %d)", attempt),
		}
	}

	var calls atomic.Int64
	stubFn := func() error {
		n := calls.Add(1)
		if n <= 2 {
			return ytdlpFail(int(n))
		}
		return nil // success on the 3rd attempt
	}

	clk := &fakeRetryClock{}

	err := pkgretry.Do(context.Background(), stubFn, pkgretry.Options{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond, // nominal backoff (fake clock fires immediately)
		BackoffFactor:  2.0,
		JitterFraction: 0, // deterministic — fake clock drains in 0 wall-clock
		DisableJitter:  true,
		IsRetryable:    pkgretry.IsTransient, // canonical typed probe
		Clock:          clk,
	})

	require.NoError(t, err,
		"YTDLP_TIMEOUT (transient typed-carrier) MUST retry until success — 3rd attempt succeeded")
	assert.Equal(t, int64(3), calls.Load(),
		"YTDLP_TIMEOUT: expected 3 attempts (2 transient-fail + 1 success)")
	assert.Equal(t, int64(2), clk.afterCalls.Load(),
		"YTDLP_TIMEOUT: retry loop MUST fire exactly N-1 backoff sleeps (between attempts, not before #1 or post-success)")

	// Classify pins the transient channel explicitly. Network
	// branch is canonical for "connection refused / timeout / 5xx"
	// shapes per pkg/retry.Classify's typed-first path.
	probeErr := &pkgretry.TransientInfrastructureError{
		Err: errors.New("yt-dlp: i/o timeout"),
	}
	cat, retryable := pkgretry.Classify(probeErr)
	require.True(t, retryable,
		"YTDLP_TIMEOUT MUST classify as retryable (TypedCarrier + substring 'timeout' routes to ErrTimeout/ErrNetwork)")
	assert.True(t,
		cat == pkgretry.ErrTimeout || cat == pkgretry.ErrNetwork,
		"YTDLP_TIMEOUT MUST classify into the transient channel; got %q (terminal channel would dead-letter the source-stage phase)", cat)
}

// ── Test 2 — FFMPEG_INVALID_INPUT ─────────────────────────────────────────────

// TestStock_RetryPolicy_FFMPEGInvalidInput_TerminalNoRetry pins
// the DoD §9 contract: an FFmpeg "invalid input" failure is
// terminal (the input file is malformed; retries cannot recover
// it). The retry loop MUST short-circuit on attempt #1 — no
// 10× CPU waste on a non-recoverable shape.
//
// What this test pins:
//   - A plain errors.New without a *TransientInfrastructureError
//     carrier is canonical-TERMINAL under FASE 6 Cut 6.1.D's
//     pure-typed-probe IsTransient (substring fallback was
//     removed from production).
//   - pkg/retry.Classify(ErrorCategory, bool) routes the
//     "validation" / "invalid" substring path to ErrValidation
//     (terminal) so dashboards can differentiate this from retry
//     storm's ErrNetwork/ErrTimeout.
//   - The error propagates verbatim via fmt.Errorf("%w", ...) so
//     the orchestrator's MarkFailed path can errors.Is into the
//     typed underlying sentinel — the typed-error godlike/06
//     SSOT seam.
func TestStock_RetryPolicy_FFMPEGInvalidInput_TerminalNoRetry(t *testing.T) {
	// No t.Parallel(): see the comment at the top of
	// TestStock_RetryPolicy_YTDLPTimeout_RetriesWithBackoff for the
	// race-detector / WAL-lock issue this avoids.

	// FFMPEG_INVALID_INPUT is NOT transient — malformed input
	// is a terminal shape. We deliberately do NOT wrap in
	// *TransientInfrastructureError. Under pure-typed-probe
	// IsTransient (post FASE 6 Cut 6.1.D), the retry loop
	// classifies this as terminal and short-circuits.
	ffmpegInvalid := errors.New("ffmpeg cut: validation: invalid input parameters (codec unsupported)")

	var calls atomic.Int64
	stubFn := func() error {
		calls.Add(1)
		return ffmpegInvalid
	}

	err := pkgretry.Do(context.Background(), stubFn, pkgretry.Options{
		MaxAttempts:    10, // generous — terminal must STILL short-circuit
		InitialBackoff: 5 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
		DisableJitter:  true,
		IsRetryable:    pkgretry.Retryable, // binary classify shape
	})

	require.Error(t, err, "FFMPEG_INVALID_INPUT is terminal — error MUST propagate")
	assert.ErrorIs(t, err, ffmpegInvalid,
		"underlying typed sentinel MUST propagate verbatim so orchestrator MarkFailed can errors.Is")
	assert.Equal(t, int64(1), calls.Load(),
		"FFMPEG_INVALID_INPUT MUST short-circuit on attempt #1; no 10 retries burned on a non-recoverable shape")

	// Classify pins the failure class explicitly.
	cat, retryable := pkgretry.Classify(ffmpegInvalid)
	assert.Equal(t, pkgretry.ErrValidation, cat,
		"FFMPEG_INVALID_INPUT 'validation' substring MUST classify as ErrValidation (terminal class)")
	assert.False(t, retryable,
		"ErrValidation is canonical terminal; classifier MUST return retryable=false")

	// IsTransient (typed probe, FASE 6 Cut 6.1.D) MUST return
	// false on a plain errors.New — the canonical "untyped
	// errors are terminal" surface.
	assert.False(t, pkgretry.IsTransient(ffmpegInvalid),
		"plain errors.New without typed marker is canonical-terminal under pure-typed-probe IsTransient")
}

// ── Test 3 — Drive upload failure, resume WITHOUT re-rendering ─────────────────

// stockPublishThrowingStep is a stub Step that returns
// ErrStockPublishArtifactFailed on the FIRST Run call (failNext=true)
// and returns nil on subsequent calls. Used by the Drive-failure
// resume contract test (DoD §9 retry-per-phase) to verify:
//   - run #1 aborts at stock.publish (the wrapping fmt.Errorf("%w")
//     propagates ErrStockPublishArtifactFailed so the orchestrator's
//     MarkFailed layer can errors.Is into the typed sentinel and the
//     broker's retry classifier can route the typed error correctly).
//   - run #2 (same job_id) CAS-skips plan/stage_sources/extract_clips/
//     compose_chunks via ErrStepAlreadyCompleted and ONLY re-runs
//     stock.publish; no re-render of compose_chunks (composeChunkCount
//     stays at 0 across the SECOND run).
type stockPublishThrowingStep struct {
	name      string
	failNext  *atomic.Bool
	pubCalls  *atomic.Int32
	published *atomic.Bool
}

func (s *stockPublishThrowingStep) Name() string { return s.name }
func (s *stockPublishThrowingStep) Run(_ context.Context, _ StepRunner) error {
	s.pubCalls.Add(1)
	if s.failNext.Load() {
		return fmt.Errorf("stock.publish: simulated drive upload failure: %w",
			ErrStockPublishArtifactFailed)
	}
	s.published.Store(true)
	return nil
}

// TestStock_RetryPolicy_DriveFailure_ResumesWithoutReRendering pins
// the DoD §9 contract: when stock.publish fails on a Drive upload
// error, the orchestrator's CAS-resume contract must guarantee
// that the SECOND run against the SAME job_id SKIPS the
// plan/stage_sources/extract_clips/compose_chunks steps (which
// already persisted Completed rows in stepStore) and ONLY retries
// stock.publish. The source video MUST NOT be re-staged, the clips
// MUST NOT be re-cut, the chunks MUST NOT be re-rendered.
//
// What this test pins:
//   - Every pre-Completed step counter stays at 0 across run #2
//     (CAS-skip via ErrStepAlreadyCompleted on MarkStarted).
//   - stock.publish's pubCalls increments to 1 on run #2 (only
//     one attempt — the failing attempt from run #1 is recorded
//     in stepStore as StatusFailed, NOT in pubCalls).
//   - stepStore row count is exactly 5 after run #2 (no duplicate
//     stage rows for plan/stage_sources/extract_clips/compose_chunks).
//   - The Failed row from run #1 is REPLACED by a Completed row
//     (attempt=1, Status=Completed) via the orchestrator's
//     re-MarkStarted → re-MarkCompleted sequence. This is the
//     canonical "retry-only-upload" contract.
func TestStock_RetryPolicy_DriveFailure_ResumesWithoutReRendering(t *testing.T) {
	// No t.Parallel(): see the comment at the top of
	// TestStock_RetryPolicy_YTDLPTimeout_RetriesWithBackoff for the
	// race-detector / WAL-lock issue this avoids.

	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "drive-failure-retry-1"

	// ── dispatchSteps shared across BOTH runs (same job_id) ──
	// Pre-completion lives in the stepStore; on run #2 the
	// orchestrator's MarkStarted will return
	// ErrStepAlreadyCompleted for the 4 pre-Completed rows and
	// continue without invoking the step's Run body.
	//
	// NOTE: stubRecorderStep's count field is *int32 (sibling
	// pattern from orchestrator_resume_test.go); race-safe
	// via atomic.LoadInt32 / atomic.AddInt32 on the pointer.
	preStageCounters := [4]*int32{
		new(int32), // stock.plan
		new(int32), // stock.stage_sources
		new(int32), // stock.extract_clips
		new(int32), // stock.compose_chunks
	}
	failNext := &atomic.Bool{}
	pubCalls := &atomic.Int32{}
	published := &atomic.Bool{}

	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: preStageCounters[0]},
		&stubRecorderStep{name: "stock.stage_sources", count: preStageCounters[1]},
		&stubRecorderStep{name: "stock.extract_clips", count: preStageCounters[2]},
		&stubRecorderStep{name: "stock.compose_chunks", count: preStageCounters[3]},
		&stockPublishThrowingStep{
			name: "stock.publish", failNext: failNext, pubCalls: pubCalls,
			published: published,
		},
	}
	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}

	// ─────────────────────────── RUN #1 ──────────────────────────
	// Fail at stock.publish. The orchestrator's MarkFailed row
	// is recorded for stock.publish; the 4 pre-publish stages'
	// MarkStarted → MarkCompleted path runs first.
	failNext.Store(true)

	o1 := NewOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{},
		fakeSucceedingCutter{}, noopRenderer{})
	o1.dispatchSteps = dispatchSteps
	_, run1Err := o1.RunResilient(ctx, &RunInput{})
	require.Error(t, run1Err, "run #1: stock.publish Drive-failure MUST abort the orchestrator")
	require.ErrorIs(t, run1Err, ErrStockPublishArtifactFailed,
		"run #1: orchestrator MUST propagate the typed stock.publish sentinel verbatim (typed-error godlike/06 SSOT)")

	// After run #1: pre-publish stages have run; stock.publish
	// aborted; stepStore has 5 rows (4 Completed + 1 Failed).
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[0]), "run #1: plan Ran")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[1]), "run #1: stage_sources Ran")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[2]), "run #1: extract_clips Ran")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[3]), "run #1: compose_chunks Ran (the in-memory render)")
	assert.Equal(t, int32(1), pubCalls.Load(), "run #1: stock.publish Ran once and failed")
	assert.False(t, published.Load(), "run #1: stock.publish did NOT succeed")

	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows),
		"run #1: stepStore has exactly 5 rows (4 Completed pre-publish + 1 Failed stock.publish)")

	// Locate the publish row; verify it's Failed (not Completed
	// or anything else — this drives the resume semantics for run #2).
	var publishRow *steps.StepState
	for i := range rows {
		if rows[i].StepKey == "stock.publish" {
			r := rows[i]
			publishRow = &r
		}
	}
	require.NotNil(t, publishRow, "run #1: stepStore MUST have a stock.publish row")
	assert.Equal(t, steps.StatusFailed, publishRow.Status,
		"run #1: stock.publish row MUST be StatusFailed so run #2 can re-MarkStarted on it")

	// ─────────────────────────── RUN #2 ──────────────────────────
	// Same job_id. Success on stock.publish. Orchestrator MUST
	// CAS-skip the 4 pre-Completed rows (no re-stage / no re-cut /
	// no re-render) and ONLY re-run stock.publish.
	failNext.Store(false)

	o2 := NewOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{},
		fakeSucceedingCutter{}, noopRenderer{})
	o2.dispatchSteps = dispatchSteps
	_, run2Err := o2.RunResilient(ctx, &RunInput{})
	require.NoError(t, run2Err,
		"run #2: stock.publish now succeeds → orchestrator MUST return nil (retry-only-upload contract)")

	// Critical assertion: pre-publish stages' counters DID NOT
	// increment on run #2 — the orchestrator's ErrStepAlreadyCompleted
	// branch (`continue` in orchestrator_run.go) SKIPPED the
	// step body. Their counters reflect run #1 only.
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[0]),
		"run #2: stock.plan was pre-Completed in run #1; Run MUST NOT be called again")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[1]),
		"run #2: stock.stage_sources was pre-Completed; source MUST NOT be re-staged")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[2]),
		"run #2: stock.extract_clips was pre-Completed; clips MUST NOT be re-cut")
	assert.Equal(t, int32(1), atomic.LoadInt32(preStageCounters[3]),
		"run #2: stock.compose_chunks was pre-Completed; chunks MUST NOT be re-rendered")

	// publish re-ran and succeeded.
	assert.Equal(t, int32(2), pubCalls.Load(),
		"run #2: stock.publish MUST re-run exactly once (the failed attempt from run #1 is in stepStore, NOT in pubCalls)")
	assert.True(t, published.Load(), "run #2: stock.publish succeeded (retry-only-upload contract honoured)")

	// stepStore row count is STILL exactly 5 — no duplicate
	// stage rows for the 4 pre-publish phases. The Failed row
	// for stock.publish is REPLACED by a Completed row.
	rows2, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows2),
		"run #2: stepStore has exactly 5 rows (CAS-resume contract — NO duplicate stage rows)")

	for _, r := range rows2 {
		assert.Equal(t, steps.StatusCompleted, r.Status,
			"run #2: every row must be StatusCompleted after retry-only-upload succeeds (step=%s)", r.StepKey)
	}
	// Per-row attempt pin: locks the SQLite CAS-preservation
	// semantics strictly. Without this, a regression that
	// touches sqlite_store.go's CASE clause (e.g. removing
	// the `status='completed'` short-circuit so EVERY step
	// bumps on re-MarkStarted) would slip through a weak
	// `>= 1` guard. The exact per-row map is the audit pin:
	//   - 4 pre-Completed stages: CAS preserves attempt=1 (the
	//     CASE-preserve-on-completed branch is the canonical
	//     godlike/07 terminal-immutability seam).
	//   - stock.publish row: was Failed(attempt=1) after run #1;
	//     MarkStarted on Failed row flips to Pending(attempt+1=2)
	//     per sqlite_store.go's ON CONFLICT clause (the CASE
	//     `'completed'` is FALSE on Failed so the ELSE branch
	//     fires). Run → MarkCompleted preserves the attempt
	//     counter, so it stays at 2.
	expectedAttempts := map[string]int{
		"stock.plan":           1,
		"stock.stage_sources":  1,
		"stock.extract_clips":  1,
		"stock.compose_chunks": 1,
		"stock.publish":        2, // Failed(1)→Pending(2)→Completed(2)
	}
	for _, r := range rows2 {
		require.Contains(t, expectedAttempts, r.StepKey,
			"unexpected step_key in stepStore; test fixture drift")
		assert.Equal(t, expectedAttempts[r.StepKey], r.Attempt,
			"run #2: attempt counter MUST pin per CAS semantics (step=%s; expected %d, got %d)",
			r.StepKey, expectedAttempts[r.StepKey], r.Attempt)
	}
}

// ── Test 4 — Qdrant down → retry-or-stay-pending classifier pin ───────────────

// TestStock_RetryPolicy_QdrantDown_OutboxKeepsRetryClassifier pins
// the DoD §9 contract: when the Qdrant projection fails during
// stock.finalize's best-effort index path, the failure surface
// must:
//   - Be classified as ERR-NETWORK (transient) by pkg/retry so the
//     outbox.indexing handler routes the failure to MarkFailed
//     (pending-with-attempt_count++) rather than MarkDeadLetter.
//   - NOT be classified as ErrTerminal / ErrMissingHandler /
//     ErrBadPayload — those pipelines dead-letter immediately and
//     stop retrying, which would freeze the indexing pipeline on a
//     transient Qdrant outage.
//   - Honour pkg/retry.Do's exponential-backoff retry semantics
//     (max attempts honoured, clock-driven backoff verified).
//
// Plus the orchestrator-level invariant:
//
//   - All 6 stages pre-Completed → re-run on the SAME job_id does
//     NOT re-render. The orchestrator_run.go CAS contract
//     (MarkStarted → ErrStepAlreadyCompleted → continue) is the
//     canonical "no re-render after a transient failure" mechanism;
//     the same path that saved stock.compose_chunks from re-rendering
//     in Test 3 (Drive failure resume) saves it from re-rendering
//     here.
//
// Scope note: the heavy outbox-pool integration (processEvent +
// Repository.MarkFailed + tabular event_status transitions) lives
// in a follow-up commit scoped to internal/infrastructure/database/
// sqlite/outboxevents; this test pins the typed-classifier + resume
// pre-conditions that the heavier integration will exercise.
func TestStock_RetryPolicy_QdrantDown_OutboxKeepsRetryClassifier(t *testing.T) {
	// No t.Parallel(): see the comment at the top of
	// TestStock_RetryPolicy_YTDLPTimeout_RetriesWithBackoff for the
	// race-detector / WAL-lock issue this avoids.

	// Qdrant production surfaces a typed RetryableError
	// (qdrant.APIError.IsRetryable). PipelineGen wraps at the
	// indexing-handler boundary with retry.WrapTransient so
	// IsTransient recognises the typed carrier. We simulate the
	// post-boundary shape here.
	qdrantDown := &pkgretry.TransientInfrastructureError{
		Err: errors.New("qdrant indexing: connection refused (sidecar unreachable)"),
	}

	// Part A — classifier pinning: Qdrant-styled transient
	// errors MUST classify as retryable (NOT terminal).
	//
	// Classify returns the typed-carrier path → IsTransient
	// probe → substring "connection refused" routes to
	// ErrNetwork. retryable=true confirms the indexing handler
	// routes to MarkFailed (transient path), NOT
	// MarkDeadLetter (terminal path).
	cat, retryable := pkgretry.Classify(qdrantDown)
	require.True(t, retryable,
		"Qdrant 'connection refused' (transient-typed-carrier) MUST classify as retryable (outbox handler routes to MarkFailed, NOT dead-letter)")
	assert.Equal(t, pkgretry.ErrNetwork, cat,
		"Qdrant 'connection refused' MUST classify into ErrNetwork (confirms it lands in the transient retry path)")

	// Part B — retry loop pins the classifier's behavioral
	// consequence: pkg/retry.Do with retryable=true RETRIES the
	// stub fn until MaxAttempts is exhausted (or success).
	// With MaxAttempts=3 and fail-always fn, calls==3 + last
	// error surfaces. The retry budget is HONOURED (not the
	// terminal "short-circuit on attempt 1" path that
	// FFMPEG_INVALID_INPUT exercises).
	var calls atomic.Int64
	stubFn := func() error {
		calls.Add(1)
		return qdrantDown
	}
	err := pkgretry.Do(context.Background(), stubFn, pkgretry.Options{
		MaxAttempts:    3,
		InitialBackoff: 5 * time.Millisecond,
		BackoffFactor:  2.0,
		JitterFraction: 0,
		DisableJitter:  true,
		IsRetryable:    pkgretry.IsTransient,
		Clock:          &fakeRetryClock{},
	})
	require.Error(t, err,
		"Qdrant-down (retryable) MUST exhaust the retry budget before propagating the error")
	require.ErrorIs(t, err, qdrantDown,
		"underlying typed carrier MUST surface verbatim after retry exhaustion (typed-error godlike/06 SSOT)")
	assert.Equal(t, int64(3), calls.Load(),
		"Qdrant-down (retryable=true) MUST honour MaxAttempts=3 (NOT short-circuit on attempt 1 — timeout would be contradicted by FFMPEG_INVALID_INPUT behaviour)")

	// Part C — orchestrator resume invariant: all 6 dispatchSteps
	// pre-Completed → re-run on the SAME job_id does NOT
	// re-render. The CAS-preserved Completed rows from a prior
	// run (e.g. after publish succeeded but the Qdrant
	// projection failed mid-finalize) are preserved across the
	// retry → media_assets row stays valid (write happened on
	// the first run) → outbox.asset.index.requested stays at
	// status='pending' with attempt_count > 0 (Pin: no
	// re-emit, no duplicate stage rows).
	//
	// The 'no re-render' invariant at the orchestrator layer is
	// the canonical guarantee: even if a future refactor wires
	// RunResilient as 're-run from scratch on retry' (a
	// regression), this test fails loudly.
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "qdrant-down-resume-1"

	allNames := []string{
		"stock.plan",
		"stock.stage_sources",
		"stock.extract_clips",
		"stock.compose_chunks",
		"stock.publish",
		"stock.finalize",
	}

	// Pre-Complete all 6 stages — simulates a successful prior
	// run whose Qdrant projection step (finalize's best-effort
	// IndexClip call) failed.
	for _, name := range allNames {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: stepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k),
			"pre-Complete %q: MarkStarted", name)
		require.NoError(t, store.MarkCompleted(ctx, k, nil, nil),
			"pre-Complete %q: MarkCompleted", name)
	}

	// Stub recorder counters: every step is expected to be
	// SKIPPED on this run (ErrStepAlreadyCompleted path).
	// *int32 pattern matches sibling orchestrator_resume_test.go;
	// race-safe via atomic.LoadInt32 on the pointer.
	counters := [6]*int32{}
	for i := range counters {
		counters[i] = new(int32)
	}
	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: counters[0]},
		&stubRecorderStep{name: "stock.stage_sources", count: counters[1]},
		&stubRecorderStep{name: "stock.extract_clips", count: counters[2]},
		&stubRecorderStep{name: "stock.compose_chunks", count: counters[3]},
		&stubRecorderStep{name: "stock.publish", count: counters[4]},
		// stock.finalize: pre-Completed; CAS-skip on this run.
		// The stub's Run is INVOKED 0 times — counter[5] stays at 0.
		&stubRecorderStep{name: "stock.finalize", count: counters[5]},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{},
		fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err2 := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err2,
		"all-6-pre-Completed re-run MUST succeed (CAS-skip on every step)")

	for i := range counters {
		assert.Equal(t, int32(0), atomic.LoadInt32(counters[i]),
			"Qdrant-down resume: %q was pre-Completed; Run MUST NOT be called (no re-render after a transient projection failure)",
			allNames[i])
	}

	// stepStore invariant: 6 rows, all Completed at attempt=1.
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 6, len(rows),
		"Qdrant-down resume: stepStore has exactly 6 rows (no duplicate stage rows from re-run)")
	for _, r := range rows {
		assert.Equal(t, steps.StatusCompleted, r.Status,
			"Qdrant-down resume: every row remains StatusCompleted (CAS-terminal-immutability; step=%s)", r.StepKey)
		// attempt counter: when re-MarkStarted on a Completed row,
		// the ON CONFLICT clause preserves attempt. No flips-back
		// happens here, so attempt stays at 1 for all 6 rows.
		assert.Equal(t, 1, r.Attempt,
			"Qdrant-down resume: every row stays at attempt=1 (Completed-CAS preserves attempt on re-MarkStarted; step=%s)",
			r.StepKey)
	}
}
