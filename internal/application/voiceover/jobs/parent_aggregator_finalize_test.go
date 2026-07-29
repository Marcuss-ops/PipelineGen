// Package jobs — parent_aggregator_finalize_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// FINALIZE test surface (mirror of parent_aggregator_finalize.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the 5 Test funcs
// that exercise the per-parent finalizeParent pipeline:
//
//   - TestParentBrokerStatusIsFAILEDWhenAllChildrenFailed
//     (broker-status flip to FAILED when aggregate is ParentFailed)
//
//   - TestParentBrokerStatusSUCCEEDEDWhenAggregateIsPartialSuccess
//     (broker-status stays SUCCEEDED when aggregate is PartialSuccess)
//
//   - TestFinalizeAggregateParentReplayIsIdempotent_NoOp
//     (ErrAlreadyTerminalAggregate branch: info-log + early return)
//
//   - TestAcceptance_DBSucceededWorkerCrash_IdempotentRetry
//     (worker crashed post-flip: retry tick skipped via
//     IsParentAwaitingAggregation gate)
//
//   - TestAcceptance_AggregatorCrash_VersionCASConflictRecovery
//     (unexpected FinalizeAggregateParent error: warn-log + early return)
//
// All test funcs target the FINALIZE production code only
// (parent_aggregator_finalize.go::finalizeParent). They share the
// stubAggregatorJobsService + flipRecord + factory helpers from
// parent_aggregator_testhelpers_test.go via same-package visibility.
// godlike/07 minimum-blast-radius: pure code-motion, no logic change.
package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// FASE 2 + ParentFailed branch: both required children failed →
// broker-status MUST flip to FAILED + parent_state="failed" +
// errMsg non-empty.
// ─────────────────────────────────────────────────────────────────

// TestParentBrokerStatusIsFAILEDWhenAllChildrenFailed pins the
// finalizeParent FAILED-flip branch (parent_aggregator_finalize.go:44-45):
// when VoiceoverAggregateResult.ParentState == voiceover.ParentFailed,
// targetStatus MUST be job.StatusFailed and errMsg MUST be non-empty
// (operator forensic marker). The 2 REQUIRED-failed children drive
// the StateMachine to FailedTerminal via the REQUIRED-fail
// short-circuit in parent_aggregator_aggregate.go::Transition.
func TestParentBrokerStatusIsFAILEDWhenAllChildrenFailed(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-aggregate-failed",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en"}, 2, 0, "waiting_children"),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: Edge TTS connection timeout"),
				Payload: makeChildPayloadWithRequired(true), // REQUIRED
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "upload_failed: Drive timeout"),
				Payload: makeChildPayloadWithRequired(true), // REQUIRED
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// Assert 1: the aggregator must have called FinalizeAggregateParent.
	require.Contains(t, stub.flipped, "parent-aggregate-failed",
		"FASE 2: aggregator must emit a flip when all required children failed")

	got := stub.flipped["parent-aggregate-failed"]

	// Assert 2: targetStatus flipped to FAILED.
	assert.Equal(t, job.StatusFailed, got.targetStatus,
		"FASE 2 + ParentFailed branch: broker-status MUST flip to FAILED when aggregate is ParentFailed")

	// Assert 3: parent_state == "failed".
	assert.Equal(t, "failed", got.result["parent_state"],
		"FASE 2 + ParentFailed branch: parent_state MUST be 'failed' when all required children failed")

	// Assert 4: errMsg non-empty (operator forensic marker per FASE 2).
	assert.NotEmpty(t, got.errMsg,
		"FASE 2 + ParentFailed branch: errMsg MUST be non-empty (audit forensics — see parent_aggregator_finalize.go:44)")

	// Assert 5: back-compat mirror — completed map has _target_status=FAILED
	// for legacy P0.1-gate tests that read the JSON mirror.
	stubCompleted, ok := stub.completed["parent-aggregate-failed"]
	require.True(t, ok, "FASE 2: stub.completed mirror MUST be populated")
	assert.Equal(t, string(job.StatusFailed), stubCompleted["_target_status"],
		"FASE 2: stub.completed back-compat _target_status MUST mirror to FAILED")
}

