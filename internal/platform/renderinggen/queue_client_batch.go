package renderinggen

import (
	"context"
	"fmt"
	"strings"

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
	identities, err := scriptgen.RenderRequestFamilyIdentities(jobs)
	if err != nil {
		return fmt.Errorf("renderinggen submit batch: request identity: %w", err)
	}
	if c.prefetch != nil {
		assets := uniqueBatchAssets(jobs)
		if err := c.prefetch.Prefetch(ctx, assets); err != nil {
			return fmt.Errorf("renderinggen asset prefetch: %w", err)
		}
	}
	wire := make([]queueclient.Job, len(jobs))
	for i, job := range jobs {
		wire[i] = queueclient.Job{
			ID: job.ID, JobType: job.JobType, ParentJobID: job.ParentJobID,
			ChunkIndex: job.ChunkIndex, FrameRange: toQueueFrameRange(job.FrameRange),
			PreparedRenderPackage: toQueuePreparedRenderPackage(job.PreparedRenderPackage),
			RenderPlan:            job.OverlaySpec, Assets: toQueueAssets(job.Assets),
			IdempotencyKey: identities[i].IdempotencyKey(job.ID),
		}
	}
	if err := c.q.SubmitBatch(ctx, wire); err != nil {
		if mapped := classifyQueueSubmitError(err); mapped != nil {
			return fmt.Errorf("renderinggen submit batch: %w", mapped)
		}
		return fmt.Errorf("renderinggen submit batch: %w", err)
	}
	return nil
}

// uniqueBatchAssets stages each content address once for the whole atomic
// family. Chunk children commonly share the same manifest; prefetching each
// child separately repeats object-store HEADs (and can repeat remote reads on
// stores without a reliable HEAD path) before the batch is submitted.
func uniqueBatchAssets(jobs []scriptgen.RenderQueueJob) []scriptgen.RenderQueueAsset {
	assets := make([]scriptgen.RenderQueueAsset, 0)
	indexes := make(map[string]int)
	for _, job := range jobs {
		for _, asset := range job.Assets {
			hash := asset.Ref().SHA256
			if index, exists := indexes[hash]; exists {
				// Keep the first logical path, but merge materialization hints so
				// one incomplete reference cannot hide a usable source carried by
				// another member of the same family.
				current := &assets[index]
				if current.LocalPath == "" {
					current.LocalPath = asset.LocalPath
				}
				if current.SourceURL == "" {
					current.SourceURL = asset.SourceURL
				}
				if current.SourceURL == "" && current.URL != asset.URL && strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.URL)), "http") {
					current.SourceURL = asset.URL
				}
				continue
			}
			indexes[hash] = len(assets)
			assets = append(assets, asset)
		}
	}
	return assets
}

func toQueueFrameRange(in *scriptgen.RenderFrameRange) *queueclient.FrameRange {
	if in == nil {
		return nil
	}
	return &queueclient.FrameRange{Start: in.Start, End: in.End}
}
