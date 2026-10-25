package adapters

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

type roundRobinImageSearchProvider struct {
	queries []string
}

func (*roundRobinImageSearchProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }

func (p *roundRobinImageSearchProvider) Search(_ context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.queries = append(p.queries, req.Query)
	slug := strings.NewReplacer(" ", "-", ":", "").Replace(strings.ToLower(req.Query))
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "image-" + slug, Provider: p.Name(), SourceURL: "https://cdn.example/" + slug + ".jpg", Query: req.Query,
	}}, nil
}

func (*roundRobinImageSearchProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("not used")
}

func (*roundRobinImageSearchProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("not used")
}

func TestVidRushRegistryMediaResolverSearchImagesKeepsVariantsInTopPool(t *testing.T) {
	provider := &roundRobinImageSearchProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()

	got, err := (&VidRushRegistryMediaResolver{Registry: registry}).SearchImages(context.Background(), InternetImageSearchRequest{
		Query: "Alberto Stasi single-person portrait headshot photo", Entity: "Alberto Stasi", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.queries) != 5 {
		t.Fatalf("image queries = %d, want 5", len(provider.queries))
	}
	if len(got) != 5 {
		t.Fatalf("candidate count = %d, want bounded 5-query pool", len(got))
	}
	seenQueries := make(map[string]struct{}, len(got))
	for _, candidate := range got {
		seenQueries[candidate.Query] = struct{}{}
	}
	if len(seenQueries) != 5 {
		t.Fatalf("top pool contains %d query variants, want all 5: %+v", len(seenQueries), got)
	}
}

// fanoutGate coordinates the two provider searchers so a test can prove they
// start concurrently rather than sequentially.
type fanoutGate struct {
	entered chan string
	release chan struct{}
}

type gatedYouTubeProvider struct {
	calls atomic.Int32
	gate  *fanoutGate
}

func (p *gatedYouTubeProvider) Name() string { return scriptpkg.VidRushProviderYouTube }

func (p *gatedYouTubeProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.calls.Add(1)
	p.gate.entered <- "youtube"
	select {
	case <-p.gate.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "yt-1", Provider: p.Name(), SourceURL: "https://youtu.be/yt-1", Query: req.Query}}, nil
}

func (*gatedYouTubeProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("not used")
}

func (*gatedYouTubeProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("not used")
}

type gatedImageSearcher struct {
	calls atomic.Int32
	gate  *fanoutGate
}

func (s *gatedImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	s.gate.entered <- "internet_images"
	select {
	case <-s.gate.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "img-1", Provider: "internet_images", SourceURL: "https://cdn.example/img-1", Query: req.Query}}, nil
}

func TestVidRushProviderFanoutRunsProvidersInParallel(t *testing.T) {
	gate := &fanoutGate{entered: make(chan string, 2), release: make(chan struct{})}
	youtube := &gatedYouTubeProvider{gate: gate}
	images := &gatedImageSearcher{gate: gate}
	fanout := NewVidRushProviderFanoutWithYouTube(images, youtube)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "parallel",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				YouTube:        mediadomain.MediaToggleEnabled,
				InternetImages: mediadomain.MediaToggleEnabled,
			},
			Sources: []mediadomain.SegmentMediaSource{{
				SegmentID: "seg-1", Provider: scriptpkg.VidRushProviderYouTube,
				SourceURL: "https://youtu.be/seg-1", Mode: mediadomain.SegmentMediaSourceModeSuggested,
			}},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-1", TextHash: "hash", Text: "scene text",
		Insights: scriptpkg.SegmentInsights{
			SegmentID:    "seg-1",
			ImageQueries: []string{"image query"},
		},
	}

	resultCh := make(chan error, 1)
	var result scriptpkg.VidRushSegmentResult
	go func() {
		var err error
		result, err = fanout.ResolveProviders(context.Background(), plan, segment)
		resultCh <- err
	}()

	// Both providers must have entered before either is released — this proves
	// the fanout launches them concurrently rather than serially.
	entered := make(map[string]bool, 2)
	for len(entered) < 2 {
		select {
		case name := <-gate.entered:
			entered[name] = true
		case err := <-resultCh:
			t.Fatalf("ResolveProviders returned before both providers started: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("providers did not both start")
		}
	}
	close(gate.release)
	if err := <-resultCh; err != nil {
		t.Fatalf("ResolveProviders error: %v", err)
	}

	if youtube.calls.Load() != 1 || images.calls.Load() != 1 {
		t.Fatalf("youtube calls=%d images calls=%d, want 1 each", youtube.calls.Load(), images.calls.Load())
	}
	if result.Assets.PrimaryVideo != nil {
		t.Fatalf("provider fanout selected a primary: %+v", result.Assets.PrimaryVideo)
	}
	if len(result.Assets.Candidates) == 0 {
		t.Fatal("expected discovered candidates")
	}
	if len(result.Assets.SecondaryImages) == 0 {
		t.Fatal("expected internet image candidates in secondary images")
	}
}