// ─────────────────────────────────────────────────────────────────
// FASE 2 + ParentPartialSuccess branch: 1 required-success + 1
// optional-failed → broker-status stays SUCCEEDED (no false flip).
// ─────────────────────────────────────────────────────────────────

// TestParentBrokerStatusSUCCEEDEDWhenAggregateIsPartialSuccess
// pins the finalizeParent SUCCEEDED-stays branch
// (parent_aggregator_finalize.go:42-46): when
// VoiceoverAggregateResult.ParentState == voiceover.ParentPartialSuccess,
// targetStatus MUST remain job.StatusSucceeded (NOT flip to FAILED)
// and errMsg MUST be empty. The OPTIONAL-failed child tolerates
// partial-success per FASE 2 wire-shape semantics.
func TestParentBrokerStatusSUCCEEDEDWhenAggregateIsPartialSuccess(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-aggregate-partial",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en"}, 2, 0, "waiting_children"),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(true), // REQUIRED
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: optional language not critical"),
				Payload: makeChildPayloadWithRequired(false), // OPTIONAL
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// Assert 1: aggregator emitted a flip (finalizeParent was reached).
	require.Contains(t, stub.flipped, "parent-aggregate-partial",
		"FASE 2: aggregator must emit a flip when partial-success terminal")

	got := stub.flipped["parent-aggregate-partial"]

	// Assert 2: targetStatus stays SUCCEEDED (no false-FAILED flip).
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"FASE 2 + ParentPartialSuccess branch: broker-status MUST stay SUCCEEDED (partial_success is not a terminal failure)")

	// Assert 3: parent_state == "partial_success".
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"FASE 2: parent_state MUST be 'partial_success' when 1 succeeded + 1 optional-failed")

	// Assert 4: errMsg empty (no operator forensic message on partial-success).
	assert.Empty(t, got.errMsg,
		"FASE 2 + ParentPartialSuccess branch: errMsg MUST be empty on partial_success (audit forensics only on FAILED)")

	// Assert 5: counter counts are correct.
	assert.Equal(t, 2, got.result["total_children"],
		"FASE 2: total_children=2 (both enqueued)")
	assert.Equal(t, 1, got.result["succeeded_count"],
		"FASE 2: succeeded_count=1 (c-it)")
	assert.Equal(t, 1, got.result["failed_count"],
		"FASE 2: failed_count=1 (c-en, optional)")
	assert.Equal(t, 0, got.result["required_failed_count"],
		"FASE 2: required_failed_count=0 (only OPTIONAL failed)")
}

// ─────────────────────────────────────────────────────────────────
// P0 #1 idempotency: ErrAlreadyTerminalAggregate → info-log early return.
// The stub's flippedErr knob returns the typed error WITHOUT mutating
// flipped + completed maps (no-op SQL update parimody).
// ─────────────────────────────────────────────────────────────────

// TestFinalizeAggregateParentReplayIsIdempotent_NoOp pins
// finalizeParent's ErrAlreadyTerminalAggregate branch
// (parent_aggregator_finalize.go:53-58): when the broker signals
// the parent is already terminal (replay in flight from a crashed
// worker / second tick trying the same aggregate), the aggregator
// MUST log Info and return early WITHOUT mutating its own state.
// godlike/07: no panic, no spurious Update, no second flip recorded.
func TestFinalizeAggregateParentReplayIsIdempotent_NoOp(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-replay-noop",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it"}, 1, 0, "waiting_children"),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(true),
			},
		},
		// Simulate "parent already finalized" at the SQL layer —
		// the broker's FinalizeAggregateParent returns the typed
		// ErrAlreadyTerminalAggregate on replay.
		flippedErr: domainremote.ErrAlreadyTerminalAggregate,
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// Assert 1: stub.flipped must be empty (replay-no-op: no flip recorded).
	assert.Empty(t, stub.flipped,
		"P0 #1 idempotency: ErrAlreadyTerminalAggregate MUST NOT mutate stub.flipped (replay-no-op semantics)")

	// Assert 2: stub.completed must be empty too (no successful mirror write).
	assert.Empty(t, stub.completed,
		"P0 #1 idempotency: ErrAlreadyTerminalAggregate MUST NOT mutate stub.completed (no-op SQL update parimody)")

	// Assert 3: no panic — test completes without crashing.
	t.Log("P0 #1 idempotency: ErrAlreadyTerminalAggregate handled gracefully (info-log early return)")
}

