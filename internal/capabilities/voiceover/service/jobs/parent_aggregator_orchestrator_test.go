// Package jobs — parent_aggregator_orchestrator_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// ORCHESTRATOR test surface (mirror of parent_aggregator.go slim
// orchestrator). Per godlike/06 SSOT (one canonical owner per fact),
// this file is the SOLE canonical owner of the 5 orchestrator-level
// Test funcs that drive the full Tick() pipeline end-to-end:
//
//   - TestAcceptance_HappyPath_ThreeLanguagesAllSucceeded (§15.1)
//   - TestAcceptance_TTSTransientFailure_ChildRetryParentStaysOpen
//     (§15.2 — also pins the §15.2 cache-skip behaviour: retry tick
//     re-queries only the changed child, skipping already-terminal
//     siblings via the previouslyTerminal cache)
//   - TestAcceptance_FanoutParziale_OKFalse_AggregatorHandlesPartialSuccess (§15.4c)
//   - TestAcceptance_PartialFanout_ExpectedChildrenMatchesEnqueued (§15.4b)
//   - TestZeroChildren_AggregatorReturnsParentFailed (FASE 4 close-out,
//     July 2026; zero-children aggregate MUST flip to FAILED, NOT
//     partial_success — the pre-FASE-4 mapping was a dispatch-failure
//     false-positive terminal leak)
//
// These 5 funcs exercise the END-TO-END Tick() pipeline through the
// full aggregator. Targeted tests for sibling concerns live in:
//   - parent_aggregator_aggregate_test.go         (P0.1 false-success gate + §15.4 empty-string filter — 5 funcs)
//   - parent_aggregator_finalize_test.go         (finalizeParent: FAILED flip + PartialSuccess keep + ErrAlreadyTerminalAggregate replay + VersionCASConflictRecovery — 5 funcs)
//   - parent_aggregator_state_machine_test.go    (domainToVoiceoverParentState mapping: REQUIRED-fail short-circuit + OPTIONAL-fail tolerance + Required propagation — 3 funcs)
//   - parent_aggregator_eligibility_test.go       (IsParentAwaitingAggregation gate: waiting_children / succeeded / cancelled — 1 outer + 3 sub-tests)
//   - parent_aggregator_testhelpers_test.go      (shared stubAggregatorJobsService + 7 factory helpers + flipRecord — 0 funcs)
//
// All 5 funcs in this file share the stubAggregatorJobsService +
// factory helpers from parent_aggregator_testhelpers_test.go via
// same-package test visibility. Two pre-existing sibling test files
// (parent_aggregator_read_preference_test.go, finalizer_invariants_test.go)
// continue to use the stub + factory helpers without modification.
//
// godlike/07 minimum-blast-radius: pure code-motion — file rename only.
// The 5 funcs and their setup/assertions are unchanged.
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// §15.1 Happy path: 3 languages, 3 children, all SUCCEEDED
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_HappyPath_ThreeLanguagesAllSucceeded(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-happy",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", "c-pt"}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-pt": {ID: "c-pt", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-happy",
		"§15.1: aggregator must finalise parent when all 3 children are terminal")
	got := stub.flipped["parent-happy"]
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.1: broker status must stay SUCCEEDED when all children succeeded")
	assert.Equal(t, "succeeded", got.result["parent_state"],
		"§15.1: parent_state must be 'succeeded' when all children succeeded")
	assert.Equal(t, 3, got.result["total_children"],
		"§15.1: total_children must be 3")
	assert.Equal(t, 3, got.result["succeeded_count"],
		"§15.1: succeeded_count must be 3")
	assert.Equal(t, 0, got.result["failed_count"],
		"§15.1: failed_count must be 0")
}

