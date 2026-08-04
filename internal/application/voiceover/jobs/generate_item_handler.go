// Package jobs — generate_item_handler.go (BLOC5.3 commit-2-child-canonical, June 2026).
//
// Child handler for voiceover.generate_item jobs scheduled by
// FanoutVoiceoversUseCase (fanout.go). Reaches the canonical
// 7-port pipeline via the narrow VoiceoverItemExecutor interface
// (port abstraction layer, AGENTS.md Pattern 0) rather than the
// legacy ProcessOneVoiceoverUseCase bridge (which converted
// GenerateVoiceoverItemCommand → BatchRequest → Service.GenerateBatch).
//
// BLOC5.3 audit-pin invariants (P0.6 — pass-through, no recalc):
//   - item.TextHash, item.Voice, item.Filename, item.RequestID,
//     item.ParentJobID are all pre-populated by fanout; Execute
//     trusts them verbatim. NO re-derivation in the child handler.
//   - the handler dispatches useCase.Execute(ctx, &item) directly
//     with no BatchRequest conversion at any layer.
//
// NO goroutines are spawned inside the handler; sibling dispatch is
// regulated by the worker pool's per-job-type Concurrency field.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"go.uber.org/zap"
)

// GenerateItemJobHandler is the canonical per-language child handler
// for voiceover.generate_item jobs (job.TypeVoiceoverGenerateItem).
// The useCase field is typed as the narrow VoiceoverItemExecutor port
// (Pattern 0 — AGENTS.md) so test fixtures can inject a recording
// stub without instantiating the full 7-port ProcessVoiceoverItemUseCase.
// Production wires *ProcessVoiceoverItemUseCase (concrete satisfies
// the port via Go implicit interface rules; compile-time assertion
// in process_voiceover_item.go pins the conformance).
type GenerateItemJobHandler struct {
	useCase voiceover.VoiceoverItemExecutor
	logger  *zap.Logger
}

// NewGenerateItemJobHandler constructs the handler. useCase is
// MANDATORY (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is OPTIONAL (nil-safe via zap.NewNop()).
func NewGenerateItemJobHandler(useCase voiceover.VoiceoverItemExecutor, logger *zap.Logger) *GenerateItemJobHandler {
	if useCase == nil {
		panic("voiceover.Jobs.NewGenerateItemJobHandler: useCase is required (VoiceoverItemExecutor port)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GenerateItemJobHandler{
		useCase: useCase,
		logger:  logger,
	}
}

// Register binds the handler to the canonical jobs.Service dispatcher
// for the voiceover.generate_item job type. Idempotent via the
// dispatcher double-Register protection.
//
// Audit P0 #2 (July 2026): Register now returns error so the
// composition root can fail-closed at boot if the dispatcher rejects
// the binding. Mirrors GenerateJobHandler.Register's signature
// change — the parent-child handler pair shares the same fail-fast
// contract so a missing child registration (e.g. via a future
// migration that splits BuildDomainBundle) crashes NewComposition
// loudly rather than silently dropping per-language jobs onto an
// unsigned dispatcher.
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs.
func (h *GenerateItemJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("GenerateItemJobHandler.Register: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeVoiceoverGenerateItem, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("GenerateItemJobHandler.Register: bind %q to dispatcher: %w",
			appjobs.TypeVoiceoverGenerateItem, err)
	}
	if h.logger != nil {
		h.logger.Info("registered voiceover.generate_item handler",
			zap.String("job_type", appjobs.TypeVoiceoverGenerateItem))
	}
	return nil
}

