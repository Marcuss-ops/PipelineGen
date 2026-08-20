package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
// GET /jobs/{id}). JobType is the canonical overlay job type
// (overlay.prepare / overlay.render) the queue worker dispatches on.
type RenderQueueJob struct {
	ID          string             `json:"id"`
	JobType     string             `json:"job_type,omitempty"`
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
// RenderCompletionMetrics separates the worker-reported Chronon duration from
// the client-side wait used to observe the queue. PollingSleep is the time
// deliberately spent sleeping between status requests, so it is the direct
// measurable impact of the polling cadence (and not Chronon work).
type RenderCompletionMetrics struct {
	CompletionWait time.Duration
	PollingSleep   time.Duration
	PollInterval   time.Duration
	PollCount      int
}

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
	// Keep the queue dispatch explicit. RenderingGen uses this discriminator to
	// route the job through the overlay renderer (and to apply the overlay media
	// contract/ffprobe checks); omitting it silently falls back to the legacy
	// render_segment path.
	job := RenderQueueJob{
		ID:          plan.PlanID,
		JobType:     capoverlay.JobTypeRender,
		OverlaySpec: spec,
		Assets:      assets,
	}

	if err := e.client.Submit(ctx, job); err != nil {
		if !errors.Is(err, ErrJobExists) {
			return RenderReference{}, fmt.Errorf("chronon queue render submit failed: %w", err)
		}
	}

	done, wait, err := e.waitForCompletion(ctx, plan.PlanID)
	if err != nil {
		return RenderReference{}, err
	}
	if e.recorder != nil {
		attempt := BuildRenderAttemptAnalyticsWithWait(plan.PlanID, plan, done.Artifact, wait)
		if err := e.recorder.RecordAttempt(ctx, attempt); err != nil {
			return RenderReference{}, fmt.Errorf("record render attempt analytics: %w", err)
		}
	}
	return RenderReference{JobID: plan.PlanID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}

// ── overlay.prepare ───────────────────────────────────────────────────

// QueuePrepareEnqueuer submits the overlay.prepare job for the run's
// pre-timing OverlayIntents to the central RenderingGen queue. Unlike the
// render enqueuer it is fire-and-forget: prepare resolves templates and
// prefetches entity assets independently of the timing-frozen render path
// and must never block the pipeline. The job id is deterministic
// ("prepare-"+planID) so replays are idempotent.
type QueuePrepareEnqueuer struct {
	client RenderQueueClient
}

// NewQueuePrepareEnqueuer creates a queue-backed prepare enqueuer.
func NewQueuePrepareEnqueuer(client RenderQueueClient) (*QueuePrepareEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue prepare enqueuer requires a queue client")
	}
	return &QueuePrepareEnqueuer{client: client}, nil
}

// EnqueuePrepare submits the prepare job and returns immediately. A job that
// already exists (ErrJobExists) is treated as idempotent success so a retry
// never double-prepares.
func (e *QueuePrepareEnqueuer) EnqueuePrepare(ctx context.Context, req capoverlay.PrepareRequest) error {
	if e == nil || e.client == nil {
		return fmt.Errorf("queue prepare enqueuer is not configured")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	spec, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("chronon queue prepare marshal: %w", err)
	}
	job := RenderQueueJob{
		ID:          "prepare-" + req.PlanID,
		JobType:     capoverlay.JobTypePrepare,
		OverlaySpec: spec,
		Assets:      prepareAssets(req.Intents),
	}
	if err := e.client.Submit(ctx, job); err != nil {
		if errors.Is(err, ErrJobExists) {
			return nil // idempotent replay
		}
		return fmt.Errorf("chronon queue prepare submit failed: %w", err)
	}
	return nil
}

// prepareAssets collects the entity-image assets referenced by the intents,
// deduplicated by content hash, so the queue worker can prefetch them during
// the prepare phase.
func prepareAssets(intents []capoverlay.OverlayIntent) []RenderQueueAsset {
	var assets []RenderQueueAsset
	seen := make(map[string]bool)
	for _, intent := range intents {
		for _, ref := range intent.Payload.AssetRefs {
			hash := strings.ToLower(strings.TrimSpace(ref.SHA256))
			if hash == "" || seen[hash] {
				continue
			}
			seen[hash] = true
			assets = append(assets, RenderQueueAsset{Hash: hash, URL: ref.URL})
		}
	}
	return assets
}

// waitForCompletion polls the queue until the job reaches a terminal state.
// The whole blocked interval is recorded as a completion wait on the bound
// run (RunReport.Waits), never as a stage: it is time spent waiting on the
// render queue, not pipeline CPU work.
func (e *QueueRenderEnqueuer) waitForCompletion(ctx context.Context, id string) (RenderQueueJob, RenderCompletionMetrics, error) {
	waitStarted := time.Now()
	interval := e.pollInterval
	if interval <= 0 {
		interval = defaultQueuePollInterval
	}
	metrics := RenderCompletionMetrics{PollInterval: interval}
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
		metrics.PollCount++
		if err != nil {
			return RenderQueueJob{}, metrics, err
		}
		switch job.State {
		case "completed":
			metrics.CompletionWait = time.Since(waitStarted)
			return job, metrics, nil
		case "failed":
			reason := job.FailReason
			if reason == "" {
				reason = "unknown failure"
			}
			metrics.CompletionWait = time.Since(waitStarted)
			return job, metrics, fmt.Errorf("render job %s failed: %s", id, reason)
		}

		sleepStarted := time.Now()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			metrics.PollingSleep += time.Since(sleepStarted)
			metrics.CompletionWait = time.Since(waitStarted)
			return RenderQueueJob{}, metrics, ctx.Err()
		case <-timer.C:
			metrics.PollingSleep += time.Since(sleepStarted)
		}
	}
}
