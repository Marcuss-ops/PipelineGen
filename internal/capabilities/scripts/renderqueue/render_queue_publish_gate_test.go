package renderqueue

import (
	"context"
	"strings"
	"testing"
	"time"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

type recordingOverlayPublisher struct{ calls int }

func (p *recordingOverlayPublisher) PublishOverlay(context.Context, OverlayPublicationSpec, *contract.RenderArtifact) error {
	p.calls++
	return nil
}

// blockingOverlayPublisher models a stalled Drive: if the publication path
// ever reaches it, the call waits until the context expires. The object-store
// gate must return before that happens, so a Drive slowdown cannot block the
// render/handoff.
type blockingOverlayPublisher struct {
	called chan struct{}
}

func (p *blockingOverlayPublisher) PublishOverlay(ctx context.Context, _ OverlayPublicationSpec, _ *contract.RenderArtifact) error {
	select {
	case <-p.called:
	default:
		close(p.called)
	}
	<-ctx.Done()
	return ctx.Err()
}

// Every overlay render is an intermediate: the certified object-store locator
// is what the local join and the final handoff read. The publication step must
// return before the Drive publisher, even when one is wired.
func TestPublishChrononArtifactsKeepsObjectStoreIntermediatesOffDrive(t *testing.T) {
	publisher := &recordingOverlayPublisher{}
	enqueuer := &QueueRenderEnqueuer{publisher: publisher}
	sha := strings.Repeat("a", 64)
	artifact := &contract.RenderArtifact{
		ID: "artifact-1", SHA256: sha, StorageKey: sha, SizeBytes: 1024,
		URL: "https://store.example/objects/" + sha,
	}
	plan := capmodel.OverlayPlan{PlanID: "plan-1", RequireObjectStoreBeforeReturn: true}

	err := enqueuer.publishChrononArtifacts(
		context.Background(), plan,
		RenderQueueJob{ID: "render-1", Artifact: artifact},
		"render-1", nil, plan, RenderCompletionMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 0 {
		t.Fatalf("Drive publisher called %d times for an object-store intermediate", publisher.calls)
	}
}

// TestPublishChrononArtifactsDoesNotWaitForAStalledDrive pins the concrete
// slowdown/error case: even when the publisher is wired and would block, an
// object-store intermediate returns immediately with no Drive call.
func TestPublishChrononArtifactsDoesNotWaitForAStalledDrive(t *testing.T) {
	publisher := &blockingOverlayPublisher{called: make(chan struct{})}
	enqueuer := &QueueRenderEnqueuer{publisher: publisher}
	sha := strings.Repeat("c", 64)
	artifact := &contract.RenderArtifact{
		ID: "artifact-1", SHA256: sha, StorageKey: sha, SizeBytes: 1024,
		URL: "https://store.example/objects/" + sha,
	}
	plan := capmodel.OverlayPlan{PlanID: "plan-1", RequireObjectStoreBeforeReturn: true}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- enqueuer.publishChrononArtifacts(
			ctx, plan,
			RenderQueueJob{ID: "render-1", Artifact: artifact},
			"render-1", nil, plan, RenderCompletionMetrics{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("object-store publication returned %v, want success without Drive", err)
		}
	case <-ctx.Done():
		t.Fatal("publication waited on the stalled Drive publisher")
	}
	select {
	case <-publisher.called:
		t.Fatal("Drive publisher was called for an object-store intermediate")
	default:
	}
}

// The gate is also the certification: an artifact without the content-addressed
// object-store identity fails closed instead of being accepted as "published".
func TestPublishChrononArtifactsRejectsUncertifiedObjectStoreArtifact(t *testing.T) {
	enqueuer := &QueueRenderEnqueuer{publisher: &recordingOverlayPublisher{}}
	artifact := &contract.RenderArtifact{ID: "artifact-1", SHA256: strings.Repeat("b", 64), SizeBytes: 10, URL: "https://store.example/objects/x"}
	plan := capmodel.OverlayPlan{PlanID: "plan-1", RequireObjectStoreBeforeReturn: true}

	if err := enqueuer.publishChrononArtifacts(
		context.Background(), plan,
		RenderQueueJob{ID: "render-1", Artifact: artifact},
		"render-1", nil, plan, RenderCompletionMetrics{}); err == nil {
		t.Fatal("an intermediate without StorageKey == SHA256 must fail closed")
	}
}
