package scriptgeneration

// overlay_render_conflict_recovery_test.go pins the submit idempotency rule of
// the overlay enqueuer. The queue job id is the sealed plan revision, and the
// overlay path submits no idempotency key, so a retried submit of the SAME plan
// answers 409 instead of an idempotent 200. The enqueuer must recognise that
// replay from the stored overlay spec and continue waiting on the existing job;
// a DIFFERENT spec under the same id must stay a fail-closed conflict.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// conflictReplayQueueClient rejects the first submit as an identity conflict
// and serves the job the queue already owns, exactly like a producer retrying a
// plan the queue stored on an earlier attempt.
type conflictReplayQueueClient struct {
	submits   int
	submitted []byte
	// existing is the spec the queue already owns under the submitted id.
	// Empty means the queue stored the same request (the retried replay).
	existing []byte
	artifact RenderArtifact
}

func (c *conflictReplayQueueClient) Submit(_ context.Context, job RenderQueueJob) error {
	c.submits++
	c.submitted = append([]byte(nil), job.OverlaySpec...)
	return ErrRenderRequestConflict
}

func (c *conflictReplayQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	spec := c.existing
	if len(spec) == 0 {
		spec = c.submitted
	}
	artifact := c.artifact
	return RenderQueueJob{
		ID:          id,
		State:       RenderQueueStateCompleted,
		OverlaySpec: append([]byte(nil), spec...),
		Artifact:    &artifact,
	}, nil
}

func conflictReplayArtifact() RenderArtifact {
	return RenderArtifact{
		SHA256:     strings.Repeat("b", 64),
		SizeBytes:  8192,
		URL:        "https://objects.example/objects/" + strings.Repeat("b", 64),
		DurationUS: 2_000_000,
		Width:      1920, Height: 1080, FPSNum: 30, FPSDen: 1,
	}
}

func TestOverlayEnqueueRecoversIdenticalReplayConflict(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	client := &conflictReplayQueueClient{artifact: conflictReplayArtifact()}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatalf("NewQueueRenderEnqueuer: %v", err)
	}
	enqueuer.SetPollInterval(time.Millisecond)

	plan := manifestTestPlan()
	for i := range plan.Items {
		plan.Items[i].StartMs = plan.Items[i].StartUS / 1000
		plan.Items[i].EndMs = (plan.Items[i].StartUS + plan.Items[i].DurationUS + 999) / 1000
	}
	ref, err := enqueuer.enqueueChrononPlan(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("identical replay must be recovered, got error: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("submissions = %d, want exactly 1 (the replay must not resubmit)", client.submits)
	}
	if ref.Artifact == nil || ref.Artifact.SHA256 != client.artifact.SHA256 {
		t.Fatalf("artifact = %+v, want the queue-owned certified artifact %s", ref.Artifact, client.artifact.SHA256)
	}
}

func TestOverlayEnqueueKeepsConflictWhenStoredSpecDiffers(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	client := &conflictReplayQueueClient{artifact: conflictReplayArtifact()}
	// The queue owns a DIFFERENT render under the same id: admitting it would
	// publish bytes for content this run never asked for.
	client.existing = []byte(`{"schema_version":"renderinggen.overlay-plan.v1","items":[{"id":"other"}]}`)
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatalf("NewQueueRenderEnqueuer: %v", err)
	}
	enqueuer.SetPollInterval(time.Millisecond)

	plan := manifestTestPlan()
	for i := range plan.Items {
		plan.Items[i].StartMs = plan.Items[i].StartUS / 1000
		plan.Items[i].EndMs = (plan.Items[i].StartUS + plan.Items[i].DurationUS + 999) / 1000
	}
	if _, err := enqueuer.enqueueChrononPlan(context.Background(), plan, nil); !errors.Is(err, ErrRenderRequestConflict) {
		t.Fatalf("different stored spec must stay a conflict, got %v", err)
	}
}
