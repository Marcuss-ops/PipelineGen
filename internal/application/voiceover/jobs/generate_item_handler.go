// Package jobs — generate_item_handler.go (PR-VOICEOVER-PARENT-CHILD-FANOUT, P0.3, June 2026).
//
// Restored after origin/main drift: the cleanup commits removed this
// file even though internal/app/composition.go::NewComposition
// (late-bindings block) still constructs and registers
//
//	childHandler := voiceoverjobs.NewGenerateItemJobHandler(
//	    domains.VoiceoverProcessOne, log)
//	childHandler.Register(jobs.Service)
//
// alongside the parent fan-out. Without this file, the wire smoke
// test at internal/app/voiceover_wiring_test.go fails (child job
// type TypeVoiceoverGenerateItem never sees its handler bound).
//
// This handler is the canonical consumer of voiceover.generate_item
// jobs scheduled by FanoutVoiceoversUseCase (fanout.go). Per
// PR-VOICEOVER-PARENT-CHILD-FANOUT P0.3 invariant: NO goroutines are
// spawned inside the handler; the work delegates 1-a-1 to the
// ProcessOneVoiceoverUseCase.Execute method which forwards to the
// legacy Service.GenerateBatch pipeline.
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
// It holds a typed-port ProcessOneVoiceoverUseCase (Pattern 0) and
// dispatches to its Execute method with the unmarshalled child
// command. NO goroutines are spawned here — sibling dispatch is
// regulated by the worker pool's per-job-type Concurrency field.
type GenerateItemJobHandler struct {
	useCase *voiceover.ProcessOneVoiceoverUseCase
	logger  *zap.Logger
}

// NewGenerateItemJobHandler constructs the handler. useCase is
// MANDATORY (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is OPTIONAL (nil-safe via zap.NewNop()).
func NewGenerateItemJobHandler(useCase *voiceover.ProcessOneVoiceoverUseCase, logger *zap.Logger) *GenerateItemJobHandler {
	if useCase == nil {
		panic("voiceover.Jobs.NewGenerateItemJobHandler: useCase is required (ProcessOneVoiceoverUseCase)")
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