func TestPersonImageSearchQueryAddsPortraitHintWithoutChangingIdentity(t *testing.T) {
	if got := personImageSearchQuery("  Chiara Poggi  ", true); got != "Chiara Poggi single-person portrait headshot photo" {
		t.Fatalf("person image provider query = %q", got)
	}
	if got := personImageSearchQuery("Pavia", false); got != "Pavia" {
		t.Fatalf("non-person image provider query = %q", got)
	}
}

type rejectAllPersonImages struct{ calls int }

func (s *rejectAllPersonImages) ScoreImageCandidates(_ context.Context, _ string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	for i := range candidates {
		candidates[i].SemanticStatus = "rejected"
		candidates[i].SemanticScore = 0
	}
	return candidates, nil
}

func TestVidRushFanoutAppliesImageGateToCachedCandidates(t *testing.T) {
	scorer := &rejectAllPersonImages{}
	fanout := NewVidRushProviderFanout(nil).WithSemanticScorer(scorer)
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "cached-person", Provider: scriptpkg.VidRushProviderInternetImages,
		SourceURL: "https://images.example/cached-person.jpg", Entity: "Example Person",
	}
	got, err := fanout.verifyPersonImageCandidates(context.Background(), []scriptpkg.SegmentAssetCandidate{candidate})
	if err != nil {
		t.Fatalf("verify cached candidate: %v", err)
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1", scorer.calls)
	}
	if len(got) != 0 {
		t.Fatalf("verified candidates = %+v, want rejected cached image filtered before materialization", got)
	}
}

func TestVidRushFanoutUsesPortraitGateOnlyForPersonQueries(t *testing.T) {
	scorer := &rejectAllPersonImages{}
	fanout := NewVidRushProviderFanout(nil).WithSemanticScorer(scorer)
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "ctx-01", Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{{Value: "Alberto Stasi", Type: "PERSON"}},
	}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "person", Provider: scriptpkg.VidRushProviderInternetImages, Query: "Alberto Stasi", Entity: "Alberto Stasi", SourceURL: "https://images.example/person.jpg"},
		{AssetID: "courtroom", Provider: scriptpkg.VidRushProviderInternetImages, Query: "aula tribunale Pavia", Entity: "aula tribunale Pavia", SourceURL: "https://images.example/courtroom.jpg"},
	}

	got, err := fanout.verifySegmentImageCandidates(context.Background(), segment, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if scorer.calls != 1 {
		t.Fatalf("portrait scorer calls = %d, want only the PERSON candidate", scorer.calls)
	}
	if len(got) != 1 || got[0].AssetID != "courtroom" {
		t.Fatalf("verified candidates = %+v, want contextual photo retained and rejected portrait removed", got)
	}
}

