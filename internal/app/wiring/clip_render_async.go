package wiring

// clip_render_async.go owns the composition-root adapter for the clip.render
// submit→settle continuation.
//
// SCOPE (2026-09-12): this file used to also carry an env-gated wrapper
// (`CLIP_RENDER_ASYNC_COMPLETION` → `wrapClipRenderAsyncCompletion` →
// `clipRenderAsyncExecutor`) plus a capability-discovery interface
// (`cliprender.AsyncCompletionProvider`). That machinery was DEAD: no
// composition root ever called the wrapper, so the switch could not change the
// runtime mode, while the live mode was decided inside Worker.Handle (the worker
// selects Submit/Settle exactly when the renderer implements AsyncRenderExecutor
// and both durable continuation ports are attached). Two authorities for "is
// this render async?" is how the mode became impossible to read from the wiring;
// the wrapper and the discovery interface are DELETED, and the decision now has
// exactly one owner.
//
// What remains is the one thing composition genuinely owns: turning the
// capability's ContinuationEnqueuer port into a durable job enqueue.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// clipRenderContinuationEnqueuer is the composition adapter for the
// capability-owned ContinuationEnqueuer port. It reuses the SAME clip.render
// job type and injects the canonical ParentLink into the payload before the
// broker persists it.
type clipRenderContinuationEnqueuer struct {
	jobs job.Service
}

var _ cliprender.ContinuationEnqueuer = (*clipRenderContinuationEnqueuer)(nil)

func (e *clipRenderContinuationEnqueuer) EnqueueContinuation(ctx context.Context, req cliprender.ContinuationRequest) (string, error) {
	if e == nil || e.jobs == nil {
		return "", fmt.Errorf("clip render continuation enqueue: job service is not wired")
	}
	if strings.TrimSpace(req.ParentJobID) == "" || strings.TrimSpace(req.ActiveKey) == "" {
		return "", fmt.Errorf("clip render continuation enqueue: parent_job_id and active_key are required")
	}
	if err := req.Continuation.Validate(); err != nil {
		return "", err
	}

	base := map[string]any{
		"render_phase": "settle",
		"continuation": req.Continuation,
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return "", fmt.Errorf("clip render continuation enqueue: encode payload: %w", err)
	}
	raw = job.InjectParentLink(raw, job.ParentLink{ParentJobID: req.ParentJobID, ParentRunID: req.ParentRunID})
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("clip render continuation enqueue: decode linked payload: %w", err)
	}

	child, err := e.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type:          cliprender.TypeClipRender,
		Payload:       payload,
		CorrelationID: req.Continuation.Submission.CorrelationID,
		MaxRetries:    3,
		ActiveKey:     req.ActiveKey,
	})
	if err != nil {
		return "", err
	}
	if child == nil || strings.TrimSpace(child.ID) == "" {
		return "", fmt.Errorf("clip render continuation enqueue: broker returned an empty child job")
	}
	return child.ID, nil
}
