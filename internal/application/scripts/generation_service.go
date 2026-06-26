// Package scripts — generation_service.go is the canonical implementation
// of the scriptapi.GenerationService interface consumed by the legacy
// script Handler. It translates an HTTP request into a queued background
// job via the JobEnqueuer port declared in ports.go.
//
// PG-029 (June 2026): FromClipsResult consolidated here from the
// now-deleted types.go.
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

// FromClipsResult holds the result of an enqueue-from-clips operation.
type FromClipsResult struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	JobStatus string `json:"job_status"`
}

// GenerationService is the canonical implementation of
// `scriptapi.GenerationService` (internal/api/script/handler.go:17).
// It enqueues async pipeline jobs and returns the job ID + initial
// status for the caller.
type GenerationService struct {
	enq JobEnqueuer
	cfg *config.Config
	log *zap.Logger
}

// NewGenerationService is the canonical constructor for *GenerationService.
//
// enq: any implementation of JobEnqueuer (production: *job.Service, or
// root.Jobs.Facade which forwards to it). When nil, all Enqueue*
// methods return an explicit 503-mappable error rather than a silent
// no-op — surfaces missing wiring at first integration test instead of
// at production.
// cfg: resolved runtime configuration. Reserved for future per-request
// timeout overrides; currently used only for logging diagnostics on
// the first call.
// log: zap logger; may be nil (logging degrades to no-op).
func NewGenerationService(enq JobEnqueuer, cfg *config.Config, log *zap.Logger) *GenerationService {
	return &GenerationService{
		enq: enq,
		cfg: cfg,
		log: log,
	}
}

// EnqueueFromClips translates a /generate-from-clips HTTP request into
// a queued script.generate_from_clips job, returning the enqueued
// job's ID + initial status.
//
// Mapping:
//   - HTTP request: scriptpkg.GenerationSpec (JSON body)
//   - Worker payload: same GenerationSpec, marshalled to JSON, wrapped
//     in *job.EnqueueRequest{Type: job.TypeClipScriptGenerate}
//   - Worker decode: pipeline_usecase.go::Run() calls
//     scriptpkg.DecodeGeneratePayload(j.Payload) which reconstructs
//     the same GenerationSpec.
//
// The two-field JSON round-trip is idempotent: the encoded payload
// survives structured cloning (types/sentinels, defaults) so worker
// and HTTP path share an identical contract.
func (g *GenerationService) EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*FromClipsResult, error) {
	if g == nil {
		return nil, fmt.Errorf("generation service not constructed (composition root must call scripts.NewGenerationService)")
	}
	if g.enq == nil {
		return nil, fmt.Errorf("generation service not initialized (composition root must wire JobEnqueuer in ServiceDeps.NewGenerationService)")
	}

	payload, err := encodeGenerationSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("encode generate payload: %w", err)
	}

	req := &job.EnqueueRequest{
		Type:    job.TypeClipScriptGenerate,
		Payload: payload,
	}

	enqueuedJob, err := g.enq.Enqueue(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("enqueue script.generate_from_clips: %w", err)
	}

	if g.log != nil {
		g.log.Info("enqueued script.generate_from_clips job",
			zap.String("job_id", enqueuedJob.ID),
			zap.String("topic", spec.Topic),
			zap.Int("clip_ids", len(spec.ClipIDs)),
			zap.Int("num_clips", spec.NumClips),
			zap.Bool("extract_entities", spec.ExtractEntities),
			zap.Bool("generate_scene_images", spec.GenerateSceneImages))
	}

	return &FromClipsResult{
		OK:        true,
		JobID:     enqueuedJob.ID,
		JobStatus: string(enqueuedJob.Status),
	}, nil
}

// EnqueueWithImages wraps EnqueueFromClips with the canonical
// PresetWithImages semantics followed by an envelope encode through
// scriptpkg.NewGeneratePayload.
//
// PJ-WITH-IMAGES (June 2026): the previous implementation only
// forced spec.GenerateSceneImages = true and forwarded the flat
// GenerationSpec as the worker payload. The audit verdict flagged
// this as incomplete — the documented PresetWithImages means
// scene_images=ON, voiceover=ON, extract_entities=OFF, metadata=OFF.
// The new path applies ALL four checks AND encodes the payload in
// the versioned envelope (scriptpkg.NewGeneratePayload) with
// preset="with_images" so the worker's DecodeGeneratePayload
// recognises the request as preset-driven rather than flag-driven.
//
// Job type stays canonical (job.TypeClipScriptGenerate). The
// preset is part of the typed payload, not the job type, so the
// JobService registry continues to expose a single entry for
// both /generate-from-clips and /generate-with-images.
func (g *GenerationService) EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*FromClipsResult, error) {
	if g == nil {
		return nil, fmt.Errorf("generation service not constructed")
	}
	if g.enq == nil {
		return nil, fmt.Errorf("generation service not initialized (composition root must wire JobEnqueuer)")
	}

	// Force canonical PresetWithImages semantics on the spec. The
	// order matters: scene_images + voiceover force ON; entity
	// extraction + metadata generate force OFF. We do NOT trust
	// caller-supplied overrides here — the preset semantics are
	// non-negotiable per the documented contract.
	spec.GenerateSceneImages = true
	spec.GenerateVoiceover = true
	spec.ExtractEntities = false
	spec.GenerateMetadata = false

	// Versioned envelope with the preset stamp. ScriptResult stays
	// identical to EnqueueFromClips' wire shape because the worker
	// reads both formats via DecodeGeneratePayload.
	envelope := scriptpkg.NewGeneratePayload(scriptpkg.PresetWithImages, spec)
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal versioned with_images payload: %w", err)
	}

	req := &job.EnqueueRequest{
		Type:    job.TypeClipScriptGenerate,
		Payload: payload,
	}

	enqueuedJob, err := g.enq.Enqueue(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("enqueue script.generate_from_clips (preset=with_images): %w", err)
	}

	if g.log != nil {
		g.log.Info("enqueued script.generate_from_clips job (preset=with_images)",
			zap.String("job_id", enqueuedJob.ID),
			zap.String("topic", spec.Topic),
			zap.String("preset", string(scriptpkg.PresetWithImages)))
	}

	return &FromClipsResult{
		OK:        true,
		JobID:     enqueuedJob.ID,
		JobStatus: string(enqueuedJob.Status),
	}, nil
}

// encodeGenerationSpec marshals a GenerationSpec to JSON for the job
// payload. Kept as a tiny inline helper (rather than a domain method)
// because the domain/script package is parser-focused and the only
// producer is here. Keeps the package's API surface narrow: callers
// see encodeGenerationSpec as private.
//
// NOTE (Phase 2 reviewer feedback, June 2026): the previous
// "literal-zero-value" equality check (`spec == GenerationSpec{}`)
// was a false negative — GenerationSpec carries []string fields which
// are not comparable with `==`, so the check did not compile and would
// have provided false safety. Dropped. The handler's ShouldBindJSON
// already validates the body shape before EnqueueFromClips runs; the
// worker surfaces useful failures when fields are empty or missing.
func encodeGenerationSpec(spec scriptpkg.GenerationSpec) (json.RawMessage, error) {
	bytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal generation spec: %w", err)
	}
	return bytes, nil
}