// TestVidRushProviderFanoutCachesEmptyImageResults certifies the durable-runner
// provider fanout caches empty internet_images results in L2 (TTL 48h) so a
// warm replay of the same segment is deterministic and does not re-call the
// provider:
//   - Run A (cold, force_refresh_assets=true): a real provider call, the empty
//     result is stored in L2.
//   - Run B (warm, same segment identity): the L2 hit replays the empty result
//     as HIT_EXACT with zero provider calls.
func TestVidRushProviderFanoutCachesEmptyImageResults(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &emptyInternetImageSearcher{}
	fanout := NewVidRushProviderFanoutWithCache(searcher, newMemoryVidRushCache())

	plan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Topic:    "civiltà maya",
			Language: "it",
			MediaPlan: mediadomain.MediaPlanSpec{
				ProviderPolicy:     mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
				ForceRefreshAssets: forceRefresh,
			},
		}
	}
	segment := func(textHash string) scriptpkg.VidRushSegmentResult {
		return scriptpkg.VidRushSegmentResult{
			SegmentID: "scene-0",
			TextHash:  textHash,
			Insights: scriptpkg.SegmentInsights{
				ImageQueries: []string{"valle remota"},
			},
		}
	}

	// Run A — cold: real provider call, empty result cached in L2.
	a, err := fanout.ResolveProviders(context.Background(), plan(true), segment("cold-text-hash"))
	if err != nil {
		t.Fatalf("run A failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run A provider calls = %d, want 1 real search", searcher.calls)
	}
	if got := a.Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("run A cache state = %q, want REFRESHED", got)
	}

	// Run B — warm: L2 hit replays the empty result, no provider call.
	b, err := fanout.ResolveProviders(context.Background(), plan(false), segment("cold-text-hash"))
	if err != nil {
		t.Fatalf("run B failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run B provider calls = %d, want still 1 (L2 empty hit must not search)", searcher.calls)
	}
	if got := b.Cache.InternetImages; got != "HIT_EXACT" {
		t.Fatalf("run B cache state = %q, want HIT_EXACT", got)
	}
}

func TestVidRushProviderFanoutRetriesEmptyEntityImageResults(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &emptyInternetImageSearcher{}
	cache := newMemoryVidRushCache()
	fanout := NewVidRushProviderFanoutWithCache(searcher, cache)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Topic:    "Garlasco",
		Language: "it",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"}, MaxPerEntity: 1,
			}},
		},
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-0", TextHash: "same-hash", Text: "Alberto Stasi al processo",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"Alberto Stasi"},
			Entities:     []scriptpkg.ExtractedEntity{{Type: "PERSON", Value: "Alberto Stasi"}},
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := fanout.ResolveProviders(context.Background(), plan, segment); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if searcher.calls != 2 {
		t.Fatalf("provider calls = %d, want 2; empty entity-image result must not be cached", searcher.calls)
	}
}

// TestVidRushProviderFanoutForceRefreshBypassesCache pins that plan.ForceRefresh
// (top-level force_refresh) bypasses the internet_images asset cache even when
// the segment identity and per-query key (topic+query+language) are unchanged,
// so a force-refresh run re-searches instead of reporting HIT_EXACT.
func TestVidRushProviderFanoutForceRefreshBypassesCache(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &emptyInternetImageSearcher{}
	fanout := NewVidRushProviderFanoutWithCache(searcher, newMemoryVidRushCache())

	plan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Topic:        "civiltà maya",
			Language:     "it",
			ForceRefresh: forceRefresh,
			MediaPlan: mediadomain.MediaPlanSpec{
				ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			},
		}
	}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-0", TextHash: "same-hash",
		Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"valle remota"}},
	}

	// Seed a cold search (empty) so the caches are populated.
	if _, err := fanout.ResolveProviders(context.Background(), plan(false), segment); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("seed provider calls = %d, want 1", searcher.calls)
	}

	// force_refresh with identical segment identity: must re-search.
	b, err := fanout.ResolveProviders(context.Background(), plan(true), segment)
	if err != nil {
		t.Fatalf("force-refresh failed: %v", err)
	}
	if searcher.calls != 2 {
		t.Fatalf("force-refresh provider calls = %d, want 2 (cache must be bypassed)", searcher.calls)
	}
	if got := b.Cache.InternetImages; got == "HIT_EXACT" {
		t.Fatalf("force-refresh cache state = %q, want a fresh search (MISS/REFRESHED)", got)
	}
}
