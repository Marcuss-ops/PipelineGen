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

	enqueueReq := &scriptpkg.EnqueueRequest{
		Type:          scriptpkg.TypeScriptGenerate,
		Payload:       payload,
		Priority:      5,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
	}

	return jobsSvc.Enqueue(ctx, enqueueReq)
}
