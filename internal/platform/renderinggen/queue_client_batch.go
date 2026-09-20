package renderinggen

import (
	"context"
	"errors"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// SubmitBatch maps and submits a complete chunk family through RenderingGen's
// atomic endpoint. Prefetch runs before the transaction so a materialization
// failure cannot leave a family whose children can never render.
func (c *Client) SubmitBatch(ctx context.Context, jobs []scriptgen.RenderQueueJob) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen submit batch: client is not configured")
	}
	if len(jobs) == 0 {
		return fmt.Errorf("renderinggen submit batch: jobs are required")
	}
	for _, job := range jobs {
		if c.prefetch != nil {
			if err := c.prefetch.Prefetch(ctx, job.Assets); err != nil {
				return fmt.Errorf("renderinggen asset prefetch: %w", err)
			}
		}
	}
	wire := make([]queueclient.Job, len(jobs))
	for i, job := range jobs {
		wire[i] = queueclient.Job{
			ID: job.ID, JobType: job.JobType, ParentJobID: job.ParentJobID,
			ChunkIndex: job.ChunkIndex, FrameRange: toQueueFrameRange(job.FrameRange),
			RenderPlan: job.OverlaySpec, Assets: toQueueAssets(job.Assets),
		}
	}
	if err := c.q.SubmitBatch(ctx, wire); err != nil {
		if errors.Is(err, queueclient.ErrJobExists) {
			return fmt.Errorf("%w: chunk family", scriptgen.ErrJobExists)
		}
		return fmt.Errorf("renderinggen submit batch: %w", err)
	}
	return nil
}

func toQueueFrameRange(in *scriptgen.RenderFrameRange) *queueclient.FrameRange {
	if in == nil {
		return nil
	}
	return &queueclient.FrameRange{Start: in.Start, End: in.End}
}
