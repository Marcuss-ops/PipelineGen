package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

type recordingOverlayPublisher struct{ calls int }

func (p *recordingOverlayPublisher) PublishOverlay(context.Context, OverlayPublicationSpec, *contract.RenderArtifact) error {
	p.calls++
	return nil
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
