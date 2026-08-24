package jobs

// PR-VO-AUDIT-P06 (June 2026): parent-handler nil-guard test.
//
// Located in the voiceover/jobs sub-package because the test
// exercises unexported sub-package symbols:
//   - voiceover/jobs.FanoutVoiceoversUseCase struct (defined in fanout.go)
//   - voiceover/jobs.NewGenerateJobHandler constructor (defined in generate_handler.go)
//   - voiceover/jobs.GenerateJobHandler.HandleJob method (the P0.6 nil-guard target)
//
// The root-package voiceover cannot import these symbols (the sub-
// package depends on voiceover, not the other way around), so the
// test must live in the same package that owns the types.
//
// ────────────────────────────────────────────────────────────────────────
// AUDIT PIN: pre-P06 the next two logger lines dereferenced
// res.EnqueuedCount + res.FailedEnqueueCount unconditionally when
// FanoutVoiceoversUseCase.Execute returned (nil, err) (cmd==nil,
// cmd.Validate() failure, or panic-recovered paths). The worker
// panicked with "runtime error: invalid memory address or nil
// pointer dereference" on validation-failure paths that hit the
// HTTP layer.
//
// Post-P06 a local var extraction
//   enq, failEnq := 0, 0
//   if res != nil { enq = res.EnqueuedCount; failEnq = ... }
// nil-guards the logger call; toFanoutResultMap is also nil-safe so
// the dispatcher's result-payload contract stays intact on
// validation-failure paths.
// ────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestGenerateJobHandler_NilFanoutResultNoPanic(t *testing.T) {
	// FanoutVoiceoversUseCase is constructed directly via struct
	// literal (&FanoutVoiceoversUseCase{}) — bypasses the
	// NewFanoutVoiceoversUseCase fail-fast constructor (which
	// requires deps.JobsService ≠ nil). Validate() returns err
	// BEFORE deps.JobsService.Enqueue is reached on the empty-
	// Languages test path, so deps can stay zero-value.
	useCase := &FanoutVoiceoversUseCase{}
	h := NewGenerateJobHandler(useCase, zap.NewNop())

	// Empty-Items payload → cmd.Validate() returns err →
	// FanoutUseCase.Execute returns (nil, err) (per fanout.go:113
	// validation guard). Item text non-empty so the validation
	// isolates on Items-empty (per Step 5 invariant: empty Items
	// is the canonical trigger; P0.2's Text-empty check is gone).
	cmd := voiceover.GenerateVoiceoversCommand{
		Items: []voiceover.VoiceoverItem{},
	}
	cmdJSON, _ := json.Marshal(cmd)
	j := &jobs.Job{ID: "test-parent-job", Payload: cmdJSON}

	// The (potential) panic site is the unguarded res.EnqueuedCount
	// dereference in HandleJob's logger.Error path. Pre-P06 this
	// panicked with "runtime error: invalid memory address or nil
	// pointer dereference" — the worker crashed on validation-
	// failure paths and the parent job outcome was neither SUCCEEDED
	// nor FAILED until a human noticed.
	//
	// Post-P06 it gracefully returns the wrapped error and a well-
	// formed partial-failure result map via the nil-safe
	// toFanoutResultMap helper.
	t.Run("validation_failure_propagates_no_panic", func(t *testing.T) {
		result, err := h.HandleJob(context.Background(), j, nil)
		assert.NotNil(t, err,
			"P0.6: Execute returned (nil, err) → HandleJob must propagate the error to the dispatcher's status writer")
		assert.NotNil(t, result,
			"P0.6: nil-res result map must still be a well-formed map (toFanoutResultMap is nil-safe)")
		assert.Equal(t, false, result["ok"],
			"P0.6: ok must be false on Execute failure path")
		assert.Equal(t, "test-parent-job", result["parent_job_id"],
			"P0.6: parent_job_id must round-trip from job.ID even on Execute failure path")
		assert.Equal(t, 0, result["enqueued_count"],
			"P0.6: enqueued_count must be 0 (nil-res default) on Execute failure path")
	})

	// Additional pin: the same code path with a nil Payload (defensive —
	// the dispatcher should not deliver nil, but if it does, the
	// handler must not panic). The json.Unmarshal step returns
	// (nil, fmt.Errorf("...unmarshal payload: ...")) without ever
	// reaching the res nil-deref site, exercising the broader
	// "never panic on bad input" contract.
	t.Run("nil_payload_returns_no_panic", func(t *testing.T) {
		nilPayloadJob := &jobs.Job{ID: "test-parent-job-2", Payload: nil}
		result, err := h.HandleJob(context.Background(), nilPayloadJob, nil)
		assert.NotNil(t, err,
			"P0.6: nil payload → unmarshal fails → the handler must return err (never panic)")
		assert.Nil(t, result,
			"P0.6: on unmarshal-fail path the handler intentionally returns (nil-result, err); partial-failure result-map is only synthesised when FanoutUseCase.Execute was reached at least once")
	})
}
