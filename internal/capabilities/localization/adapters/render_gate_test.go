package adapters

import (
	"context"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"go.uber.org/zap"
)

type gateObservingExecutor struct {
	gate               chan struct{}
	outcome            *cliprender.RenderOutcome
	settleGateLen      int
	materializeGateLen int
}

func (f *gateObservingExecutor) Submit(context.Context, cliprender.ClipRenderPlanV1) error {
	return nil
}

func (f *gateObservingExecutor) Settle(context.Context, cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	f.settleGateLen = len(f.gate)
	return f.outcome, nil
}

func (f *gateObservingExecutor) Materialize(_ context.Context, outcome *cliprender.RenderOutcome, destPath string) (string, error) {
	f.materializeGateLen = len(f.gate)
	outcome.OutputPath = destPath
	return destPath, nil
}

// TestRenderPlanExecutorReleasesGPUGateBeforeMaterialize pins CR-14: the GPU
// admission slot covers Submit/Settle, not the 22–33 MB object-store download.
func TestRenderPlanExecutorReleasesGPUGateBeforeMaterialize(t *testing.T) {
	plan, _, sha, size := localizedRenderFixture(t)
	gate := make(chan struct{}, 1)
	exec := &gateObservingExecutor{
		gate: gate,
		outcome: &cliprender.RenderOutcome{
			SizeBytes: size, SHA256: sha, DurationSec: 8.432,
		},
	}
	adapter := NewRenderPlanExecutor(exec, mediaexec.VideoProfile{}, zap.NewNop())
	adapter.SetGPUGate(gate)
	if _, err := adapter.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exec.settleGateLen != 1 {
		t.Fatalf("settle held %d gate slots, want 1", exec.settleGateLen)
	}
	if exec.materializeGateLen != 0 {
		t.Fatalf("materialize held %d gate slots, want 0 (gate must be released before download)", exec.materializeGateLen)
	}
}
