package wiring

import (
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

// TestClipRenderParentAggregatorIsShared pins the single-authority rule: the
// notifier and the recovery sweeper must finalise through the same instance, so
// an already-built aggregator on the root is returned rather than replaced.
func TestClipRenderParentAggregatorIsShared(t *testing.T) {
	cached := cliprender.NewParentAggregator(nil, zap.NewNop(), time.Second)
	root := &ComposeRoot{ClipRenderParentAggregator: cached}
	if got := clipRenderParentAggregator(root, zap.NewNop()); got != cached {
		t.Fatal("an already-built clip.render parent aggregator must be reused, not replaced")
	}
}

// TestClipRenderParentAggregatorWithoutJobsServiceIsNil pins the fail-closed
// guard: no jobs service means no aggregator, and therefore neither the
// notifier nor the sweeper is composed with a half-built dependency.
func TestClipRenderParentAggregatorWithoutJobsServiceIsNil(t *testing.T) {
	if got := clipRenderParentAggregator(&ComposeRoot{}, zap.NewNop()); got != nil {
		t.Fatalf("aggregator = %v, want nil without a jobs service", got)
	}
	if got := clipRenderParentAggregator(&ComposeRoot{Jobs: &JobsBundle{}}, zap.NewNop()); got != nil {
		t.Fatalf("aggregator = %v, want nil without a jobs service", got)
	}
	if got := clipRenderParentAggregator(nil, zap.NewNop()); got != nil {
		t.Fatalf("aggregator = %v, want nil without a composition root", got)
	}
}
