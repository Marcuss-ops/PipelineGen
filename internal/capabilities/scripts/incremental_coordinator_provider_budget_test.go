package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type blockingProviderResolver struct{}

func (blockingProviderResolver) ResolveProviders(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	<-ctx.Done()
	return segment, ctx.Err()
}

func TestSearchProvidersDegradesWhenResolveBudgetExpires(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(blockingProviderResolver{})
	coordinator.providerResolveBudget = 25 * time.Millisecond

	started := time.Now()
	result, err := coordinator.searchProviders(context.Background(), scriptpkg.VidRushSegmentResult{SegmentID: "seg-budget"})
	if err != nil {
		t.Fatalf("searchProviders returned a fatal error after the bounded resolve budget: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("searchProviders took %s, want the 25ms test budget to end the stage", elapsed)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "provider resolve exceeded") {
		t.Fatalf("scene warnings = %v, want the degraded-provider warning", result.Warnings)
	}
}

func TestSearchProvidersPropagatesParentCancellation(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(blockingProviderResolver{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.searchProviders(ctx, scriptpkg.VidRushSegmentResult{SegmentID: "seg-canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("searchProviders error = %v, want context.Canceled (degradation must only apply to the stage budget)", err)
	}
}

func TestProviderResolveBudgetDefaultsToTwoMinutes(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	if got := coordinator.providerResolveTimeout(); got != 2*time.Minute {
		t.Fatalf("providerResolveTimeout = %s, want 2m", got)
	}
}

type countingBlockingProviderResolver struct {
	calls atomic.Int32
}

func (r *countingBlockingProviderResolver) ResolveProviders(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return segment, ctx.Err()
}

// TestProviderResolveBudgetIsRunScoped pins the semantics that made the old
// per-scene cap ineffective: the budget is started by the FIRST provider
// fan-out of the run and the next scene does not get a fresh window.
func TestProviderResolveBudgetIsRunScoped(t *testing.T) {
	resolver := &countingBlockingProviderResolver{}
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(resolver)
	coordinator.providerResolveBudget = 200 * time.Millisecond

	first, err := coordinator.searchProviders(context.Background(), scriptpkg.VidRushSegmentResult{SegmentID: "seg-first"})
	if err != nil {
		t.Fatalf("first searchProviders: %v", err)
	}
	if len(first.Warnings) != 1 || !strings.Contains(first.Warnings[0], "provider resolve exceeded") {
		t.Fatalf("first scene warnings = %v, want the degraded-provider warning", first.Warnings)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("provider resolver calls after first scene = %d, want 1", got)
	}

	second, err := coordinator.searchProviders(context.Background(), scriptpkg.VidRushSegmentResult{SegmentID: "seg-second"})
	if err != nil {
		t.Fatalf("second searchProviders: %v", err)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("second scene invoked the provider resolver after the run budget was exhausted (calls=%d)", got)
	}
	if len(second.Warnings) != 1 || !strings.Contains(second.Warnings[0], "provider resolve exceeded") {
		t.Fatalf("second scene warnings = %v, want the degraded-provider warning", second.Warnings)
	}
}