// ─────────────────────────────────────────────────────────────────
// §15.2 TTS transient failure: child retry, others unaffected
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_TTSTransientFailure_ChildRetryParentStaysOpen(t *testing.T) {
	// c-it and c-pt are terminal, c-en is still in RETRY_WAIT.
	// Aggregator must skip the parent (not all children terminal).
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-retry",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", "c-pt"}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusRetryWait, Result: makeChildResult(false, "failed", "tts timeout")},
			"c-pt": {ID: "c-pt", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Parent must NOT be finalised — c-en is still in flight.
	assert.Empty(t, stub.flipped,
		"§15.2: aggregator must NOT finalise parent when a child is still RETRY_WAIT")

	// Simulate next tick: c-en has been retried and now SUCCEEDED.
	stub.childJobs["c-en"].Status = job.StatusSucceeded
	stub.childJobs["c-en"].Result = makeChildResult(true, "completed", "")
	// Reset stub.flipped for clean assertion.
	stub.flipped = nil
	// Reset getCalls so we can assert only c-en is re-queried on the retry tick.
	stub.getCalls = nil
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-retry",
		"§15.2: after retry succeeds, aggregator must finalise parent")
	got := stub.flipped["parent-retry"]
	assert.Equal(t, "succeeded", got.result["parent_state"],
		"§15.2: after retry, parent_state must be 'succeeded' (all terminal, all succeeded)")

	// §15.2 code-review assertion #3 (July 2026): on the retry tick,
	// the aggregator must only re-query the child whose status changed
	// (c-en). Already-terminal siblings (c-it, c-pt) are cached in the
	// previouslyTerminal map and skip the Get() call entirely. This
	// prevents N redundant broker round-trips per retry tick where
	// N-1 children were already terminal.
	assert.Len(t, stub.getCalls, 1,
		"§15.2: retry tick must re-query only c-en (1 Get call); c-it and c-pt were already terminal and skipped via previouslyTerminal cache")
	assert.Contains(t, stub.getCalls, "c-en",
		"§15.2: retry tick must re-query c-en (the retried child)")
}

// ─────────────────────────────────────────────────────────────────
// §15.4c Fan-out parziale con ok=false: real partial fan-out where
// some enqueues failed. The parent result has ok=false (res.OK=false
// in toFanoutResultMap), parent_state=partial_success, child_job_ids
// has 2 real children + 1 empty slot. The aggregator must handle this
// identically to the ok=true case — IsAwaitingAggregation only inspects
// parent_state, not ok.
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_FanoutParziale_OKFalse_AggregatorHandlesPartialSuccess(t *testing.T) {
	// 3 languages requested, only 2 enqueued (fan-out parziale).
	// ok=false because res.OK is false when some enqueues fail.
	// c-it SUCCEEDED, c-en FAILED (optional), 3rd child never enqueued.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-okfalse",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeFanoutPartialOkFalseParentResult([]string{"c-it", "c-en", ""}),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: Edge TTS timeout"),
				Payload: makeChildPayloadWithRequired(false),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Criterion 1: aggregator MUST still finalise the parent even with ok=false.
	// IsAwaitingAggregation() inspects parent_state, not ok — both ok=true and
	// ok=false with parent_state=partial_success reach the aggregation loop.
	require.Contains(t, stub.flipped, "parent-okfalse",
		"§15.4c: ok=false parent with parent_state=partial_success must still be finalised by aggregator")

	got := stub.flipped["parent-okfalse"]

	// Criterion 2: broker status stays SUCCEEDED (partial_success is not terminal failure).
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.4c: ok=false + partial_success → broker status stays SUCCEEDED")

	// Criterion 3: parent_state remains partial_success (one succeeded, one optional-failed).
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"§15.4c: one succeeded + one optional-failed → parent_state='partial_success'")

	// Criterion 4: total_children = 2 (empty string filtered).
	assert.Equal(t, 2, got.result["total_children"],
		"§15.4c: total_children=2 (empty string filtered out)")

	// Criterion 5: succeeded_count=1, failed_count=1.
	assert.Equal(t, 1, got.result["succeeded_count"],
		"§15.4c: 1 child succeeded (c-it)")
	assert.Equal(t, 1, got.result["failed_count"],
		"§15.4c: 1 child failed (c-en, optional TTS failure)")

	// Criterion 6: no error message on partial_success flip.
	assert.Empty(t, got.errMsg,
		"§15.4c: partial_success flip has no error message")
}

// ─────────────────────────────────────────────────────────────────
// §15.4b Fan-out parziale con accurate handler simulation:
// 3 items richiesti, 2 enqueued, expected_children=2, parent_state=partial_success.
// One child optional-failed, one child succeeded → FinalizeAggregateParent with
// parent_state=partial_success, broker status SUCCEEDED.
// ─────────────────────────────────────────────────────────────────

