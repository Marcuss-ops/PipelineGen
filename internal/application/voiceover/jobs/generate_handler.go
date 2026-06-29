// Package jobs is the per-job-type handler subpackage for the voiceover
// capability. It hosts the dedicated handler for the new single
// voiceover.generate job type (Blocco 4 EXPAND, June 2026): one job
// per request, dispatched through the typed-port
// GenerateVoiceoversUseCase (Ports-only dependency per AGENTS.md
// Pattern 0).
//
// godlike/07 EXPAND phase — this commit extends the system without
// removing the legacy voiceover.batch + voiceover.promo registrations
// wired in internal/application/voiceover/service.go at lines
// 132-133. CUTOVER (B-3) flips call sites to enqueue voiceover.generate
// instead; CONTRACT (B-4) removes back-compat aliases.
//
// Subpackage placement rationale: voiceover/jobs/ imports voiceover
// (the canonical use case) but voiceover does NOT import voiceover/jobs/
// (composition root handles the dependency injection). No circular
// dependency risk.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"go.uber.org/zap"
)

// GenerateJobHandler is the dedicated handler for the
// voiceover.generate single job type. Holds the
// *voiceover.GenerateVoiceoversUseCase and dispatches ONLY to its
// Execute(ctx, cmd) method.
//
// Why a dedicated handler (vs extending the legacy
// voiceover.Service.HandleJob switch): the new GenerateVoiceoversUseCase
// is a typed-port use case (Blocco 2) — its handler must NOT carry the
// legacy Service's per-type switch semantics. Sibling handlers
// (voiceover.batch, voiceover.promo) remain on the legacy path until
// the CUTOVER step removes them.
type GenerateJobHandler struct {
	useCase *voiceover.GenerateVoiceoversUseCase
	logger  *zap.Logger
}

// NewGenerateJobHandler constructs the handler. logger is nil-safe
// via zap.NewNop() (mirrors the voiceover use case constructor
// convention).
func NewGenerateJobHandler(useCase *voiceover.GenerateVoiceoversUseCase, logger *zap.Logger) *GenerateJobHandler {
	if useCase == nil {
		panic("voiceover.Jobs.NewGenerateJobHandler: useCase is required (GenerateVoiceoversUseCase)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GenerateJobHandler{
		useCase: useCase,
		logger:  logger,
	}
}

// Register binds the handler to the canonical jobs.Service dispatch
// for the voiceover.generate job type. The composition root owns
// WHEN this is called (post-bundle construction, pre-Freeze) so
// the dependency injection of Service + UseCase is centralised in
// internal/app/build_bundles_voiceover.go.
//
// Godlike/07: registering alongside the legacy voiceover.batch +
// voiceover.promo handlers is the EXPAND shape — no removal. Both
// paths coexist until CUTOVER flips the call sites.
func (h *GenerateJobHandler) Register(jobsSvc *appjobs.Service) {
	if jobsSvc == nil {
		h.logger.Warn("GenerateJobHandler.Register: jobsSvc is nil; handler not bound to dispatcher")
		return
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeVoiceoverGenerate, h.HandleJob); err != nil {
		h.logger.Error("GenerateJobHandler.Register: RegisterHandler failed",
			zap.String("job_type", appjobs.TypeVoiceoverGenerate),
			zap.Error(err))
		return
	}
	h.logger.Info("registered voiceover.generate handler",
		zap.String("job_type", appjobs.TypeVoiceoverGenerate))
}

// HandleJob processes a voiceover.generate job from the queue.
//
// Dispatch contract (per the EXPAND step scope): HandleJob dispatches
// ONLY to GenerateVoiceoversUseCase.Execute — no business logic
// augments the use case. Per-language error reporting flows back
// through Execute's VoiceoverItemResult.StatusFailed entries, NOT
// via the (map, error) return. The dispatcher stores the resultMap
// in job.Result JSON for downstream observability.
//
// Error mapping:
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - Execute cross-cutting failure (validate, dest resolve,
//     executor setup) → (nil, err): dispatcher marks job FAILED.
//   - Execute partial failure → (resultMap, nil): resultMap.PerLanguage
//     carries per-item status; resultMap.OK=false so callers correlate
//     "0..N items failed" without raising a job-level error.
//
// Progress: tools.Progress is called once at start (5%) and once at
// end (100%). The per-language progress + parallelism wiring lives in
// Blocco 3 (executor.Run accepts a ProgressFunc but the use case
// does not yet wire one — Block 7 followup).
func (h *GenerateJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling voiceover.generate job",
		zap.String("job_id", j.ID))

	if h.hasProgress(tools) {
		tools.Progress(5, "starting voiceover.generate execution")
	}

	var cmd voiceover.GenerateVoiceoversCommand
	if err := json.Unmarshal(j.Payload, &cmd); err != nil {
		return nil, fmt.Errorf("voiceover.generate: unmarshal payload: %w", err)
	}

	res, err := h.useCase.Execute(ctx, &cmd)
	if err != nil {
		h.logger.Error("voiceover.generate cross-cutting failure",
			zap.String("job_id", j.ID),
			zap.Error(err))
		return nil, fmt.Errorf("voiceover.generate: execute: %w", err)
	}

	if h.hasProgress(tools) {
		tools.Progress(100, "voiceover.generate completed")
	}

	// Per-item status failures surface in res.PerLanguage (with
	// res.OK=false). We return (resultMap, nil) so the dispatcher
	// marks the job SUCCEEDED — operators correlate per-item errors
	// via result.FailedCount and job.Result JSON payload.
	return toResultMap(res), nil
}

// hasProgress is the nil-safe guard for the JobTools Progress callback.
// Both tools and its Progress field may be nil depending on the
// dispatcher surface (e.g. tests + history-replay code paths).
func (h *GenerateJobHandler) hasProgress(tools *appjobs.JobTools) bool {
	return tools != nil && tools.Progress != nil
}

// toResultMap serialises a GenerateVoiceoversResult into the
// map[string]any shape that the jobs.Dispatcher writes into
// job.Result JSON. Field names mirror the GenerateVoiceoversResult
// struct's JSON tags so a downstream consumer can unmarshal it back
// into a typed result for ops dashboards.
func toResultMap(res *voiceover.GenerateVoiceoversResult) map[string]any {
	if res == nil {
		return nil
	}
	perLang := make([]map[string]any, 0, len(res.PerLanguage))
	for _, item := range res.PerLanguage {
		perLang = append(perLang, map[string]any{
			"id":            item.ID,
			"language":      item.Language,
			"voice":         item.Voice,
			"status":        item.Status,
			"error":         item.Error,
			"filename":      item.Filename,
			"local_path":    item.LocalPath,
			"cleaned_path":  item.CleanedPath,
			"drive_file_id": item.DriveFileID,
			"drive_link":    item.DriveLink,
			"file_hash":     item.FileHash,
		})
	}
	m := map[string]any{
		"ok":            res.OK,
		"request_id":    res.RequestID,
		"total_outputs": res.TotalOutputs,
		"success_count": res.SuccessCount,
		"failed_count":  res.FailedCount,
		"per_language":  perLang,
	}
	if res.Error != "" {
		m["error"] = res.Error
	}
	if !res.StartedAt.IsZero() {
		m["started_at"] = res.StartedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if !res.CompletedAt.IsZero() {
		m["completed_at"] = res.CompletedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	return m
}
