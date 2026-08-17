package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ErrJobExists is returned by RenderQueueClient.Submit when a job with the
// same ID was already enqueued. The queue enqueuer treats this as success and
// proceeds to wait on the existing job, making retries idempotent.
var ErrJobExists = errors.New("render job already exists")

// defaultQueuePollInterval is how long the queue enqueuer waits between
// status polls while the render is in flight.
const defaultQueuePollInterval = 2 * time.Second

// RenderQueueAsset points at an input asset the central queue worker must
// fetch. Hash is the object-store lookup key (the SHA-256 of the file).
type RenderQueueAsset struct {
	Hash string `json:"hash"`
	URL  string `json:"url,omitempty"`
}

// RenderQueueJob is the queue-side view of a submitted render job. It is the
// wire contract with the central RenderingGen queue (POST /jobs and
// GET /jobs/{id}).
type RenderQueueJob struct {
	ID          string             `json:"id"`
	OverlaySpec json.RawMessage    `json:"overlay_spec"`
	Assets      []RenderQueueAsset `json:"assets"`
	State       string             `json:"state"`
	FailReason  string             `json:"fail_reason,omitempty"`
	Artifact    *RenderArtifact    `json:"artifact,omitempty"`
}

// RenderQueueClient is the narrow port for the central RenderingGen queue.
// The capability stays independent of HTTP; the concrete client lives in
// internal/platform/renderinggen.
type RenderQueueClient interface {
	// Submit enqueues a job. It returns ErrJobExists when a job with the
	// same ID is already present (idempotent replay).
	Submit(ctx context.Context, job RenderQueueJob) error
	// Get returns the current state of a job, including its artifact once
	// the job completes.
	Get(ctx context.Context, id string) (RenderQueueJob, error)
}

// QueueRenderEnqueuer adapts the central RenderingGen queue for the future
// Chronon overlay render path. It compiles the semantic OverlayPlan into the
// concrete chronon.render-plan.v1 document and blocks until the render
// completes, returning the certified artifact reference. The removed video
// render enqueue path is no longer part of PipelineGen.
type QueueRenderEnqueuer struct {
	client       RenderQueueClient
	pollInterval time.Duration
	// recorder optionally persists one analytics row per completed attempt.
	// Nil means analytics are not recorded (no-op, not a failure).
	recorder RenderAttemptRecorder
}

// NewQueueRenderEnqueuer creates a queue-backed Chronon render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	return &QueueRenderEnqueuer{client: client, pollInterval: defaultQueuePollInterval}, nil
}

// SetRecorder attaches the optional analytics recorder. Production
// composition injects the SQLite-backed recorder; tests may inject a fake or
// leave it nil to skip analytics.
func (e *QueueRenderEnqueuer) SetRecorder(r RenderAttemptRecorder) {
	if e == nil {
		return
	}
	e.recorder = r
}

// EnqueueChrononPlan compiles the semantic OverlayPlan into the concrete
// chronon.render-plan.v1 document and submits it to the central queue. This is
// the production path that turns PipelineGen's visual instructions into the
// document the RenderingGen worker writes to plan.json and hands to
// chronon3d_cli. It then blocks until the render completes (or fails) and
// returns the certified artifact reference.
func (e *QueueRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	if e == nil || e.client == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueuer is not configured")
	}
	compiled, err := capoverlay.CompileChrononPlan(plan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("chronon plan compile failed: %w", err)
	}
	spec, err := compiled.Marshal()
	if err != nil {
		return RenderReference{}, err
	}
	assets := make([]RenderQueueAsset, 0, len(compiled.Assets))
	for _, a := range compiled.Assets {
		assets = append(assets, RenderQueueAsset{Hash: a.Hash, URL: a.LogicalPath})
	}
	job := RenderQueueJob{ID: plan.PlanID, OverlaySpec: spec, Assets: assets}

	if err := e.client.Submit(ctx, job); err != nil {
		if !errors.Is(err, ErrJobExists) {
			return RenderReference{}, fmt.Errorf("chronon queue render submit failed: %w", err)
		}
	}

	done, err := e.waitForCompletion(ctx, plan.PlanID)
	if err != nil {
		return RenderReference{}, err
	}
	if e.recorder != nil {
		attempt := BuildRenderAttemptAnalytics(plan.PlanID, plan, done.Artifact)
		if err := e.recorder.RecordAttempt(ctx, attempt); err != nil {
			return RenderReference{}, fmt.Errorf("record render attempt analytics: %w", err)
		}
	}
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}

// waitForCompletion polls the queue until the job reaches a terminal state.
// The whole blocked interval is recorded as a completion wait on the bound
// run (RunReport.Waits), never as a stage: it is time spent waiting on the
// render queue, not pipeline CPU work.
func (e *QueueRenderEnqueuer) waitForCompletion(ctx context.Context, id string) (RenderQueueJob, error) {
	waitStarted := time.Now()
	defer func() {
		kernobs.RecordWait(ctx, kernobs.WaitInfo{
			Kind:       kernobs.WaitCompletion,
			Component:  kernobs.ComponentRenderQueue,
			StartedAt:  waitStarted,
			FinishedAt: time.Now(),
		})
	}()
	for {
		job, err := e.client.Get(ctx, id)
		if err != nil {
			return RenderQueueJob{}, err
		}
		switch job.State {
		case "completed":
			return job, nil
		case "failed":
			reason := job.FailReason
			if reason == "" {
				reason = "unknown failure"
			}
			return job, fmt.Errorf("render job %s failed: %s", id, reason)
		}

		interval := e.pollInterval
		if interval <= 0 {
			interval = defaultQueuePollInterval
		}
		select {
		case <-ctx.Done():
			return RenderQueueJob{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}
