package jobs

// PR-VO-AUDIT-P05 micro-commit #4 (June 2026) — handler-level
// parent_state emit tests.
//
// Located in the voiceover/jobs sub-package because the test
// exercises the unexported GenerateJobHandler + toFanoutResultMap
// + FanoutVoiceoversUseCase which are package-private.
//
// Test strategy (post-#4 narrow-port refactor): drive HandleJob
// END-TO-END through a stub Enqueuer (Pattern 0 narrow port, the
// same one FanoutVoiceoversUseCase depends on). The heavy
// appjobs.NewService(repo, dispatcher, logger) is NOT constructed
// — the stub Enqueuer satisfies the port with no goroutines, no
// lease machinery, no DB.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubEnqueuer is a minimal Enqueuer implementation: returns a
// pre-configured result for every Enqueue call. Captures the call
// count so tests can assert N children enqueued.
//
// failOnCall (FASE 2 acceptance test, July 2026): when > 0, the stub
// returns returnErr on the N-th call (1-indexed) instead of returnJob.
// Used by TestGenerateJobHandler_PartialFanoutExpectedChildren to
// simulate a partial fan-out (e.g. failOnCall=3 with 3 items → first
// 2 enqueue succeed, 3rd fails).
type stubEnqueuer struct {
	returnJob  *job.Job
	returnErr  error
	failOnCall int // 0 = never fail; N = fail on N-th call (1-indexed)
	callCount  int
	lastReq    *job.EnqueueRequest
	requests   []*job.EnqueueRequest
}

func (s *stubEnqueuer) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.callCount++
	s.lastReq = req
	s.requests = append(s.requests, req)
	if s.returnErr != nil && (s.failOnCall == 0 || s.callCount == s.failOnCall) {
		return nil, s.returnErr
	}
	return s.returnJob, nil
}

// makeValidCmd builds a JSON-marshalled GenerateVoiceoversCommand
// payload that passes cmd.Validate() so HandleJob reaches the
// use case Execute path.
//
// Step 5 (P0.3 items-model recovery, June 2026): the payload
// carries Items[] instead of Text + Languages[]. The two items
// share the same text (allowed under Step 5 — sharing is no longer
// required, but the audit pins stay compatible with the legacy
// shared-text shape so a regression in the Items fan-out path is
// still caught by the 2-call-count assertion below).
func makeValidCmd(t *testing.T) []byte {
	t.Helper()
	cmd := voiceover.GenerateVoiceoversCommand{
		Items: []voiceover.VoiceoverItem{
			{Text: "hello world", Language: "en"},
			{Text: "hello world", Language: "it"},
		},
	}
	payload, err := json.Marshal(cmd)
	require.NoError(t, err)
	return payload
}

// Audit-pinned: full enqueue success → result["parent_state"] ==
// "waiting_children".
//
// Drives HandleJob end-to-end: builds a real FanoutVoiceoversUseCase
// with a stub Enqueuer (no appjobs.NewService construction), a
// real GenerateJobHandler, marshals a valid Command payload, runs
// HandleJob, asserts the result map carries the audit-pinned
// parent_state="waiting_children".
func TestGenerateJobHandler_ParentEntersWaitingChildren(t *testing.T) {
	stub := &stubEnqueuer{
		returnJob: &job.Job{ID: "child-test", Type: job.TypeVoiceoverGenerateItem},
		returnErr: nil,
	}
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
	h := NewGenerateJobHandler(uc, zap.NewNop())

	payload := makeValidCmd(t)
	j := &jobs.Job{ID: "test-parent", Payload: payload}

	result, err := h.HandleJob(context.Background(), j, nil)
	require.NoError(t, err,
		"P0.5: HandleJob full-fanout success must return nil err (dispatcher marks parent SUCCEEDED, NOT FAILED)")
	require.NotNil(t, result,
		"P0.5: HandleJob full-fanout success must return the parent_state-aware result map")

	psRaw, exists := result["parent_state"]
	require.True(t, exists,
		"P0.5: result map MUST carry parent_state key on full-success path (audit-pinned emit missing)")
	ps, ok := psRaw.(string)
	require.True(t, ok,
		"P0.5: parent_state value must be a JSON-string-encoded ParentState")
	assert.Equal(t, "waiting_children", ps,
		"P0.5 audit pin: HandleJob full-enqueue success → parent_state=\"waiting_children\" (NEVER \"succeeded\" in micro-commit #4)")

	// Sanity: stub Enqueuer was called once per item (2 items).
	assert.Equal(t, 2, stub.callCount,
		"P0.5: FanoutUseCase must enqueue one child per item in cmd.Items (Step 5: per-item fan-out)")
}

