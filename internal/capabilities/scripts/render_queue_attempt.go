package scriptgeneration

import (
	"context"
	"fmt"
	"log"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

func observeCompletionWait(wait RenderCompletionMetrics, outcome string) {
	if !wait.WaitStartedAt.IsZero() && !wait.WaitFinishedAt.IsZero() && !wait.WaitFinishedAt.Before(wait.WaitStartedAt) {
		observability.OverlayCompletionWaitSeconds.WithLabelValues(outcome).Observe(wait.WaitFinishedAt.Sub(wait.WaitStartedAt).Seconds())
	}
}

func optionalTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// recordQueueAttempt preserves producer and queue-owned timestamps even when
// the queue submit/wait path fails. Missing queue timestamps remain NULL; no
// zero timestamp is manufactured. Recorder failures are logged and returned so
// the caller can retain the original failure and the recording fault together.
func (e *QueueRenderEnqueuer) recordQueueAttempt(
	ctx context.Context,
	jobID string,
	plan capoverlay.OverlayPlan,
	metadata *overlayItemPublicationMetadata,
	wait RenderCompletionMetrics,
	submitStartedAt time.Time,
	submitAcceptedAt time.Time,
	job *RenderQueueJob,
	artifactAvailableAt time.Time,
	outcome string,
) error {
	if e == nil || e.recorder == nil {
		return nil
	}
	var artifact *RenderArtifact
	if job != nil {
		artifact = job.Artifact
	}
	attempt := BuildRenderAttemptAnalyticsWithWait(jobID, plan, artifact, wait)
	if metadata != nil && metadata.ItemID != "" {
		attempt.ItemID = metadata.ItemID
	}
	attempt.SubmitStartedAt = optionalTimePointer(submitStartedAt)
	attempt.SubmitAcceptedAt = optionalTimePointer(submitAcceptedAt)
	attempt.WaitStartedAt = optionalTimePointer(wait.WaitStartedAt)
	attempt.WaitFinishedAt = optionalTimePointer(wait.WaitFinishedAt)
	if job != nil {
		attempt.QueueQueuedAt = optionalTimePointer(job.QueuedAt)
		attempt.QueueStartedAt = optionalTimePointer(job.StartedAt)
		attempt.QueueCompletedAt = optionalTimePointer(job.CompletedAt)
	}
	attempt.ArtifactAvailableAt = optionalTimePointer(artifactAvailableAt)
	attempt.Outcome = outcome
	if err := e.recorder.RecordAttempt(ctx, attempt); err != nil {
		log.Printf("record render attempt analytics attempt_id=%s: %v", jobID, err)
		return fmt.Errorf("record render attempt analytics: %w", err)
	}
	return nil
}