// ─────────────────────────────────────────────────────────────────
// §15 P1 retry-worker: parent already terminal in DB → retry worker's
// Tick MUST skip via IsParentAwaitingAggregation gate (no double-flip).
// ─────────────────────────────────────────────────────────────────

// TestAcceptance_DBSucceededWorkerCrash_IdempotentRetry pins the
// §15 retry-worker acceptance criterion: if the worker crashes
// AFTER successful FinalizeAggregateParent, the retry worker's first
// Tick must NOT re-finalize the parent. The parent_state in the result
// JSON is already a terminal value ("succeeded"); IsParentAwaitingAggregation
// returns false; aggregateOne returns early at line ~143.
func TestAcceptance_DBSucceededWorkerCrash_IdempotentRetry(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-already-terminal",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			// parent_state already terminal in JSON result after prior flip.
			Result: makeMultiChildParentResult([]string{"c-it", "c-en"}, 2, 0, "succeeded"),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// Assert 1: retry worker must NOT re-finalize (no second flip).
	assert.Empty(t, stub.flipped,
		"§15 retry-worker: parent already terminal → IsParentAwaitingAggregation gate MUST skip (no double-flip)")

	// Assert 2: retry worker must NOT call Complete either.
	assert.Empty(t, stub.completed,
		"§15 retry-worker: parent already terminal → aggregator MUST NOT call Complete (no spurious write)")
}

// ─────────────────────────────────────────────────────────────────
// §15 P2 crash-recovery: aggregator crashed mid-flip; next tick's
// FinalizeAggregateParent returns a non-replay error → aggregator
// MUST warn-log + early return (no panic, no infinite loop).
// ─────────────────────────────────────────────────────────────────

// TestAcceptance_AggregatorCrash_VersionCASConflictRecovery pins the
// §15 P2 crash-recovery acceptance criterion: when the aggregator's
// finalizeParent is interrupted (e.g., mid-transaction crash, manual
// retry, status revoked by operator), the next worker's Tick may
// hit a non-replay error (ErrAggregateCASConflict or any other
// non-ErrAlreadyTerminalAggregate SQL error). The aggregator MUST
// warn-log + early-return WITHOUT panicking. godlike/07 fail-closed:
// the aggregator must NOT silently retry indefinitely; the operator
// must surface the warning.
func TestAcceptance_AggregatorCrash_VersionCASConflictRecovery(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-cas-conflict",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it"}, 1, 0, "waiting_children"),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(true),
			},
		},
		// Simulate non-replay FinalizeAggregateParent error (CAS conflict
		// OR manual-retry status-revoked error). Use a non-typed error to
		// exercise the WARN branch (NOT the ErrAlreadyTerminalAggregate
		// info-log replay no-op branch).
		flippedErr: errors.New("sql: aggregate CAS conflict (revision bumped by concurrent writer)"),
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// Assert 1: stub.flipped MUST be empty (non-replay error: no flip recorded).
	assert.Empty(t, stub.flipped,
		"§15 P2 crash-recovery: non-replay FinalizeAggregateParent error MUST NOT mutate stub.flipped (warn-log early return)")

	// Assert 2: stub.completed MUST be empty too.
	assert.Empty(t, stub.completed,
		"§15 P2 crash-recovery: non-replay error MUST NOT mutate stub.completed (no SQL write attempted)")

	// Assert 3: no panic — test completes without crashing.
	t.Log("§15 P2 crash-recovery: non-replay FinalizeAggregateParent error handled gracefully (warn-log early return)")
}