// Audit-pinned: parent_state is NEVER "succeeded" in micro-commit #4.
// Drives HandleJob end-to-end across all fanout-result branches and
// asserts parent_state never hits "succeeded". The micro-commit #5
// durable aggregator is the SINGLE source of "succeeded" emit.
func TestGenerateJobHandler_DoesNotMarkSucceededBeforeChildrenTerminal(t *testing.T) {
	t.Run("full_fanout_success", func(t *testing.T) {
		stub := &stubEnqueuer{returnJob: &job.Job{ID: "child-success", Type: job.TypeVoiceoverGenerateItem}}
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
		h := NewGenerateJobHandler(uc, zap.NewNop())
		result, _ := h.HandleJob(context.Background(), &jobs.Job{ID: "p1", Payload: makeValidCmd(t)}, nil)
		ps, _ := result["parent_state"].(string)
		assert.NotEqual(t, "succeeded", ps,
			"P0.5 micro-commit #4 invariant: parent_state MUST NEVER be \"succeeded\" on full-fanout success (deferred to #5 aggregator)")
	})

	t.Run("partial_fanout", func(t *testing.T) {
		// Stub returns err on every call → res.OK=false in FanoutUseCase.
		stub := &stubEnqueuer{
			returnErr: errors.New("deterministic enqueue failure"),
		}
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
		h := NewGenerateJobHandler(uc, zap.NewNop())
		result, _ := h.HandleJob(context.Background(), &jobs.Job{ID: "p2", Payload: makeValidCmd(t)}, nil)
		ps, _ := result["parent_state"].(string)
		assert.NotEqual(t, "succeeded", ps,
			"P0.5 micro-commit #4 invariant: parent_state MUST NEVER be \"succeeded\" on partial fanout (enqueue failures = partial, not succeeded)")
	})

	t.Run("validation_failure", func(t *testing.T) {
		// Empty Items → cmd.Validate() returns err → res==nil →
		// toFanoutResultMap(nil, ...) emits parent_state=\"failed\".
		// Step 5 invariant: empty Items is the canonical validation
		// trigger (the P0.2 Text-empty check is gone — Step 5
		// collapsed Text into per-item text).
		stub := &stubEnqueuer{returnJob: &job.Job{ID: "x"}}
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
		h := NewGenerateJobHandler(uc, zap.NewNop())
		cmdEmptyItems := voiceover.GenerateVoiceoversCommand{
			Items: []voiceover.VoiceoverItem{},
		}
		payload, _ := json.Marshal(cmdEmptyItems)
		result, _ := h.HandleJob(context.Background(), &jobs.Job{ID: "p3", Payload: payload}, nil)
		require.NotNil(t, result, "P0.5: toFanoutResultMap is nil-safe — nil-res still returns a map")
		ps, _ := result["parent_state"].(string)
		assert.NotEqual(t, "succeeded", ps,
			"P0.5 micro-commit #4 invariant: parent_state MUST NEVER be \"succeeded\" on validation-failure (emits failed)")
		assert.Equal(t, "failed", ps,
			"P0.5: validation-failure path → parent_state=\"failed\"")
	})
}

