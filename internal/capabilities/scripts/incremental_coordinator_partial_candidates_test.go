package scriptgeneration

import (
	"context"
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// partialThenBlockingProviderResolver models R22: the first provider query
// returns valid candidates, the second one (or the VLM pass behind it) blocks
// until the bounded resolve context expires.
type partialThenBlockingProviderResolver struct {
	candidates []scriptpkg.SegmentAssetCandidate
	// zeroSegment forces the R22 shape where a deadline error came back with
	// an empty segment instead of the partially merged one.
	zeroSegment bool
}

func (r partialThenBlockingProviderResolver) ResolveProviders(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	<-ctx.Done()
	if r.zeroSegment {
		return scriptpkg.VidRushSegmentResult{}, ctx.Err()
	}
	partial := segment
	partial.Assets.Candidates = append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...), r.candidates...)
	return partial, ctx.Err()
}

func partialCandidateFixtures() []scriptpkg.SegmentAssetCandidate {
	return []scriptpkg.SegmentAssetCandidate{
		{AssetID: "seg-quick-1", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://images.example/corrado-augias/1.jpg"},
		{AssetID: "seg-quick-2", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://images.example/corrado-augias/2.jpg"},
		{AssetID: "seg-quick-3", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://images.example/corrado-augias/3.jpg"},
	}
}

// TestSearchProvidersKeepsPartialCandidatesOnBudgetExpiry pins the fix for the
// R22 silent-empty scene: when the segment budget expires after one provider
// already produced candidates, the stage must return those candidates and an
// explicit degradation warning, not an empty scene.
func TestSearchProvidersKeepsPartialCandidatesOnBudgetExpiry(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(partialThenBlockingProviderResolver{candidates: partialCandidateFixtures()})
	coordinator.providerResolveBudget = 25 * time.Millisecond

	result, err := coordinator.searchProviders(context.Background(), scriptpkg.VidRushSegmentResult{SegmentID: "seg-partial"})
	if err != nil {
		t.Fatalf("searchProviders returned a fatal error after a partial resolve: %v", err)
	}
	if got := len(result.Assets.Candidates); got != 3 {
		t.Fatalf("retained candidates = %d, want the 3 candidates resolved before the budget expired: %+v", got, result.Assets.Candidates)
	}
	for i, candidate := range result.Assets.Candidates {
		if candidate.AssetID != partialCandidateFixtures()[i].AssetID {
			t.Fatalf("candidate[%d] = %q, want %q (partial results must not be reordered or dropped)",
				i, candidate.AssetID, partialCandidateFixtures()[i].AssetID)
		}
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "3 candidate(s) retained") {
		t.Fatalf("warnings = %v, want one explicit degradation entry stating 3 retained candidates", result.Warnings)
	}
	if result.SegmentID != "seg-partial" {
		t.Fatalf("SegmentID = %q, want the incoming segment id", result.SegmentID)
	}
}

// TestSearchProvidersKeepsIncomingCandidatesWhenResolverReturnsEmpty covers the
// exact R22 failure mode: a resolver that abandons the merged segment and
// returns the zero value together with the deadline error. The candidates the
// segment already had must survive instead of turning the scene into zero
// images.
func TestSearchProvidersKeepsIncomingCandidatesWhenResolverReturnsEmpty(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(partialThenBlockingProviderResolver{zeroSegment: true})
	coordinator.providerResolveBudget = 25 * time.Millisecond

	incoming := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-kept",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			{AssetID: "entity-image-26", Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://images.example/corrado-augias/catalog.jpg"},
		}},
		Warnings: []string{"catalog pool reused"},
	}
	result, err := coordinator.searchProviders(context.Background(), incoming)
	if err != nil {
		t.Fatalf("searchProviders returned a fatal error after the resolver degraded: %v", err)
	}
	if len(result.Assets.Candidates) != 1 || result.Assets.Candidates[0].AssetID != "entity-image-26" {
		t.Fatalf("candidates = %+v, want the incoming catalog candidate retained", result.Assets.Candidates)
	}
	if result.SegmentID != "seg-kept" {
		t.Fatalf("SegmentID = %q, want the incoming segment id", result.SegmentID)
	}
	if len(result.Warnings) != 2 || result.Warnings[0] != "catalog pool reused" ||
		!strings.Contains(result.Warnings[1], "1 candidate(s) retained") {
		t.Fatalf("warnings = %v, want the incoming warning preserved plus one explicit degradation entry", result.Warnings)
	}
}
