// Package renderinggen adapts the central RenderingGen queue's public client
// to the script-generation capability. PipelineGen no longer owns the HTTP
// contract: it delegates to github.com/Marcuss-ops/RenderingGen/queue/client
// and only maps between the capability domain types and the queue's wire
// types, so the wire format can never drift between the two codebases.
package renderinggen

import (
	"context"
	"fmt"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// Client adapts the queue's public client to renderqueue.RenderQueueClient.
type Client struct {
	q        *queueclient.Client
	prefetch *AssetPrefetcher
}

// AssetPrefetcher warms every RenderQueueJob asset without changing queue
// state. It is injected by the composition root and shares the canonical
// overlay AssetPreparer used by overlay.prepare.
type AssetPrefetcher struct {
	prepare func(context.Context, []renderqueue.RenderQueueAsset) error
}

func NewAssetPrefetcher(prepare func(context.Context, []renderqueue.RenderQueueAsset) error) *AssetPrefetcher {
	return &AssetPrefetcher{prepare: prepare}
}

func (p *AssetPrefetcher) Prefetch(ctx context.Context, assets []renderqueue.RenderQueueAsset) error {
	if p == nil || p.prepare == nil {
		return nil
	}
	return p.prepare(ctx, assets)
}

func (c *Client) SetAssetPrefetcher(prefetch *AssetPrefetcher) {
	if c != nil {
		c.prefetch = prefetch
	}
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{q: queueclient.New(baseURL)}
}

// Capabilities returns the union of features advertised by currently live
// RenderingGen workers. It is used by producer preflights so a plan is checked
// against the active renderer inventory before any expensive work starts.
func (c *Client) Capabilities(ctx context.Context) ([]string, error) {
	if c == nil || c.q == nil {
		return nil, fmt.Errorf("renderinggen capabilities: client is not configured")
	}
	capabilities, err := c.q.Capabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("renderinggen capabilities: %w", err)
	}
	return capabilities, nil
}

// Submit enqueues a job. A 409 (job already exists) is surfaced as
// renderqueue.ErrJobExists only after Queue atomically verified an exact replay.
func (c *Client) Submit(ctx context.Context, job renderqueue.RenderQueueJob) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen submit: client is not configured")
	}
	identity, err := renderqueue.RenderRequestIdentityFor(job)
	if err != nil {
		return fmt.Errorf("renderinggen request identity: %w", err)
	}
	if c.prefetch != nil {
		if err := c.prefetch.Prefetch(ctx, job.Assets); err != nil {
			return fmt.Errorf("renderinggen asset prefetch: %w", err)
		}
	}
	err = c.q.Submit(ctx, queueclient.Job{
		ID:                    job.ID,
		JobType:               job.JobType,
		ParentJobID:           job.ParentJobID,
		ChunkIndex:            job.ChunkIndex,
		FrameRange:            toQueueFrameRange(job.FrameRange),
		PreparedRenderPackage: toQueuePreparedRenderPackage(job.PreparedRenderPackage),
		RenderPlan:            job.OverlaySpec,
		Assets:                toQueueAssets(job.Assets),
		RequireGPU:            job.RequireGPU,
		IdempotencyKey:        identity.IdempotencyKey(job.ID),
	})
	if err != nil {
		if mapped := classifyQueueSubmitError(err); mapped != nil {
			return mapped
		}
		return fmt.Errorf("renderinggen submit: %w", err)
	}
	return nil
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (renderqueue.RenderQueueJob, error) {
	job, err := c.q.Get(ctx, id)
	if err != nil {
		return renderqueue.RenderQueueJob{}, fmt.Errorf("renderinggen get: %w", err)
	}
	return toScriptJob(job), nil
}

func (c *Client) Children(ctx context.Context, parentID string) ([]renderqueue.RenderQueueJob, error) {
	if c == nil || c.q == nil {
		return nil, fmt.Errorf("renderinggen children: client is not configured")
	}
	jobs, err := c.q.Children(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("renderinggen children: %w", err)
	}
	out := make([]renderqueue.RenderQueueJob, len(jobs))
	for i := range jobs {
		out[i] = toScriptJob(jobs[i])
	}
	return out, nil
}