// Branch coverage: ALL enqueue attempts fail (FailedEnqueueCount == total)
// → emits "partial_success" (semantically: every child could not be
// enqueued; client may retry). Renamed from
// "PartialFanoutEmitsPartialSuccess" to surface the actual coverage
// (the prior name implied "1-of-2" — but the stub returns err for ALL
// calls, so it's actually 100% failure → 0 enqueued). The audit-pinned
// parent_state value "partial_success" is unchanged on this branch per
// toFanoutResultMap's res.OK=false → partial_success emit.
func TestGenerateJobHandler_AllEnqueueFailsEmitsPartialSuccess(t *testing.T) {
	stub := &stubEnqueuer{returnErr: errors.New("enqueue failed")}
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
	h := NewGenerateJobHandler(uc, zap.NewNop())
	result, err := h.HandleJob(context.Background(), &jobs.Job{ID: "p", Payload: makeValidCmd(t)}, nil)
	require.Error(t, err, "P0.5: all-enqueue-fails must return err → dispatcher marks parent FAILED")
	ps, _ := result["parent_state"].(string)
	assert.Equal(t, "partial_success", ps,
		"P0.5: all-enqueue-fails (res.OK=false, FailedEnqueueCount==total, 0 enqueued) → parent_state=\"partial_success\"")
}

