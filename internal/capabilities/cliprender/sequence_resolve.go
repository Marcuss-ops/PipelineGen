package cliprender

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
	"go.uber.org/zap"
)

type resolvedSequenceClip struct {
	assetID    string
	path       string
	durationMS int64
	transition *clipsequence.Transition
}

// handleSequence runs one assembly attempt.
//
// Waiting is expressed with job.DeferredAfter: a clip whose render has not
// settled hands the attempt back without spending retry budget, so a batch
func (w *Worker) resolveBatchRecovery(ctx context.Context, req SequenceRequest, assembly SequenceAssembly) ([]SequenceClip, error) {
	clips := append([]SequenceClip(nil), req.Clips...)
	if req.Recovery == nil {
		return clips, nil
	}
	if assembly.Batch.List == nil || assembly.Batch.Enqueue == nil {
		return nil, fmt.Errorf("clip.render: batch recovery requires job list/enqueue ports")
	}
	jobType := TypeClipRender
	jobs, err := assembly.Batch.List(ctx, job.Filter{
		Type:              &jobType,
		CorrelationPrefix: req.Recovery.BatchID + ":",
		Limit:             MaxClipRenderBatchItems*2 + 2,
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: list batch %s jobs: %w", req.Recovery.BatchID, err)
	}
	byCorrelation := make(map[string]string, len(jobs))
	for i := range jobs {
		byCorrelation[jobs[i].CorrelationID] = jobs[i].ID
	}
	for _, item := range req.Recovery.Items {
		position := item.Position
		if position < 0 || position >= len(clips) {
			return nil, fmt.Errorf("clip.render: batch recovery position %d is outside 0..%d", position, len(clips)-1)
		}
		request := item.Request
		request.Normalize()
		fingerprint, err := request.Fingerprint()
		if err != nil {
			return nil, fmt.Errorf("clip.render: batch recovery position %d fingerprint: %w", position, err)
		}
		correlation := req.Recovery.BatchID + ":" + digestPrefix(fingerprint)
		jobID := byCorrelation[correlation]
		if jobID == "" {
			enqueued, enqueueErr := assembly.Batch.Enqueue(ctx, &job.EnqueueRequest{
				Type:          TypeClipRender,
				CorrelationID: correlation,
				Payload:       &request,
				Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
			})
			if enqueueErr != nil {
				return nil, fmt.Errorf("clip.render: recover batch item %d: %w", position, enqueueErr)
			}
			if enqueued == nil || strings.TrimSpace(enqueued.ID) == "" {
				return nil, fmt.Errorf("clip.render: recover batch item %d: broker returned an empty job", position)
			}
			jobID = enqueued.ID
			byCorrelation[correlation] = jobID
			w.log.Info("clip.render.sequence.recovered_item",
				zap.Int("position", position),
				zap.String("job_id", jobID),
				zap.String("batch_id", req.Recovery.BatchID))
		}
		// Duplicate items share one canonical position: every clip that names
		// it must receive the same job id, not just the first occurrence.
		for i := range clips {
			if clips[i].BatchPosition != nil && *clips[i].BatchPosition == position {
				clips[i].JobID = jobID
				clips[i].BatchPosition = nil
			}
		}
	}
	for i := range clips {
		if clips[i].BatchPosition != nil {
			return nil, fmt.Errorf("clip.render: batch recovery left clip %d at position %d unresolved", i, *clips[i].BatchPosition)
		}
	}
	return clips, nil
}

// resolveSequenceClips turns each position into local bytes. It returns a
// non-empty `waiting` message when a sibling job has not settled yet.
func (w *Worker) resolveSequenceClips(ctx context.Context, req SequenceRequest, assembly SequenceAssembly) ([]resolvedSequenceClip, string, error) {
	// Pass 1: inspect every sibling BEFORE materializing anything. A later
	// running clip must not make this attempt download the earlier ready clips
	// again; the previous single pass materialized position 0..N-1 and only then
	// discovered position N was still rendering (CR-04).
	for i, clip := range req.Clips {
		if clip.JobID == "" {
			continue
		}
		child, err := assembly.Jobs.Get(ctx, clip.JobID)
		if err != nil {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: load job %s: %w", i, clip.JobID, err)
		}
		if child == nil {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s does not exist", i, clip.JobID)
		}
		if !assembly.Jobs.IsTerminal(child.Status) {
			return nil, fmt.Sprintf("clip %d (job %s) is still %s", i, clip.JobID, child.Status), nil
		}
		if child.Status != job.StatusSucceeded {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s ended %s: %s", i, clip.JobID, child.Status, child.Error)
		}
	}

	// Pass 2: every sibling is terminal, so materialize each position once.
	clips := make([]resolvedSequenceClip, len(req.Clips))
	for i, clip := range req.Clips {
		resolved := resolvedSequenceClip{assetID: clip.AssetID, path: clip.Path, durationMS: clip.DurationMS, transition: clip.TransitionIn}
		if clip.JobID != "" {
			child, err := assembly.Jobs.Get(ctx, clip.JobID)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: load job %s: %w", i, clip.JobID, err)
			}
			if child == nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s does not exist", i, clip.JobID)
			}
			if !assembly.Jobs.IsTerminal(child.Status) {
				return nil, fmt.Sprintf("clip %d (job %s) is still %s", i, clip.JobID, child.Status), nil
			}
			if child.Status != job.StatusSucceeded {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s ended %s: %s", i, clip.JobID, child.Status, child.Error)
			}
			assetID, durationMS, err := sequenceChildArtifact(child)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: %w", i, err)
			}
			resolved.assetID = assetID
			if resolved.durationMS == 0 {
				resolved.durationMS = durationMS
			}
		}
		if resolved.path == "" {
			if resolved.assetID == "" {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d has no resolvable artifact", i)
			}
			materialized, err := assembly.Assets.MaterializeAsset(ctx, resolved.assetID)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: materialize %s: %w", i, resolved.assetID, err)
			}
			if materialized == nil || materialized.LocalPath == "" {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: asset %s has no local bytes", i, resolved.assetID)
			}
			resolved.path = materialized.LocalPath
			if resolved.durationMS == 0 {
				resolved.durationMS = materialized.DurationMS
			}
			if resolved.assetID == "" {
				resolved.assetID = materialized.AssetID
			}
		}
		if resolved.durationMS <= 0 {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: duration_ms is required for a path (or pass an asset/job that certifies it)", i)
		}
		clips[i] = resolved
	}
	return clips, "", nil
}

// sequenceChildArtifact reads what a finished clip.render job produced: the
// published asset identity and its certified length.
func sequenceChildArtifact(child *job.Job) (string, int64, error) {
	var result struct {
		Asset *struct {
			AssetID string `json:"asset_id"`
		} `json:"asset"`
		Render *struct {
			DurationSec float64 `json:"duration_sec"`
		} `json:"render"`
	}
	if len(child.Result) > 0 {
		if err := json.Unmarshal(child.Result, &result); err != nil {
			return "", 0, fmt.Errorf("job %s result is not readable: %w", child.ID, err)
		}
	}
	if result.Asset == nil || result.Asset.AssetID == "" {
		return "", 0, fmt.Errorf("job %s reported no published asset", child.ID)
	}
	var durationMS int64
	if result.Render != nil && result.Render.DurationSec > 0 {
		durationMS = int64(result.Render.DurationSec*1000 + 0.5)
	}
	return result.Asset.AssetID, durationMS, nil
}
