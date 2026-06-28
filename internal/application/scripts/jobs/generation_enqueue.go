// Package scripts — generation_enqueue.go provides the enqueue
// helpers for the unified script.generate endpoint. The HTTP
// handler binds the envelope and calls EnqueueGenerationJob;
// the worker decodes the envelope and runs the pipeline.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	ports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

	"go.uber.org/zap"
)

// GenerateEnqueueRequest is the input for EnqueueGenerationJob.
type GenerateEnqueueRequest struct {
	Envelope      domainScript.GenerationEnvelopeV2
	ActiveKey     string
	CorrelationID string
}

// NewGenerateEnqueueRequest wraps an envelope into an enqueue request.
func NewGenerateEnqueueRequest(env domainScript.GenerationEnvelopeV2) *GenerateEnqueueRequest {
	return &GenerateEnqueueRequest{
		Envelope: env,
	}
}

// EnqueueGenerationJob marshals the envelope and enqueues a
// script.generate job. Returns the enqueued job.
//
// Issue 4 (June 2026, P1): registry is now a required dependency so
// MaxRetries on the EnqueueRequest is sourced from
// appjobs.Registry.DefaultMaxRetries(jType) instead of the pre-Issue-4
// hard-coded fallback of 3.
//   - For script.generate the canonical registry value is 2 (per
//     internal/application/jobs/registry.go::Compose DefaultMaxRetries
//     entry); the pre-Issue-4 behaviour overrode this to 3.
//   - Nil-tolerant: registry==nil preserves the pre-Issue-4 hardcoded
//     fallback path (the JobsService.Enqueue MaxRetries=3 net still
//     fires). Composition root always attaches appjobs.Compose() via
//     WithRegistry so production wiring is unaffected by nil-tolerance.
//   - Pre-set MaxRetries is preserved verbatim (only set when zero),
//     letting callers that already pass an explicit value opt out of
//     the registry lookup.
func EnqueueGenerationJob(
	ctx context.Context,
	jobsSvc ports.JobEnqueuer,
	req *GenerateEnqueueRequest,
	log *zap.Logger,
	registry *appjobs.Registry,
) (*scriptpkg.Job, error) {
	if jobsSvc == nil {
		return nil, fmt.Errorf("enqueue: jobs service not configured")
	}
	if req == nil {
		return nil, fmt.Errorf("enqueue: nil request")
	}

	payload, err := json.Marshal(req.Envelope)
	if err != nil {
		return nil, fmt.Errorf("enqueue: marshal envelope: %w", err)
	}
	log.Info("enqueue: marshalled envelope",
		zap.String("preset", string(req.Envelope.Preset)),
		zap.Int("item_count", len(req.Envelope.Items)),
		zap.Int("payload_bytes", len(payload)),
		zap.String("correlation_id", req.CorrelationID),
	)
	for i, item := range req.Envelope.Items {
		log.Info("enqueue: item",
			zap.Int("index", i),
			zap.String("id", item.ID),
			zap.String("title", item.Title),
			zap.String("language", item.Language),
			zap.String("source_type", string(item.Source.Type)),
		)
	}

	enqueueReq := &scriptpkg.EnqueueRequest{
		Type:          scriptpkg.TypeScriptGenerate,
		Payload:       json.RawMessage(payload),
		Priority:      5,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
	}

	// Issue 4: source MaxRetries from the registry when it is wired
	// AND the caller did not pre-set a value. This replaces the
	// pre-Issue-4 hard-coded 3-retries fallback that the JobsService
	// silently applied when the request's MaxRetries was zero.
	if enqueueReq.MaxRetries == 0 && registry != nil {
		enqueueReq.MaxRetries = registry.DefaultMaxRetries(scriptpkg.TypeScriptGenerate)
		log.Debug("enqueue: MaxRetries sourced from registry",
			zap.String("job_type", scriptpkg.TypeScriptGenerate),
			zap.Int("max_retries", enqueueReq.MaxRetries),
		)
	}

	enqueued, err := jobsSvc.Enqueue(ctx, enqueueReq)
	if err != nil {
		log.Error("enqueue: failed",
			zap.Error(err),
		)
		return nil, err
	}
	log.Info("enqueue: success",
		zap.String("job_id", enqueued.ID),
		zap.String("status", string(enqueued.Status)),
		zap.Int("max_retries", enqueued.MaxRetries),
	)
	return enqueued, nil
}
