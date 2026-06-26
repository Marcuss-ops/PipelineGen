package scripts

import (
	"context"
	"fmt"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

func (pu *PipelineUseCase) Run(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if pu == nil {
		return nil, fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}
	genPayload, err := scriptpkg.DecodeGeneratePayload(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: decode payload: %w", ErrInvalidPayload, err)
	}
	spec := &genPayload.Spec
	if spec.GenerateSceneImages && !pu.scenesReady {
		return nil, fmt.Errorf("%w: generate_scene_images=true requested but imageService is not initialized", ErrSceneImagesUnavailable)
	}
	if pu.log != nil {
		pu.log.Info("pipeline_dispatch_decided", zap.String("job_id", j.ID), zap.Int("clip_ids", len(spec.ClipIDs)), zap.Int("num_clips", spec.NumClips), zap.Bool("create_doc", spec.CreateDoc), zap.Bool("extract_entities", spec.ExtractEntities))
	}
	started := time.Now()
	var pathResult *ClipSourcePathResult
	switch {
	case len(spec.ClipIDs) > 0:
		if pu.clipBuilder == nil {
			return nil, fmt.Errorf("%w: %d clip_ids provided but clipSourceBuilder is not initialized", ErrClipPipelineUnavailable, len(spec.ClipIDs))
		}
		pathResult, err = pu.handleClipPathExplicit(ctx, spec, tools)
	case spec.NumClips > 0:
		if pu.mediaCurator == nil {
			return nil, fmt.Errorf("%w: num_clips=%d requested but mediaCurator is not initialized", ErrAutoSearchUnavailable, spec.NumClips)
		}
		pathResult, err = pu.handleClipPathAutoSearch(ctx, spec, tools)
	default:
		pathResult, err = pu.handleClipPathTextOnly(ctx, spec, tools)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: path: %w", ErrPipelineGenerationFailed, err)
	}
	pipelineResult, err := pu.pipeline.RunWithClipScenes(ctx, spec, pathResult.WriteResult.Script, pathResult.ClipScenes, tools)
	if err != nil {
		return nil, fmt.Errorf("%w: pipeline: %w", ErrPipelineGenerationFailed, err)
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}
	scriptInsights, _ := pipelineResult.Insights.(ScriptInsights)
	out := pu.buildFinalResult(spec, pathResult,
		pipelineResult.EntitiesJSON,
		scriptInsights,
		pipelineResult.VideoMetadata,
		pipelineResult.DocLink,
		pipelineResult.DocID,
		pipelineResult.Scenes,
		pipelineResult.Voiceovers,
		time.Since(started).Milliseconds())
	if pipelineResult.DocError != "" {
		out["doc_error"] = pipelineResult.DocError
	}
	return out, nil
}