// HandleJob processes a voiceover.generate_item child job from the
// queue. Dispatch contract (P0.3, godlike/07 — no fake availability):
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - item.Validate failure → (nil, err): dispatcher marks job FAILED.
//   - useCase.Execute failure → (resultMap, err): dispatcher marks
//     job FAILED. The resultMap carries the per-language status so
//     operators see exactly which sibling failed + why.
//   - useCase.Execute returns (res, nil) with res.Status != StatusCompleted
//     (P0.1 false-success fix, July 2026): the per-item pipeline
//     returned a failed result without a Go error (e.g. TTS failed,
//     upload timeout, DB commit error). The handler MUST surface this
//     as a dispatcher error so the worker retries or marks FAILED —
//     returning (resultMap, nil) here would produce a silent
//     false-success at the job layer (the canonical audit P0.1 bug).
//   - useCase.Execute success → (resultMap, nil): dispatcher marks
//     job SUCCEEDED.
//
// Progress: tools.Progress is called once at start (5%) and once at
// end (100%). Inter-language progress is reported by the parent's
// FanoutUseCase aggregate; this child handler is one slice of that
// aggregate.
func (h *GenerateItemJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling voiceover.generate_item job",
		zap.String("job_id", j.ID))

	// job-tools.Progress nil-safe wrapper — canonical for all 3 handlers
	// (voiceover.generate, voiceover.generate_item, script.generate). The
	// Creator-runtime wrap path passes tools=nil; the SafeProgressFn
	// utility captures the canonical nil-tolerance gate so consumer
	// sites can call pf(...) directly without per-call nil checks.
	pf := appjobs.SafeProgressFn(tools)

	pf(5, "starting voiceover.generate_item")

	var item voiceover.GenerateVoiceoverItemCommand
	if err := json.Unmarshal(j.Payload, &item); err != nil {
		return nil, fmt.Errorf("voiceover.generate_item: unmarshal payload: %w", err)
	}

	if err := item.Validate(); err != nil {
		pf(100, "voiceover.generate_item validation failed")
		return nil, fmt.Errorf("voiceover.generate_item: validate: %w", err)
	}

	res, err := h.useCase.Execute(ctx, &item)
	if err != nil {
		// P0.1 Fase 1b (July 2026): check whether the error is a
		// PipelineError with Retryable flag. Log it so operators can
		// grep for "pipeline_error stage=<stage> retryable=<bool>".
		var pipelineErr *voiceover.PipelineError
		isRetryable := true // default: unknown errors are retryable (fail-safe)
		if errors.As(err, &pipelineErr) {
			isRetryable = pipelineErr.Retryable
		}
		h.logger.Error("voiceover.generate_item execution failure",
			zap.String("job_id", j.ID),
			zap.String("language", string(item.Language)),
			zap.Bool("retryable", isRetryable),
			zap.Error(err))
		pf(100, "voiceover.generate_item execution failed")
		return toItemResultMap(res, &item, j.ID), fmt.Errorf("voiceover.generate_item: execute: %w", err)
	}

	// P0.1 false-success fix (July 2026): ProcessVoiceoverItemUseCase
	// returns (result, nil) even when a stage fails (TTS, upload, DB).
	// The result carries Status=StatusFailed + Error=... but err==nil.
	// The handler MUST surface this as a dispatcher error so the worker
	// retries or marks FAILED. Returning (resultMap, nil) here produces
	// a silent false-success: the broker marks the job SUCCEEDED, the
	// parent aggregator sees SUCCEEDED, the user never knows the
	// voiceover was never created. The check below closes this gap:
	// when the result exists and its Status is not completed, the
	// handler returns an error (with the result map so operators see
	// which stage failed + the per-language Error string).
	if res != nil && res.Status != voiceover.StatusCompleted {
		errMsg := fmt.Sprintf("voiceover.generate_item: %s", res.Error)
		if res.Error == "" {
			errMsg = "voiceover.generate_item: pipeline returned non-completed status"
		}
		h.logger.Error("voiceover.generate_item pipeline failure (P0.1 false-success gate)",
			zap.String("job_id", j.ID),
			zap.String("language", string(item.Language)),
			zap.String("status", string(res.Status)),
			zap.String("error", res.Error))
		pf(100, "voiceover.generate_item pipeline failed: "+string(res.Status))
		return toItemResultMap(res, &item, j.ID), fmt.Errorf("%s", errMsg)
	}

	pf(100, "voiceover.generate_item execution complete")
	return toItemResultMap(res, &item, j.ID), nil
}

// toItemResultMap serialises a per-language VoiceoverItemResult into
// the map[string]any shape the dispatcher writes into job.Result JSON.
// Only the fields the canonical VoiceoverItemResult struct exposes
// (result.go) are surfaced — operators see exactly which sibling
// child landed and what its terminal state + URLs are.
func toItemResultMap(res *voiceover.VoiceoverItemResult, item *voiceover.GenerateVoiceoverItemCommand, childJobID string) map[string]any {
	m := map[string]any{
		"job_id":        childJobID,
		"parent_job_id": item.ParentJobID,
		"request_id":    item.RequestID,
	}
	if res == nil {
		m["language"] = item.Language
		m["status"] = voiceover.StatusFailed
		m["ok"] = false
		return m
	}
	m["language"] = res.Language
	m["status"] = res.Status
	m["ok"] = res.Status == voiceover.StatusCompleted
	if res.Voice != "" {
		m["voice"] = res.Voice
	}
	if res.DriveLink != "" {
		m["drive_link"] = res.DriveLink
	}
	if res.DriveFileID != "" {
		m["drive_file_id"] = res.DriveFileID
	}
	if res.LocalPath != "" {
		m["local_path"] = res.LocalPath
	}
	if res.Error != "" {
		m["error"] = res.Error
	}
	if res.ErrorCode != "" {
		m["error_code"] = res.ErrorCode
	}
	return m
}
