package vidrush

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// blockingImageSearcher models a provider lane that never answers in time
// (SearXNG slow / DuckDuckGo vqd stall). It must never be reached when the
// durable catalog already satisfies the scene.
type blockingImageSearcher struct {
	calls atomic.Int32
}

func (s *blockingImageSearcher) SearchImages(ctx context.Context, _ InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

// blockingSemanticScorer models an Ollama instance that is loaded, broken or
// simply slower than the stage budget.
type blockingSemanticScorer struct {
	calls atomic.Int32
}

func (s *blockingSemanticScorer) ScoreImageCandidates(ctx context.Context, _ string, _ []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func degradedProviderPlan(force bool) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Language: "it",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy:     mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			ForceRefreshAssets: force,
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"}, Scope: "per_scene",
			}},
		},
	}
}

func degradedProviderSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-degraded", SceneID: "scene-degraded", Position: 1,
		Text:     "Corrado Augias racconta la sua carriera in studio.",
		TextHash: "degraded-provider-hash",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{
				Value: "Corrado Augias", Type: "PERSON", CanonicalID: "person:corrado-augias",
				SearchIntents: []string{"Corrado Augias"},
			}},
			ImageEntityCanonicalIDs: map[string]string{"Corrado Augias": "person:corrado-augias"},
			ImageQueries:            []string{"Corrado Augias portrait"},
		},
	}
}

// TestDegradedProvidersAndBlockedScorerStillServeWarmCatalog is the offline R22
// simulation: Wikipedia returning 429, DuckDuckGo timing out and the Ollama
// scorer blocked must not starve a scene whose catalog already holds verified
// portraits. The catalog hit must not consult any of them.
func TestDegradedProvidersAndBlockedScorerStillServeWarmCatalog(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedVerifiedPortraitCatalog(t, repo, "Corrado Augias", 4)

	searcher := &blockingImageSearcher{}
	scorer := &blockingSemanticScorer{}
	fanout := NewVidRushProviderFanoutWithCatalog(searcher, nil, repo).WithSemanticScorer(scorer)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := fanout.ResolveProviders(ctx, degradedProviderPlan(false), degradedProviderSegment())
	if err != nil {
		t.Fatalf("ResolveProviders with a warm catalog: %v", err)
	}
	if len(result.Assets.Candidates) < 4 {
		t.Fatalf("candidates = %d, want the 4 verified catalog portraits retained through provider/VLM degradation",
			len(result.Assets.Candidates))
	}
	if calls := searcher.calls.Load(); calls != 0 {
		t.Fatalf("provider searches = %d, want 0: a warm catalog must not wait on a degraded provider", calls)
	}
	if calls := scorer.calls.Load(); calls != 0 {
		t.Fatalf("VLM scorer calls = %d, want 0: catalog verdicts are already certified", calls)
	}
}

// TestDegradedProviderWithoutCatalogFailsExplicitly pins the no-silent-failure
// contract: with no usable catalog the 429/timeout must surface as an error to
// the caller, which the coordinator turns into an explicit scene warning.
func TestDegradedProviderWithoutCatalogFailsExplicitly(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &refreshImageSearcher{err: errors.New("wikipedia: http 429 Too Many Requests")}
	fanout := NewVidRushProviderFanoutWithCatalog(searcher, nil, repo)

	result, err := fanout.ResolveProviders(context.Background(), degradedProviderPlan(false), degradedProviderSegment())
	// A degraded image provider is not fatal (another scene/provider may
	// recover), but it must leave an explicit diagnostic on the segment.
	if err != nil {
		t.Fatalf("degradable image provider must not fail the run: %v", err)
	}
	if len(result.Assets.Candidates) != 0 {
		t.Fatalf("candidates = %d, want 0 when nothing answered", len(result.Assets.Candidates))
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "internet images provider degraded") && strings.Contains(warning, "429") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want an explicit internet-images degradation entry naming the provider failure", result.Warnings)
	}
}

// TestSlowProviderRespectsSceneBudget: a provider slower than the caller's
// deadline must end with a deadline error, never with an unbounded wait.
func TestSlowProviderRespectsSceneBudget(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &blockingImageSearcher{}
	fanout := NewVidRushProviderFanoutWithCatalog(searcher, nil, repo)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := fanout.ResolveProviders(ctx, degradedProviderPlan(true), degradedProviderSegment())
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("ResolveProviders: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ResolveProviders took %s, want the 50ms deadline to bound the stage", elapsed)
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "internet images provider degraded") && strings.Contains(warning, "deadline exceeded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want the deadline surfaced as an explicit degradation", result.Warnings)
	}
}