// WaitTerminal implements renderqueue.RenderQueueWaiter over the queue's
// job-status long poll (GET /jobs/{id}/wait). The enqueuer observes a
// terminal render at the state transition instead of at the next polling
// tick; the queue client degrades to polling on its own when the wait route
// is absent (older server).
func (c *Client) WaitTerminal(ctx context.Context, id string) (renderqueue.RenderQueueJob, error) {
	if c == nil || c.q == nil {
		return renderqueue.RenderQueueJob{}, fmt.Errorf("renderinggen wait: client is not configured")
	}
	job, err := c.q.WaitTerminal(ctx, id)
	if err != nil {
		return renderqueue.RenderQueueJob{}, fmt.Errorf("renderinggen wait: %w", err)
	}
	return toScriptJob(job), nil
}

// WaitProgress performs one bounded server-side long poll and returns the
// current job, including Progress, even when the wait window elapses without a
// terminal state. WaitRenderQueueTerminalWatched uses it to observe stalls
// without a second GET at every slice boundary (CR-03).
func (c *Client) WaitProgress(ctx context.Context, id string) (renderqueue.RenderQueueJob, error) {
	if c == nil || c.q == nil {
		return renderqueue.RenderQueueJob{}, fmt.Errorf("renderinggen progress wait: client is not configured")
	}
	job, err := c.q.WaitProgress(ctx, id)
	if err != nil {
		return renderqueue.RenderQueueJob{}, fmt.Errorf("renderinggen progress wait: %w", err)
	}
	return toScriptJob(job), nil
}

// toScriptJob maps a queue wire job onto the capability's typed job. It is the
// single projection shared by Get, WaitTerminal and WaitProgress.
func toScriptJob(job queueclient.Job) renderqueue.RenderQueueJob {
	return renderqueue.RenderQueueJob{
		ID:                    job.ID,
		JobType:               job.JobType,
		ParentJobID:           job.ParentJobID,
		ChunkIndex:            job.ChunkIndex,
		FrameRange:            fromQueueFrameRange(job.FrameRange),
		PreparedRenderPackage: fromQueuePreparedRenderPackage(job.PreparedRenderPackage),
		OverlaySpec:           job.RenderPlan,
		Assets:                fromQueueAssets(job.Assets),
		RequireGPU:            job.RequireGPU,
		State:                 string(job.State),
		FailReason:            job.FailReason,
		Artifact:              toScriptArtifact(job.Artifact),
		// Queue-owned lifecycle timestamps: the admission/service split the
		// producer cannot observe from the artifact alone.
		QueuedAt:    job.QueuedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		Progress:    fromQueueProgress(job.Progress),
	}
}

// fromQueueProgress maps the queue's progress body onto the capability type.
func fromQueueProgress(in *queueclient.Progress) *renderqueue.RenderQueueProgress {
	if in == nil {
		return nil
	}
	return &renderqueue.RenderQueueProgress{
		FramesDone: in.FramesDone, TotalFrames: in.TotalFrames,
		LastFrameAt: in.LastFrameAt, Worker: in.Worker,
	}
}

// Cancel releases the GPU lane of a render that the settle watchdog has proven
// stalled. It implements renderqueue.RenderQueueCanceller.
func (c *Client) Cancel(ctx context.Context, id string) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen cancel: client is not configured")
	}
	if err := c.q.Cancel(ctx, id); err != nil {
		return fmt.Errorf("renderinggen cancel %s: %w", id, err)
	}
	return nil
}

func fromQueueFrameRange(in *queueclient.FrameRange) *renderqueue.RenderFrameRange {
	if in == nil {
		return nil
	}
	return &renderqueue.RenderFrameRange{Start: in.Start, End: in.End}
}

// Retry resets a failed job back to pending state.
func (c *Client) Retry(ctx context.Context, id string) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen retry: client is not configured")
	}
	return c.q.Retry(ctx, id)
}

func toQueueAssets(in []renderqueue.RenderQueueAsset) []queueclient.AssetRef {
	if in == nil {
		return nil
	}
	out := make([]queueclient.AssetRef, len(in))
	for i, a := range in {
		// The canonical identity supplies the digest, so the wire never sees a
		// second spelling of one content address. Ref() already returns
		// canonicalised values; re-canonicalising here would be a second
		// (silently different) owner of the spelling rule.
		out[i] = queueclient.AssetRef{Hash: a.Ref().SHA256, LogicalPath: a.URL, SourceURL: a.SourceURL}
	}
	return out
}

func fromQueueAssets(in []queueclient.AssetRef) []renderqueue.RenderQueueAsset {
	if in == nil {
		return nil
	}
	out := make([]renderqueue.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = renderqueue.NewRenderQueueAsset(
			kernelasset.Ref{AssetID: a.LogicalPath, SHA256: a.Hash}, a.LogicalPath, a.SourceURL)
	}
	return out
}
