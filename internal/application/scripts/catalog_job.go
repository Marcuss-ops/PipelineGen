// Package scripts — CatalogJobServiceImpl extracted from api/script/handler_jobs.go (PR2, June 2026).
package scripts

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	pkgtextutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// CatalogJobServiceImpl satisfies the CatalogJobService interface
// (defined in api/script/helpers.go) for the catalog-to-script background job.
type CatalogJobServiceImpl struct {
	ClipSourceBuilder *ClipSourceBuilder
	Engine            *Engine
	Log               *zap.Logger
}

// NewCatalogJobServiceImpl creates the catalog job service.
func NewCatalogJobServiceImpl(
	clipSourceBuilder *ClipSourceBuilder,
	engine *Engine,
	log *zap.Logger,
) *CatalogJobServiceImpl {
	return &CatalogJobServiceImpl{
		ClipSourceBuilder: clipSourceBuilder,
		Engine:            engine,
		Log:               log,
	}
}

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
func (c *CatalogJobServiceImpl) HandleCatalogScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	c.Log.Info("handling script.generate_from_catalog job", zap.String("job_id", j.ID))

	clipSvc := c.ClipSourceBuilder
	if clipSvc == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if c.Engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	var payload JobPayloadCatalogScript
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Loading %d clips selected from catalog", len(payload.ClipIDs)))
	}

	opts := &ClipGenerationOptions{
		Language:         payload.Language,
		Tone:             payload.Tone,
		Title:            payload.Title,
		Model:            payload.Model,
		TargetWords:      payload.TargetWords,
		TranscriptPolicy: payload.TranscriptPolicy,
		OrderingStrategy: payload.OrderingStrategy,
	}
	if payload.MinQualityScore != nil {
		opts.MinQualityScore = *payload.MinQualityScore
	}
	if payload.MinTranscriptWords != nil {
		opts.MinTranscriptWords = *payload.MinTranscriptWords
	}

	if tools.Progress != nil {
		tools.Progress(15, "Hydrating clips and building evidence cards")
	}

	pack, plan, sourceText, err := clipSvc.BuildClipContext(ctx, payload.ClipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}

	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, NewFingerprintContext(opts.Model, opts.Model))

	if tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine (MemoryGate, normalization)...")
	}

	writeResult, err := c.Engine.WriteScript(ctx, WriteScriptRequest{
		Plan: &script.ScriptGenerationPlan{
			Title:       plan.Title,
			Topic:       plan.Title,
			Language:    opts.Language,
			Tone:        opts.Tone,
			Model:       opts.Model,
			Mode:        gemmamemory.ModeClipToScript,
			SourceText:  sourceText,
			TargetWords: opts.TargetWords,
			UseMemory:   !payload.ForceRefresh,
			SaveToDB:    payload.SaveToDB,
			Prompt:      sourceFingerprint,
		},
		Topic:       plan.Title,
		Title:       plan.Title,
		Language:    opts.Language,
		Tone:        opts.Tone,
		Model:       opts.Model,
		Mode:        gemmamemory.ModeClipToScript,
		SourceText:  sourceText,
		MinWords:    opts.TargetWords,
		Prompt:      sourceFingerprint,
		UseMemory:   !payload.ForceRefresh,
		SaveToDB:    payload.SaveToDB,
		SaveTimeout: 60,
		Type:        opts.Type,
		ClipPack:    pack,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	wordCount := pkgtextutil.CountWords(writeResult.Script)

	if tools.Progress != nil {
		tools.Progress(90, "Finalizing...")
	}

	result := map[string]any{
		"ok":           true,
		"script_id":    writeResult.ScriptID,
		"title":        plan.Title,
		"script":       writeResult.Script,
		"word_count":   wordCount,
		"language":     opts.Language,
		"mode":         "catalog_first",
		"cache_status": writeResult.CacheStatus,
		"clip_coverage": map[string]any{
			"requested": len(payload.ClipIDs),
			"accepted":  len(pack.Clips),
			"used":      len(plan.OrderedClips),
			"excluded":  len(pack.ExcludedClips),
		},
		"narrative_arc":      plan.NarrativeArc,
		"warnings":           plan.Warnings,
		"sections_count":     len(plan.OrderedClips),
		"source_fingerprint": sourceFingerprint,
		"narrative_plan":     plan,
	}

	if len(pack.ExcludedClips) > 0 {
		excluded := make([]map[string]any, 0, len(pack.ExcludedClips))
		for _, ec := range pack.ExcludedClips {
			excluded = append(excluded, map[string]any{
				"clip_id": ec.ClipID,
				"reason":  ec.ExcludeReason,
			})
		}
		result["excluded_clips"] = excluded
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog-first generation completed")
	}

	return result, nil
}
