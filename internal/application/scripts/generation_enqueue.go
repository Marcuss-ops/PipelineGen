// Package scripts — generation_enqueue.go provides the enqueue
// helpers for the unified script.generate endpoint. The HTTP
// handler binds the envelope and calls EnqueueGenerationJob;
// the worker decodes the envelope and runs the pipeline.
package scripts

import (
	"context"
	"encoding/json"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

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
func EnqueueGenerationJob(
	ctx context.Context,
	jobsSvc JobEnqueuer,
	req *GenerateEnqueueRequest,
	log *zap.Logger,
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

	// Wrap in json.RawMessage to prevent double-encoding: the
	// downstream Service.Enqueue calls json.Marshal on Payload,
	// which would base64-encode a []byte. json.RawMessage passes
	// through verbatim.
	enqueueReq := &scriptpkg.EnqueueRequest{
		Type:          scriptpkg.TypeScriptGenerate,
		Payload:       json.RawMessage(payload),
		Priority:      5,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
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
	)
	return enqueued, nil
}