// TestAcceptance_PartialFanout_ExpectedChildrenMatchesEnqueued verifies
// the complete acceptance criteria for partial fan-out (§15 fan-out parziale):
//
//  1. expected_children = 2 (the handler writes res.EnqueuedCount, NOT total_outputs)
//  2. parent_state = "partial_success" (res.OK=false → partial_success in toFanoutResultMap)
//  3. FinalizeAggregateParent is called correctly — broker status stays SUCCEEDED
//     (partial_success is not a terminal failure), parent_state emits "partial_success"
//     in the finalised result.
func TestAcceptance_PartialFanout_ExpectedChildrenMatchesEnqueued(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:       "parent-partial-accurate",
			Type:     job.TypeVoiceoverGenerate,
			Status:   job.StatusSucceeded,
			Result:   makePartialFanoutParentResult([]string{"c-it", "c-en", ""}),
			Revision: 7,
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: Deepgram connection timeout"),
				Payload: makeChildPayloadWithRequired(false), // OPTIONAL
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Criterion 1: aggregator must finalise the parent (FinalizeAggregateParent called).
	require.Contains(t, stub.flipped, "parent-partial-accurate",
		"§15.4b: aggregator must call FinalizeAggregateParent when all enqueued children are terminal")

	got := stub.flipped["parent-partial-accurate"]

	// Criterion 2: broker status stays SUCCEEDED (partial_success ≠ terminal failure).
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.4b: partial_success → broker status stays SUCCEEDED (no false-FAILED flip)")

	// Criterion 3: parent_state = "partial_success" (one succeeded, one optional-failed).
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"§15.4b: one succeeded + one optional-failed → parent_state='partial_success'")

	// Criterion 4: total_children = 2 (empty string filtered out).
	assert.Equal(t, 2, got.result["total_children"],
		"§15.4b: total_children=2 (empty string for failed enqueue filtered out)")

	// Criterion 5: succeeded_count=1, failed_count=1.
	assert.Equal(t, 1, got.result["succeeded_count"],
		"§15.4b: 1 child succeeded (c-it)")
	assert.Equal(t, 1, got.result["failed_count"],
		"§15.4b: 1 child failed (c-en, optional TTS failure)")

	// Criterion 6: no REQUIRED failures → required_failed_count=0.
	assert.Equal(t, 0, got.result["required_failed_count"],
		"§15.4b: optional-only failures → required_failed_count=0")

	// Criterion 7: version CAS guard — StateMachineVersion = j.Revision (7).
	assert.Equal(t, 7, got.result["_aggregator_version"],
		"§15.4b: StateMachineVersion must match j.Revision (7) for CAS guard")

	// Criterion 8: no error message on partial_success flip.
	assert.Empty(t, got.errMsg,
		"§15.4b: partial_success flip has no error message")
}

// ─────────────────────────────────────────────────────────────────
// FASE 4 close-out (July 2026): zero children → ParentFailed
// ─────────────────────────────────────────────────────────────────

// TestZeroChildren_AggregatorReturnsParentFailed pins the FASE 4
// sub-task 4 spec close-out for the voiceover aggregator: when a
// parent has zero enqueued children (e.g. all child enqueues
// failed at dispatch time), the aggregator MUST finalise the
// parent as ParentFailed (not ParentPartialSuccess). The pre-FASE-4
// mapping (ParentPartialSuccess) conflated dispatch failure
// (zero enqueued) with partial-success (mixed terminal) — two
// semantically distinct states. The pre-FASE-4 mapping was a
// false-positive terminal leak that masked dispatch failures in
// the operator dashboard. FASE 4 spec mandates ParentFailed (the
// canonical "all children definitively failed" terminal per
// parent_state.go:62 + 78).
func TestZeroChildren_AggregatorReturnsParentFailed(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-zero-fase4",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeParentResult([]string{}), // ZERO children enqueued
		},
		childJobs: map[string]*job.Job{}, // no children at all
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// The aggregator must have called FinalizeAggregateParent (not
	// silently skipped). The contract is "terminal flip on zero
	// children", NOT "no-op on zero children".
	require.Contains(t, stub.flipped, "parent-zero-fase4",
		"FASE 4: aggregator must call FinalizeAggregateParent on zero-children aggregate")

	got := stub.flipped["parent-zero-fase4"]

	// Assert 1: targetStatus = StatusFailed (not StatusSucceeded).
	assert.Equal(t, job.StatusFailed, got.targetStatus,
		"FASE 4: zero-children aggregate MUST flip broker-status to FAILED (not SUCCEEDED via partial_success)")

	// Assert 2: parent_state in result = "failed" (not "partial_success").
	ps, _ := got.result["parent_state"].(string)
	assert.Equal(t, "failed", ps,
		"FASE 4: zero-children aggregate MUST emit parent_state=%q in result (not %q)",
		"failed", "partial_success")

	// Assert 3: errMsg is non-empty (operator forensic marker).
	assert.NotEmpty(t, got.errMsg,
		"FASE 4: zero-children FAILED flip must carry a non-empty errMsg (audit forensics)")

	// Assert 4: total_children = 0.
	assert.Equal(t, 0, got.result["total_children"],
		"FASE 4: zero-children aggregate must report total_children=0 in result")
}
