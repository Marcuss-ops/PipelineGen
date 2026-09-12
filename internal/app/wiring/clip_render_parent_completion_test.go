package wiring

import (
	"context"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// recordingParentFinalizer captures the parents the notifier decides to
// finalise, so the ownership filter is provable without a jobs service.
type recordingParentFinalizer struct {
	calls []string
	err   error
}

func (f *recordingParentFinalizer) FinalizeParent(_ context.Context, parentJobID string) error {
	f.calls = append(f.calls, parentJobID)
	return f.err
}

// TestClipRenderParentNotifierOwnsOnlyClipRenderChildren pins the filter that
// makes attaching the port to every worker safe: a child of another capability
// must never be routed into the clip.render aggregation semantics.
func TestClipRenderParentNotifierOwnsOnlyClipRenderChildren(t *testing.T) {
	finalizer := &recordingParentFinalizer{}
	notifier := &clipRenderParentNotifier{agg: finalizer}

	cases := []struct {
		name  string
		child *job.Job
		want  int
	}{
		{"clip render child with a parent", &job.Job{ID: "c1", Type: cliprender.TypeClipRender, ParentJobID: "p1"}, 1},
		{"clip render child without a parent", &job.Job{ID: "c2", Type: cliprender.TypeClipRender}, 0},
		{"script child", &job.Job{ID: "c3", Type: "script.generate", ParentJobID: "p3"}, 0},
		{"voiceover child", &job.Job{ID: "c4", Type: "voiceover.generate", ParentJobID: "p4"}, 0},
		{"nil child", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(finalizer.calls)
			if err := notifier.NotifyChildTerminal(context.Background(), tc.child); err != nil {
				t.Fatalf("notify: %v", err)
			}
			if got := len(finalizer.calls) - before; got != tc.want {
				t.Fatalf("finalize calls = %d, want %d", got, tc.want)
			}
		})
	}
	if len(finalizer.calls) != 1 || finalizer.calls[0] != "p1" {
		t.Fatalf("finalized parents = %v, want exactly [p1]", finalizer.calls)
	}
}

// TestClipRenderParentNotifierWithoutAggregatorIsNoop pins nil-tolerance: the
// port can be attached unconditionally, and a deployment that did not compose
// the aggregator simply does nothing.
func TestClipRenderParentNotifierWithoutAggregatorIsNoop(t *testing.T) {
	notifier := &clipRenderParentNotifier{}
	if err := notifier.NotifyChildTerminal(context.Background(), &job.Job{
		ID: "c1", Type: cliprender.TypeClipRender, ParentJobID: "p1",
	}); err != nil {
		t.Fatalf("nil aggregator must be a no-op, got %v", err)
	}
	var nilNotifier *clipRenderParentNotifier
	if err := nilNotifier.NotifyChildTerminal(context.Background(), nil); err != nil {
		t.Fatalf("nil notifier must be a no-op, got %v", err)
	}
}

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
