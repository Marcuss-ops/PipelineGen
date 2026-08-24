// Package scripts — generation_enqueue.go provides the enqueue
// helpers for the unified script.generate endpoint. The HTTP
// handler binds the envelope and calls EnqueueGenerationJob;
// the worker decodes the envelope and runs the pipeline.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	ports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"

	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"

	"go.uber.org/zap"
)

// GenerateEnqueueRequest is the input for EnqueueGenerationJob.
type GenerateEnqueueRequest struct {
	Envelope      domainScript.GenerationEnvelopeV2
	ActiveKey     string
	CorrelationID string
}

// NewGenerateEnqueueRequest wraps an envelope into an enqueue request.
//
// Issue 5 (June 2026, P1): propagates env.CorrelationID (whitespace
// trimmed) into the helper-level GenerateEnqueueRequest so
// EnqueueGenerationJob can forward it to the broker without the caller
// threading it manually. The trim matches the canonical shape used by
// JobsService.Enqueue's idempotency / dedup lookups.
func NewGenerateEnqueueRequest(env domainScript.GenerationEnvelopeV2) *GenerateEnqueueRequest {
	return &GenerateEnqueueRequest{
		Envelope:      env,
		CorrelationID: strings.TrimSpace(env.CorrelationID),
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
) (*job.Job, error) {
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

	// Issue 5 (P1): trim ActiveKey defensively so the helper-side
	// Match remains deterministic regardless of caller path (HTTP header
	// driven from the canonical generate handler or internal RPC.
	// CLI smoke). Whitespace in ActiveKey would cause JobsService.Enqueue's
	// FindActiveByKey dedup lookup to silently miss a real matching
	// active key, breaking the idempotency contract.
	activeKey := strings.TrimSpace(req.ActiveKey)

	// Context fallback for CorrelationID: if the upstream envelope set
	// no value (e.g. CLI smoke callers using EmptyEnvelope helpers),
	// fall back to the canonical corid trace context. Keeps trace
	// continuity end-to-end without forcing every caller to set it.
	correlationID := req.CorrelationID
	if correlationID == "" {
		correlationID = corid.FromContext(ctx)
	}

	enqueueReq := &job.EnqueueRequest{
		Type:          domainScript.TypeGenerate,
		Payload:       json.RawMessage(payload),
		Priority:      5,
		ActiveKey:     activeKey,
		CorrelationID: correlationID,
	}

	// Issue 4: source MaxRetries from the registry when it is wired
	// AND the caller did not pre-set a value. This replaces the
	// pre-Issue-4 hard-coded 3-retries fallback that the JobsService
	// silently applied when the request's MaxRetries was zero.
	if enqueueReq.MaxRetries == 0 && registry != nil {
		enqueueReq.MaxRetries = registry.DefaultMaxRetries(domainScript.TypeGenerate)
		log.Debug("enqueue: MaxRetries sourced from registry",
			zap.String("job_type", domainScript.TypeGenerate),
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
		// Issue 5: surface the resolved ActiveKey so operators can
		// audit idempotency dedup at the log level. Empty here means
		// the caller did not pass an Idempotency-Key header; the
		// broker will not dedup, but the fan-out is unchanged.
		zap.String("active_key", enqueued.ActiveKey),
		zap.String("correlation_id", enqueued.CorrelationID),
		// NOTE (June 2026, P1): the Stripe / AWS-SQS Idempotency-Key
		// convention permits tenant markers in the key value, and the
		// standard trace context may also carry tenant ids. If a future
		// PR adds a per-tenant scrubber (e.g. application/zap.WithRedaction)
		// or a sampling downgrade for noisy tenants, this is the seam
		// to touch. Keeping these fields visible at INFO for now since
		// operator dedup-audit is the load-bearing observability use case.
	)
	return enqueued, nil
}