// TestGenerateJobHandler_MixedTextsEnqueueDistinctActiveKeys is the
// Step 5 E2E audit-pin that locks per-item textHash propagation into
// the child ActiveKey. Pre-Step-5 (under the items-collapse), all
// children shared the same text hash because they all derived from
// cmd.Text. Step 5 makes each item carry its own textHash, so two
// items with distinct texts MUST produce distinct child ActiveKeys —
// this test pins the invariant end-to-end through HandleJob.
//
// Without this pin, a future regression that drops per-item textHash
// (e.g. a fanout.go refactor that reverts to a shared hash) would
// silently merge distinct items into the same dedupe key, and the
// broker would deduplicate siblings as if they were retries.
func TestGenerateJobHandler_MixedTextsEnqueueDistinctActiveKeys(t *testing.T) {
	stub := &stubEnqueuer{
		returnJob: &job.Job{ID: "child-x", Type: job.TypeVoiceoverGenerateItem},
		returnErr: nil,
	}
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
	h := NewGenerateJobHandler(uc, zap.NewNop())

	cmd := voiceover.GenerateVoiceoversCommand{
		Items: []voiceover.VoiceoverItem{
			{Text: "Ciao", Language: "it-IT"},
			{Text: "Hello", Language: "en-US"},
		},
	}
	payload, err := json.Marshal(cmd)
	require.NoError(t, err)

	_, err = h.HandleJob(context.Background(), &jobs.Job{ID: "p-mixed", Payload: payload}, nil)
	require.NoError(t, err, "Step 5: mixed-text fan-out must succeed (no validation failure)")

	require.Equal(t, 2, stub.callCount,
		"Step 5: 2 cmd.Items → 2 fan-out children (per-item fan-out, NOT per-language)")
	require.Len(t, stub.requests, 2,
		"Step 5: stub captured 2 EnqueueRequest records for assertion")

	assert.NotEqual(t, stub.requests[0].ActiveKey, stub.requests[1].ActiveKey,
		"Step 5 audit-pin: distinct item texts must produce distinct child ActiveKeys (per-item textHash in the ActiveKey format protects against phantom-retry dedup)")

	for i, req := range stub.requests {
		if !strings.Contains(req.ActiveKey, string(cmd.Items[i].Language)) {
			t.Errorf("Step 5: child[%d] ActiveKey missing language %q (got %q)", i, string(cmd.Items[i].Language), req.ActiveKey)
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// §15.4b Fan-out parziale handler-level test (July 2026):
// 3 items, stub fails on 3rd enqueue → expected_children=2,
// parent_state=partial_success, child_job_ids carries the 2 real
// IDs + 1 empty string.
// ─────────────────────────────────────────────────────────────────

// TestGenerateJobHandler_PartialFanoutExpectedChildren verifies that
// the fan-out handler's toFanoutResultMap accurately reflects partial
// fan-out when the job broker rejects one child enqueue (simulated
// via failOnCall=3, returnErr).
//
// Acceptence criteria (§15 fan-out parziale):
//  1. expected_children = 2 (res.EnqueuedCount, NOT total_outputs=3)
//  2. parent_state = "partial_success" (res.OK=false → partial_success)
//  3. failed_enqueue_count = 1 (the 3rd enqueue failed)
//  4. child_job_ids carries 2 real IDs + 1 empty string
//  5. HandleJob returns (resultMap, err) — err signals the dispatcher
//     to mark the parent FAILED (partial fan-out is a failure at the
//     broker level, even though the result map carries partial_success)
func TestGenerateJobHandler_PartialFanoutExpectedChildren(t *testing.T) {
	stub := &stubEnqueuer{
		returnJob:  &job.Job{ID: "child-test", Type: job.TypeVoiceoverGenerateItem},
		returnErr:  errors.New("broker enqueue rejected: queue full"),
		failOnCall: 3, // fail ONLY on 3rd call (Portuguese), not the first 2
	}
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
	h := NewGenerateJobHandler(uc, zap.NewNop())

	// 3 items: Italian, English, Portuguese — the Portuguese enqueue will fail.
	cmd := voiceover.GenerateVoiceoversCommand{
		RequestID: "vo_partial_test",
		Items: []voiceover.VoiceoverItem{
			{Text: "Ciao mondo", Language: "it-IT", Voice: "it-IT-Elsa"},
			{Text: "Hello world", Language: "en-US", Voice: "en-US-Aria"},
			{Text: "Olá mundo", Language: "pt-BR", Voice: "pt-BR-Francisca"},
		},
	}
	payload, err := json.Marshal(cmd)
	require.NoError(t, err)

	result, err := h.HandleJob(context.Background(), &jobs.Job{ID: "parent-partial", Payload: payload}, nil)

	// Criterion 5: HandleJob must return an error (partial fan-out = broker failure).
	require.Error(t, err,
		"§15.4b handler: partial fan-out must return err → dispatcher marks parent FAILED")
	require.NotNil(t, result,
		"§15.4b handler: toFanoutResultMap is nil-safe — partial fan-out still returns a result map")

	// Criterion 1: expected_children = EnqueuedCount = 2 (not total_outputs=3).
	assert.Equal(t, 2, result["expected_children"],
		"§15.4b handler: expected_children must be 2 (res.EnqueuedCount, not TotalOutputs)")

	// Criterion 2: parent_state = partial_success (res.OK=false → partial_success).
	assert.Equal(t, "partial_success", result["parent_state"],
		"§15.4b handler: parent_state must be 'partial_success' (res.OK=false branch)")

	// Criterion 3: failed_enqueue_count = 1.
	assert.Equal(t, 1, result["failed_enqueue_count"],
		"§15.4b handler: failed_enqueue_count must be 1 (3rd enqueue rejected)")

	// enqueued_count = 2.
	assert.Equal(t, 2, result["enqueued_count"],
		"§15.4b handler: enqueued_count must be 2")

	// total_outputs = 3 (the original request count).
	assert.Equal(t, 3, result["total_outputs"],
		"§15.4b handler: total_outputs must be 3 (original request)")

	// Criterion 4: child_job_ids has 3 entries, 2 real IDs + 1 empty.
	childIDs, ok := result["child_job_ids"].([]string)
	require.True(t, ok, "§15.4b handler: child_job_ids must be []string")
	assert.Len(t, childIDs, 3,
		"§15.4b handler: child_job_ids must have 3 entries (2 real + 1 empty for failed enqueue)")
	assert.NotEmpty(t, childIDs[0],
		"§15.4b handler: child_job_ids[0] must be a real job ID (Italian enqueued)")
	assert.NotEmpty(t, childIDs[1],
		"§15.4b handler: child_job_ids[1] must be a real job ID (English enqueued)")
	assert.Empty(t, childIDs[2],
		"§15.4b handler: child_job_ids[2] must be empty (Portuguese enqueue failed)")

	// Sanity: stub was called exactly 3 times (one per item).
	assert.Equal(t, 3, stub.callCount,
		"§15.4b handler: FanoutUseCase must attempt 3 enqueues (one per item)")

	// per_language carries all 3 languages (including the failed one).
	perLang, ok := result["per_language"].([]string)
	require.True(t, ok, "§15.4b handler: per_language must be []string")
	assert.Len(t, perLang, 3,
		"§15.4b handler: per_language must have 3 entries (all items recorded)")
	assert.Contains(t, perLang, "pt-BR",
		"§15.4b handler: per_language must include the failed Portuguese enqueue")
}
