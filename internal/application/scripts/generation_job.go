// Package scripts — generation_job.go is the single job-system
// handler for `script.generate` jobs. It decodes a
// GenerationEnvelopeV2 from the job payload and delegates to
// GenerateOneUseCase (single item) or GenerateManyUseCase
// (multiple items).
//
// This handler replaces the fragmented per-job-type handlers:
//   - PipelineUseCase.HandleJob (script.generate_from_clips)
//   - BatchJobHandler.Handle (script.generate_batch)
//   - CatalogJobServiceImpl.HandleCatalogScriptGenerateJob
//     (script.generate_from_catalog)
//
// All job-type registration lives in GenerateJobHandler.RegisterJobs.
package scripts

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// GenerateJobHandler is the application-layer job-system handler for
// `script.generate` jobs. Registered via the jobs broker in
// wire_script.go:
//
//	root.Jobs.Service.RegisterHandler(job.TypeScriptGenerate,
//	    genJobHandler.Handle)
type GenerateJobHandler struct {
	one  *GenerateOneUseCase
	many *GenerateManyUseCase
	cfg  NormalizationConfig
	log  *zap.Logger
}

// NewGenerateJobHandler wires the handler to the unified use cases.
func NewGenerateJobHandler(
	one *GenerateOneUseCase,
	many *GenerateManyUseCase,
	cfg NormalizationConfig,
	log *zap.Logger,
) *GenerateJobHandler {
	return &GenerateJobHandler{
		one:  one,
		many: many,
		cfg:  cfg,
		log:  log,
	}
}

// Handle is the queue-worker entry point. Decodes the envelope,
// dispatches to single or batch generation, and returns results
// as a typed map for the job system.
func (h *GenerateJobHandler) Handle(
	ctx context.Context,
	j *scriptpkg.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil {
		return nil, fmt.Errorf("generate job handler: not constructed")
	}

	// Decode the envelope.
	env, err := domainScript.DecodeEnvelopeV2(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("generate job handler: decode envelope: %w", err)
	}

	if h.log != nil {
		h.log.Info("handling script.generate job",
			zap.String("job_id", j.ID),
			zap.String("preset", string(env.Preset)),
			zap.Int("items", len(env.Items)))
	}

	// Pipe progress through.
	var progressFn func(int, string)
	if tools != nil && tools.Progress != nil {
		progressFn = tools.Progress
	}

	if len(env.Items) == 1 {
		// Single-item path.
		tracker := NewProgressTracker(progressFn, env.Items[0].ID)
		result, err := h.one.Execute(ctx, env.Items[0], env.Preset, tracker)
		if err != nil {
			if h.log != nil {
				h.log.Error("script.generate: single-item failed",
					zap.String("job_id", j.ID),
					zap.Error(err))
			}
			return nil, err
		}
		return mapGenerationResult(result)
	}

	// Multi-item path.
	manyResult, err := h.many.Execute(ctx, env, h.cfg, progressFn)
	if err != nil {
		if h.log != nil {
			h.log.Error("script.generate: multi-item failed",
				zap.String("job_id", j.ID),
				zap.Error(err))
		}
		// Return partial results even on error.
		if manyResult != nil {
			return mapManyResult(manyResult)
		}
		return nil, err
	}
	return mapManyResult(manyResult)
}

// RegisterJobs registers the handler for TypeScriptGenerate with
// the canonical Broker port.
func (h *GenerateJobHandler) RegisterJobs(jobsSvc Broker) error {
	if h == nil {
		return fmt.Errorf("generate job handler: not constructed")
	}
	if jobsSvc == nil {
		return nil
	}
	if err := jobsSvc.RegisterHandler(scriptpkg.TypeScriptGenerate, h.Handle); err != nil {
		return fmt.Errorf("generate job handler: register: %w", err)
	}
	if h.log != nil {
		h.log.Info("registered script.generate job handler")
	}
	return nil
}

// ── Result mapping ─────────────────────────────────────────────────

func mapGenerationResult(r *domainScript.GenerationResult) (map[string]any, error) {
	if r == nil {
		return map[string]any{"ok": false}, nil
	}
	out := map[string]any{
		"ok":             true,
		"script":         r.Script,
		"word_count":     r.WordCount,
		"title":          r.Title,
		"language":       r.Language,
		"model":          r.Model,
		"cache_status":   r.CacheStatus,
		"cache_hit":      r.CacheHit,
		"timings":        r.Timings,
	}
	if r.EntitiesJSON != "" {
		out["entities_json"] = r.EntitiesJSON
	}
	if len(r.Metadata) > 0 {
		out["metadata"] = r.Metadata
	}
	if len(r.Voiceovers) > 0 {
		out["voiceovers"] = r.Voiceovers
	}
	if len(r.SceneImages) > 0 {
		out["scenes"] = r.SceneImages
	}
	if len(r.ClipScenes) > 0 {
		out["clip_scenes"] = r.ClipScenes
	}
	if r.DocLink != "" {
		out["doc_link"] = r.DocLink
		out["doc_id"] = r.DocID
	}
	if len(r.SearchResults) > 0 {
		out["search_results"] = r.SearchResults
	}
	if len(r.AcceptedClipIDs) > 0 {
		out["accepted_clip_ids"] = r.AcceptedClipIDs
	}
	if len(r.Warnings) > 0 {
		out["warnings"] = r.Warnings
	}
	return out, nil
}

func mapManyResult(r *GenerateManyResult) (map[string]any, error) {
	results := make([]map[string]any, 0, len(r.Results))
	for _, item := range r.Results {
		itemMap, _ := mapGenerationResult(item)
		results = append(results, itemMap)
	}
	out := map[string]any{
		"ok":        len(r.Warnings) == 0,
		"count":     len(r.Results),
		"total":     len(r.Results) + len(r.Warnings),
		"results":   results,
	}
	if len(r.Warnings) > 0 {
		out["warnings"] = r.Warnings
	}
	return out, nil
}

