package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

func (pu *PipelineUseCase) Run(
	ctx context.Context,
	j *job.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if pu == nil {
		return nil, fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}

	genPayload, err := scriptpkg.DecodeGeneratePayload(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: decode payload: %w", ErrInvalidPayload, err)
	}
	spec := &genPayload.Spec

	// Phase 2 activation (June 2026) — gate scene image generation:
	// reject any job asking for scene images when scenesReady is false
	// (= ImageService was not wired at composition time). Surfaces the
	// missing dependency as a typed 503-class error instead of silently
	// producing an empty scenes array in the response. The gate runs
	// BEFORE the path dispatch so we fail fast and don't pay the cost
	// of clip curation / engine.WriteScript when the call cannot
	// succeed.
	if spec.GenerateSceneImages && !pu.scenesReady {
		return nil, fmt.Errorf("%w: generate_scene_images=true requested but imageService is not initialized", ErrSceneImagesUnavailable)
	}

	if pu.log != nil {
		pu.log.Info("pipeline_dispatch_decided",
			zap.String("job_id", j.ID),
			zap.Int("clip_ids", len(spec.ClipIDs)),
			zap.Int("num_clips", spec.NumClips),
			zap.Bool("extract_entities", spec.ExtractEntities),
			zap.Bool("generate_scene_images", spec.GenerateSceneImages),
			zap.Bool("generate_voiceover", spec.GenerateVoiceover),
			zap.Int("sentences_per_image", spec.SentencesPerImage),
			zap.Int("images_per_scene", spec.ImagesPerScene),
			zap.String("language", spec.Language),
			zap.String("style", spec.Style))
	}

	startAll := time.Now()

	var pathResult *ClipSourcePathResult

	pathStart := time.Now()
	switch {
	case len(spec.ClipIDs) > 0:
		if pu.clipBuilder == nil {
			return nil, fmt.Errorf("%w: %d clip_ids provided but clipSourceBuilder is not initialized",
				ErrClipPipelineUnavailable, len(spec.ClipIDs))
		}
		pathResult, err = pu.handleClipPathExplicit(ctx, spec, tools)
	case spec.NumClips > 0:
		if pu.mediaCurator == nil {
			return nil, fmt.Errorf("%w: num_clips=%d requested but mediaCurator is not initialized",
				ErrAutoSearchUnavailable, spec.NumClips)
		}
		pathResult, err = pu.handleClipPathAutoSearch(ctx, spec, tools)
	default:
		pathResult, err = pu.handleClipPathTextOnly(ctx, spec, tools)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: path: %w", ErrPipelineGenerationFailed, err)
	}
	if pu.log != nil {
		pu.log.Info("script_generation_completed",
			zap.String("job_id", j.ID),
			zap.Int("script_chars", len(pathResult.WriteResult.Script)),
			zap.Int("word_count", pathResult.WriteResult.WordCount),
			zap.String("cache_status", pathResult.WriteResult.CacheStatus),
			zap.Int64("path_ms", time.Since(pathStart).Milliseconds()))
	}

	// Build human-readable doc content from clip scenes when available,
	// otherwise use the raw script text.
	docContent := pathResult.WriteResult.Script
	if len(pathResult.ClipScenes) > 0 {
		var sceneTexts []string
		for _, cs := range pathResult.ClipScenes {
			if cs.Text != "" {
				sceneTexts = append(sceneTexts, cs.Text)
			}
		}
		if joined := strings.Join(sceneTexts, "\n\n"); joined != "" {
			docContent = joined
		}
	}
	pipelineResult, pipeErr := pu.pipeline.Run(ctx, spec, docContent, tools)
	if pipeErr != nil {
		return nil, fmt.Errorf("%w: pipeline: %w", ErrPipelineGenerationFailed, pipeErr)
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	if pu.log != nil {
		pu.log.Info("pipeline_completed",
			zap.String("job_id", j.ID),
			zap.Int64("total_ms", totalDurMs),
			zap.Int("scenes", len(pipelineResult.Scenes)),
			zap.Int("voiceovers", len(pipelineResult.Voiceovers)),
			zap.Bool("has_doc", pipelineResult.DocLink != ""))
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}

	// PG-033 phase 2 (June 2026): defensive narrowing (mirrors
	// catalog_job.go:166 / commit 290e9cd5). The zero value of
	// ScriptInsights is safe — buildFinalResult only emits insights
	// fields when payload.ExtractEntities is true, and every
	// ScriptInsights field is JSON-zero-safe (nil []string → [],
	// nil interface{} → null).
	scriptInsights, _ := pipelineResult.Insights.(ScriptInsights)
	scriptMeta := make([]VideoMetadata, len(pipelineResult.VideoMetadata))
	for i, m := range pipelineResult.VideoMetadata {
		scriptMeta[i] = VideoMetadata{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
	}

	return pu.buildFinalResult(spec, pathResult,
		pipelineResult.EntitiesJSON,
		scriptInsights,
		scriptMeta,
		pipelineResult.DocLink,
		pipelineResult.DocID,
		pipelineResult.Scenes,
		pipelineResult.Voiceovers,
		totalDurMs), nil
}

// RegisterJobs wires the pipeline job handler into the canonical
// jobs service. Lives on the use case so the handler no longer
// owns job-registration logic (handler is purely transport).
//
// Accepts the canonical Broker port declared in ports.go. The
// parameter was previously `interface{}` with an internal type
// assertion; PG-042 tightens it to the concrete port so composition
// root passes the typed value directly and the compiler enforces
// the contract at build time.
