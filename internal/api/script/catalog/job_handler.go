package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Service handles background script.generate_from_catalog jobs.
type Service struct {
	clipSourceBuilder *scripts.ClipSourceBuilder
	engine            *scripts.Engine
	log               *zap.Logger
}

// NewService creates a new catalog job service.
func NewService(
	clipSourceBuilder *scripts.ClipSourceBuilder,
	engine *scripts.Engine,
	log *zap.Logger,
) *Service {
	return &Service{
		clipSourceBuilder: clipSourceBuilder,
		engine:            engine,
		log:               log,
	}
}

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
func (s *Service) HandleCatalogScriptGenerateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling script.generate_from_catalog job", zap.String("job_id", job.ID))

	clipSvc := s.clipSourceBuilder
	if clipSvc == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if s.engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	var payload curation.JobPayloadCatalogScript
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Loading %d clips selected from catalog", len(payload.ClipIDs)))
	}

	opts := &scripts.ClipGenerationOptions{
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

	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, scripts.NewFingerprintContext(opts.Model, opts.Model))

	if tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine (MemoryGate, normalization)...")
	}

	writeResult, err := s.engine.WriteScript(ctx, scripts.WriteScriptRequest{
		Plan: &scripts.ScriptGenerationPlan{
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

	wordCount := textutil.CountWords(writeResult.Script)

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
