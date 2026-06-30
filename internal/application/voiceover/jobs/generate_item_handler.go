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
func (h *GenerateItemJobHandler) Register(jobsSvc *appjobs.Service) {
	if jobsSvc == nil {
		h.logger.Warn("GenerateItemJobHandler.Register: jobsSvc is nil; handler not bound to dispatcher")
		return
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeVoiceoverGenerateItem, h.HandleJob); err != nil {
		h.logger.Error("GenerateItemJobHandler.Register: RegisterHandler failed",
			zap.String("job_type", appjobs.TypeVoiceoverGenerateItem),
			zap.Error(err))
		return
	}
	h.logger.Info("registered voiceover.generate_item handler",
		zap.String("job_type", appjobs.TypeVoiceoverGenerateItem))
}

// HandleJob processes a voiceover.generate_item child job from the
// queue. Dispatch contract (P0.3, godlike/07 — no fake availability):
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - item.Validate failure → (nil, err): dispatcher marks job FAILED.
//   - useCase.Execute failure → (resultMap, err): dispatcher marks
//     job FAILED. The resultMap carries the per-language status so
//     operators see exactly which sibling failed + why.
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

	if h.hasProgress(tools) {
		tools.Progress(5, "starting voiceover.generate_item")
	}

	var item voiceover.GenerateVoiceoverItemCommand
	if err := json.Unmarshal(j.Payload, &item); err != nil {
		return nil, fmt.Errorf("voiceover.generate_item: unmarshal payload: %w", err)
	}

	if err := item.Validate(); err != nil {
		if h.hasProgress(tools) {
			tools.Progress(100, "voiceover.generate_item validation failed")
		}
		return nil, fmt.Errorf("voiceover.generate_item: validate: %w", err)
	}

	res, err := h.useCase.Execute(ctx, &item)
	if err != nil {
		h.logger.Error("voiceover.generate_item execution failure",
			zap.String("job_id", j.ID),
			zap.String("language", item.Language),
			zap.Error(err))
		if h.hasProgress(tools) {
			tools.Progress(100, "voiceover.generate_item execution failed")
		}
		return toItemResultMap(res, &item, j.ID), fmt.Errorf("voiceover.generate_item: execute: %w", err)
	}

	if h.hasProgress(tools) {
		tools.Progress(100, "voiceover.generate_item execution complete")
	}
	return toItemResultMap(res, &item, j.ID), nil
}

// hasProgress is the nil-safe guard for the JobTools Progress callback.
func (h *GenerateItemJobHandler) hasProgress(tools *appjobs.JobTools) bool {
	return tools != nil && tools.Progress != nil
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
	return m
}
