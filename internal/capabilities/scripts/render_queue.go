package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
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
// internal/infrastructure/renderinggen.
type RenderQueueClient interface {
	// Submit enqueues a job. It returns ErrJobExists when a job with the
	// same ID is already present (idempotent replay).
	Submit(ctx context.Context, job RenderQueueJob) error
	// Get returns the current state of a job, including its artifact once
	// the job completes.
	Get(ctx context.Context, id string) (RenderQueueJob, error)
}

// QueueRenderEnqueuer adapts the central RenderingGen queue to the generation
// RenderEnqueuer port. It submits the canonical render plan as the queue's
// overlay spec, then blocks until the render completes so the returned
// RenderReference carries the certified artifact.
type QueueRenderEnqueuer struct {
	client       RenderQueueClient
	fs           render.FileSystem
	pollInterval time.Duration
}

// NewQueueRenderEnqueuer creates a queue-backed render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient, fs render.FileSystem) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	if fs == nil {
		return nil, fmt.Errorf("queue render enqueuer requires filesystem adapter")
	}
	return &QueueRenderEnqueuer{client: client, fs: fs, pollInterval: defaultQueuePollInterval}, nil
}

// Enqueue validates the plan, submits it to the central queue and waits for
// the artifact to complete. The job ID is the render plan's JobID, which makes
// replays idempotent against the queue's ON CONFLICT (id) DO NOTHING submit.
func (e *QueueRenderEnqueuer) Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error) {
	if e == nil || e.client == nil || e.fs == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueuer is not configured")
	}
	if result.RenderPlan == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueue requires RenderPlan")
	}
	if _, err := render.ValidateRenderPlan(*result.RenderPlan, e.fs); err != nil {
		return RenderReference{}, fmt.Errorf("queue render enqueue validation failed: %w", err)
	}

	plan := *result.RenderPlan
	spec, err := json.Marshal(plan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("queue render enqueue marshal overlay spec: %w", err)
	}
	job := RenderQueueJob{ID: plan.JobID, OverlaySpec: spec, Assets: queueAssets(&plan)}

	if err := e.client.Submit(ctx, job); err != nil {
		if !errors.Is(err, ErrJobExists) {
			return RenderReference{}, fmt.Errorf("queue render submit failed: %w", err)
		}
	}

	done, err := e.waitForCompletion(ctx, plan.JobID)
	if err != nil {
		return RenderReference{}, err
	}
	return RenderReference{JobID: plan.JobID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}

// waitForCompletion polls the queue until the job reaches a terminal state.
func (e *QueueRenderEnqueuer) waitForCompletion(ctx context.Context, id string) (RenderQueueJob, error) {
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

// queueAssets projects the render plan's manifest and final audio into the
// asset references the central queue worker resolves by hash.
func queueAssets(plan *render.RenderPlan) []RenderQueueAsset {
	if plan == nil {
		return nil
	}
	assets := make([]RenderQueueAsset, 0, len(plan.Manifest)+1)
	for _, entry := range plan.Manifest {
		if entry.SHA256 == "" {
			continue
		}
		assets = append(assets, RenderQueueAsset{Hash: entry.SHA256})
	}
	if plan.FinalAudio != nil && plan.FinalAudio.SHA256 != "" {
		assets = append(assets, RenderQueueAsset{Hash: plan.FinalAudio.SHA256})
	}
	return assets
}

var _ RenderEnqueuer = (*QueueRenderEnqueuer)(nil)
