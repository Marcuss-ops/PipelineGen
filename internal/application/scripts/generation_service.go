// Package scripts contains the canonical service that translates script HTTP
// requests into versioned background jobs.
package scripts

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type FromClipsResult struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	JobStatus string `json:"job_status"`
}

type GenerationService struct {
	enq JobEnqueuer
	cfg *config.Config
	log *zap.Logger
}

func NewGenerationService(enq JobEnqueuer, cfg *config.Config, log *zap.Logger) *GenerationService {
	return &GenerationService{enq: enq, cfg: cfg, log: log}
}

// EnqueueFromClips is the canonical custom-generation entry point. New jobs are
// always written using GeneratePayload v1; the decoder keeps flat-payload
// support only for jobs that were already queued before this migration.
func (g *GenerationService) EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*FromClipsResult, error) {
	spec.CreateDoc = true
	return g.enqueue(ctx, scriptpkg.PresetCustom, spec)
}

// EnqueueWithImages applies the complete documented preset and uses the same
// canonical versioned envelope as EnqueueFromClips.
func (g *GenerationService) EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*FromClipsResult, error) {
	spec.GenerateSceneImages = true
	spec.GenerateVoiceover = true
	spec.ExtractEntities = false
	spec.GenerateMetadata = false
	spec.CreateDoc = true
	return g.enqueue(ctx, scriptpkg.PresetWithImages, spec)
}

func (g *GenerationService) enqueue(ctx context.Context, preset scriptpkg.Preset, spec scriptpkg.GenerationSpec) (*FromClipsResult, error) {
	if g == nil {
		return nil, fmt.Errorf("generation service not constructed")
	}
	if g.enq == nil {
		return nil, fmt.Errorf("generation service not initialized")
	}
	if !spec.HasText() && !spec.HasClips() {
		return nil, fmt.Errorf("%w: topic, source_text, clip_ids, or num_clips is required", scriptpkg.ErrInvalidPayload)
	}

	payload, err := json.Marshal(scriptpkg.NewGeneratePayload(preset, spec))
	if err != nil {
		return nil, fmt.Errorf("marshal versioned generation payload: %w", err)
	}
	queued, err := g.enq.Enqueue(ctx, &job.EnqueueRequest{
		Type:    job.TypeClipScriptGenerate,
		Payload: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue script.generate_from_clips: %w", err)
	}

	if g.log != nil {
		g.log.Info("enqueued script.generate_from_clips job",
			zap.String("job_id", queued.ID),
			zap.String("preset", string(preset)),
			zap.String("topic", spec.Topic),
			zap.Int("clip_ids", len(spec.ClipIDs)),
			zap.Int("num_clips", spec.NumClips),
			zap.Bool("create_doc", spec.CreateDoc),
			zap.Bool("extract_entities", spec.ExtractEntities),
			zap.Bool("generate_scene_images", spec.GenerateSceneImages))
	}

	return &FromClipsResult{
		OK:        true,
		JobID:     queued.ID,
		JobStatus: string(queued.Status),
	}, nil
}
