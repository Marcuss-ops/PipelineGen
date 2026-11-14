package scriptgeneration

import (
	"context"
	"errors"
	"strings"
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
