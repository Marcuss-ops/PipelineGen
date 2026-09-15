package wiring

// clip_render_async.go owns the composition-root adapter for the clip.render
// submit→settle continuation.
//
// SCOPE (2026-09-12): this file used to also carry an env-gated wrapper
// (`CLIP_RENDER_ASYNC_COMPLETION` → `wrapClipRenderAsyncCompletion` →
// `clipRenderAsyncExecutor`) plus a capability-discovery interface
// (`cliprender.AsyncCompletionProvider`). That machinery was DEAD: no
// composition root ever called the wrapper, so the switch could not change the
// runtime mode, while the live mode was decided inside Worker.Handle. The port
// is Submit/Settle only (the blocking form was deleted in the 2026-09-13
// clip.render audit), so the worker renders asynchronously exactly when both
// durable continuation ports are attached. Two authorities for "is this render
// async?" is how the mode became impossible to read from the wiring; the
// wrapper and the discovery interface are DELETED, and the decision now has
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
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
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

	// The settle child MUST NOT inherit the parent's correlation id: the
	// parent is the same job type (clip.render) and is still RUNNING, so the
	// broker's (type, correlation_id) dedupe would resolve this enqueue back to
	// the PARENT. EnqueueContinuation would then hand the worker its own parent
	// id as the "child", no settle job would ever be created, and the rendered
	// artifact would never be collected or published (the live 2026-09-13
	// defect: parent SUCCEEDED with child_job_id == its own id, zero
	// parent_job_id children, no media_assets written).
	// The settle child is the SAME job type as the submit parent, so its retry
	// budget is the DECLARED policy for that type (jobs.Registry, seeded by
	// registry_media.go) rather than a literal owned by this adapter. The
	// historical literal was hardcoded to 3 while the registered policy for the
	// same job type is 2 — two answers for one fact, with the submit and settle
	// halves of a single render not derivable from the registry. Mirrors the
	// registry-sourcing pattern in
	// capabilities/scripts/jobs/generation_enqueue.go.
	submission := req.Continuation.Submission
	child, err := e.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type:    cliprender.TypeClipRender,
		Payload: payload,
		CorrelationID: cliprender.SettleCorrelationID(
			submission.CorrelationID, submission.RenderJobID, submission.Attempt,
		),
		MaxRetries: appjobs.Compose().DefaultMaxRetries(cliprender.TypeClipRender),
		ActiveKey:  req.ActiveKey,
	})
	if err != nil {
		return "", err
	}
	if child == nil || strings.TrimSpace(child.ID) == "" {
		return "", fmt.Errorf("clip render continuation enqueue: broker returned an empty child job")
	}
	return child.ID, nil
}
