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
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubEnqueuer is a minimal Enqueuer implementation: returns a
// pre-configured result for every Enqueue call. Captures the call
// count so tests can assert N children enqueued.
type stubEnqueuer struct {
	returnJob *job.Job
	returnErr error
	callCount int
	lastReq   *job.EnqueueRequest
}

func (s *stubEnqueuer) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.callCount++
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return s.returnJob, nil
}

// makeValidCmd builds a JSON-marshalled GenerateVoiceoversCommand
// payload that passes cmd.Validate() so HandleJob reaches the
// use case Execute path.
func makeValidCmd(t *testing.T) []byte {
	t.Helper()
	cmd := voiceover.GenerateVoiceoversCommand{
		Text:      "hello world",
		Languages: []string{"en", "it"},
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

	// Sanity: stub Enqueuer was called once per language (2 languages).
	assert.Equal(t, 2, stub.callCount,
		"P0.5: FanoutUseCase must enqueue one child per language in cmd.Languages")
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
		// Empty Languages → cmd.Validate() returns err → res==nil →
		// toFanoutResultMap(nil, ...) emits parent_state=\"failed\".
		stub := &stubEnqueuer{returnJob: &job.Job{ID: "x"}}
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{Enqueuer: stub, Logger: zap.NewNop()})
		h := NewGenerateJobHandler(uc, zap.NewNop())
		cmdEmptyLangs := voiceover.GenerateVoiceoversCommand{Text: "hi", Languages: []string{}}
		payload, _ := json.Marshal(cmdEmptyLangs)
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
