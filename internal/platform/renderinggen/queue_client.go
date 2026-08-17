// Package renderinggen adapts the central RenderingGen queue's public client
// to the script-generation capability. PipelineGen no longer owns the HTTP
// contract: it delegates to github.com/Marcuss-ops/RenderginGen/queue/client
// and only maps between the capability domain types and the queue's wire
// types, so the wire format can never drift between the two codebases.
package renderinggen

import (
	"context"
	"errors"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// Client adapts the queue's public client to scriptgen.RenderQueueClient.
type Client struct {
	q *queueclient.Client
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{q: queueclient.New(baseURL)}
}

// Submit enqueues a job. A 409 (job already exists) is surfaced as
// scriptgen.ErrJobExists so the enqueuer treats replays as idempotent.
func (c *Client) Submit(ctx context.Context, job scriptgen.RenderQueueJob) error {
	err := c.q.Submit(ctx, queueclient.Job{
		ID:         job.ID,
		JobType:    job.JobType,
		RenderPlan: job.OverlaySpec,
		Assets:     toQueueAssets(job.Assets),
	})
	if errors.Is(err, queueclient.ErrJobExists) {
		return fmt.Errorf("%w: job %s", scriptgen.ErrJobExists, job.ID)
	}
	if err != nil {
		return fmt.Errorf("renderinggen submit: %w", err)
	}
	return nil
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (scriptgen.RenderQueueJob, error) {
	job, err := c.q.Get(ctx, id)
	if err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get: %w", err)
	}
	return scriptgen.RenderQueueJob{
		ID:          job.ID,
		OverlaySpec: job.RenderPlan,
		Assets:      fromQueueAssets(job.Assets),
		State:       string(job.State),
		FailReason:  job.FailReason,
		Artifact:    toScriptArtifact(job.Artifact),
	}, nil
}

func toQueueAssets(in []scriptgen.RenderQueueAsset) []queueclient.AssetRef {
	if in == nil {
		return nil
	}
	out := make([]queueclient.AssetRef, len(in))
	for i, a := range in {
		out[i] = queueclient.AssetRef{Hash: a.Hash, LogicalPath: a.URL}
	}
	return out
}

func fromQueueAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	if in == nil {
		return nil
	}
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.RenderQueueAsset{Hash: a.Hash, URL: a.LogicalPath}
	}
	return out
}

func toScriptArtifact(in *queueclient.Artifact) *scriptgen.RenderArtifact {
	if in == nil {
		return nil
	}
	return &scriptgen.RenderArtifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		URL:                in.ArtifactURL,
		SHA256:             in.ArtifactHash,
		MimeType:           in.ContentType,
		SizeBytes:          in.SizeBytes,
		Width:              in.Width,
		Height:             in.Height,
		FPSNum:             in.FPSNum,
		FPSDen:             in.FPSDen,
		FrameCount:         in.FrameCount,
		DurationUS:         in.DurationUS,
		ProfileID:          in.ProfileID,
		CopyEligible:       in.CopyEligible,
		Codec:              in.Codec,
		CodecProfile:       in.CodecProfile,
		ClosedGOP:          in.ClosedGOP,
		FirstFrameKeyframe: in.FirstFrameKeyframe,
		RenderMS:           metricMillis(in.Metrics, "render_ms"),
		EncodeMS:           metricMillis(in.Metrics, "encode_ms"),
		DriveFileID:        in.DriveFileID,
		DriveLink:          in.DriveLink,
	}
}

// metricMillis reads a millisecond metric from the worker's metrics map,
// rounding down to whole milliseconds. Absent keys yield 0 (unreported).
func metricMillis(m map[string]float64, key string) int64 {
	if m == nil {
		return 0
	}
	return int64(m[key])
}

var _ scriptgen.RenderQueueClient = (*Client)(nil)
