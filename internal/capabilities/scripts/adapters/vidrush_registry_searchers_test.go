package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

type roundRobinImageSearchProvider struct {
	mu      sync.Mutex
	queries []string
}

func (*roundRobinImageSearchProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }

func (p *roundRobinImageSearchProvider) Search(_ context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.mu.Lock()
	p.queries = append(p.queries, req.Query)
	p.mu.Unlock()
	slug := strings.NewReplacer(" ", "-", ":", "").Replace(strings.ToLower(req.Query))
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "image-" + slug, Provider: p.Name(), SourceURL: "https://cdn.example/" + slug + ".jpg", Query: req.Query,
	}}, nil
}

type gatedVariantImageProvider struct {
	entered chan string
	release chan struct{}
}

type measuredParallelImageProvider struct {
	calls  atomic.Int32
	active atomic.Int32
	peak   atomic.Int32
}

type parallelImageQueryProbe struct {
	calls  atomic.Int32
	active atomic.Int32
	peak   atomic.Int32
}

func (p *parallelImageQueryProbe) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.calls.Add(1)
	active := p.active.Add(1)
	for peak := p.peak.Load(); active > peak; peak = p.peak.Load() {
		if p.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer p.active.Add(-1)
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	slug := strings.ToLower(strings.ReplaceAll(req.Query, " ", "-"))
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "asset-" + slug, Provider: scriptpkg.VidRushProviderInternetImages,
		Query: req.Query, SourceURL: "https://cdn.example/" + slug + ".jpg",
	}}, nil
}

func (*measuredParallelImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *measuredParallelImageProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.calls.Add(1)
	active := p.active.Add(1)
	for peak := p.peak.Load(); active > peak; peak = p.peak.Load() {
		if p.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	defer p.active.Add(-1)
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID:  "parallel-" + strings.ReplaceAll(strings.ToLower(req.Query), " ", "-"),
		Provider: scriptpkg.VidRushProviderInternetImages, Query: req.Query,
		SourceURL: "https://cdn.example/" + strings.ReplaceAll(strings.ToLower(req.Query), " ", "-") + ".jpg",
	}}, nil
}
func (*measuredParallelImageProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("not used")
}
func (*measuredParallelImageProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("not used")
}

func (*gatedVariantImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *gatedVariantImageProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p.entered <- req.Query
	select {
	case <-p.release:
		return []scriptpkg.SegmentAssetCandidate{{
			AssetID:  "variant-" + strings.ReplaceAll(strings.ToLower(req.Query), " ", "-"),
			Provider: scriptpkg.VidRushProviderInternetImages, Query: req.Query,
			SourceURL: "https://cdn.example/" + strings.ReplaceAll(strings.ToLower(req.Query), " ", "-") + ".jpg",
		}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*gatedVariantImageProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("not used")
}
func (*gatedVariantImageProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("not used")
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

func TestVidRushRegistryMediaResolverRunsPortraitVariantsInParallel(t *testing.T) {
	provider := &gatedVariantImageProvider{entered: make(chan string, 5), release: make(chan struct{})}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	resolver := &VidRushRegistryMediaResolver{Registry: registry}
	done := make(chan struct {
		candidates []scriptpkg.SegmentAssetCandidate
		err        error
	}, 1)
	go func() {
		candidates, err := resolver.SearchImages(context.Background(), InternetImageSearchRequest{
			Query: "Alberto Stasi single-person portrait headshot photo", Entity: "Alberto Stasi", Limit: 2,
		})
		done <- struct {
			candidates []scriptpkg.SegmentAssetCandidate
			err        error
		}{candidates: candidates, err: err}
	}()

	seen := make(map[string]bool, 5)
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for len(seen) < 5 {
		select {
		case query := <-provider.entered:
			seen[query] = true
		case <-timeout.C:
			close(provider.release)
			<-done
			t.Fatalf("only %d/5 portrait variants entered before release; searches are not parallel", len(seen))
		}
	}
	close(provider.release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	wantOrder := []string{
		"Alberto Stasi single-person portrait headshot photo",
		"Alberto Stasi foto intervista",
		"Alberto Stasi foto udienza tribunale Garlasco",
		"Alberto Stasi dichiarazioni foto avvocato Garlasco",
		"Alberto Stasi servizio televisivo ritratto",
	}
	if len(result.candidates) != len(wantOrder) {
		t.Fatalf("candidate count = %d, want %d", len(result.candidates), len(wantOrder))
	}
	for i, want := range wantOrder {
		if result.candidates[i].Query != want {
			t.Fatalf("candidate[%d].Query = %q, want deterministic query order %q", i, result.candidates[i].Query, want)
		}
	}
}

func TestVidRushImageSearchGlobalParallelismCapsAtFifteen(t *testing.T) {
	provider := &measuredParallelImageProvider{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	resolver := &VidRushRegistryMediaResolver{Registry: registry}
	const concurrentScenes = 4
	done := make(chan error, concurrentScenes)
	for scene := 0; scene < concurrentScenes; scene++ {
		go func(scene int) {
			_, err := resolver.SearchImages(context.Background(), InternetImageSearchRequest{
				SegmentID: fmt.Sprintf("scene-%d", scene),
				Query:     "Alberto Stasi single-person portrait headshot photo", Entity: "Alberto Stasi", Limit: 5,
			})
			done <- err
		}(scene)
	}
	for scene := 0; scene < concurrentScenes; scene++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := provider.calls.Load(); got != concurrentScenes*5 {
		t.Fatalf("provider calls = %d, want %d", got, concurrentScenes*5)
	}
	if got := provider.peak.Load(); got != vidRushMaxParallelImageSearches {
		t.Fatalf("peak concurrent image searches = %d, want configured cap %d", got, vidRushMaxParallelImageSearches)
	}
}

func TestVidRushImageQueriesRunUpToFifteenInParallelAndMergeInInputOrder(t *testing.T) {
	searcher := &parallelImageQueryProbe{}
	fanout := &VidRushProviderFanout{images: searcher}
	queries := make([]string, 20)
	for i := range queries {
		queries[i] = fmt.Sprintf("query-%02d", i)
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		Topic: "parallel query test", Language: "en", ForceRefresh: true,
		MediaPlan: mediadomain.MediaPlanSpec{ForceRefreshAssets: true},
	}
	workerInput := scriptpkg.VidRushSegmentResult{SegmentID: "parallel-segment", TextHash: "parallel-hash"}
	fanoutPlan := vidRushFanoutPlan{
		segmentID: workerInput.SegmentID, textHash: workerInput.TextHash,
		imageQueries: queries, perQueryLimit: 1,
	}
	outcomes := make(chan vidRushProviderOutcome, 1)
	fanout.resolveInternetImageCandidates(context.Background(), plan, fanoutPlan, workerInput, outcomes)
	outcome := <-outcomes
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if got := searcher.calls.Load(); got != int32(len(queries)) {
		t.Fatalf("provider calls = %d, want %d", got, len(queries))
	}
	if got := searcher.peak.Load(); got != vidRushMaxParallelImageSearches {
		t.Fatalf("peak per-scene queries = %d, want cap %d", got, vidRushMaxParallelImageSearches)
	}
	if len(outcome.candidates) != len(queries) {
		t.Fatalf("candidate count = %d, want %d", len(outcome.candidates), len(queries))
	}
	for i, want := range queries {
		if got := outcome.candidates[i].Query; got != want {
			t.Fatalf("candidate[%d].Query = %q, want stable input order %q", i, got, want)
		}
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

// TestVidRushFanoutHydratesDurableVerdictsAndSkipsReverification pins the
// cache-hit fast path of the resolve: a discovered candidate whose URL already
// carries a durable pixel verdict in the catalog is served from that verdict —
// the scorer is never called (no download, no VLM call), and the verdict
// survives into the returned candidates.
func TestVidRushFanoutHydratesDurableVerdictsAndSkipsReverification(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/mj/certified.jpg")
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: "person:michael-jordan", Provider: "duckduckgo", Rank: 1,
		SourceURL:      "https://images.example/mj/certified.jpg",
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, SemanticScore: 0.9,
		QualityReason: "Ollama visual verification confirmed one dominant subject",
	}); err != nil {
		t.Fatal(err)
	}
	scorer := &rejectAllPersonImages{}
	fanout := NewVidRushProviderFanoutWithCatalog(nil, nil, repo).WithSemanticScorer(scorer)
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "cached", Provider: scriptpkg.VidRushProviderInternetImages, Query: "Michael Jordan", Entity: "Michael Jordan", SourceURL: "https://images.example/mj/certified.jpg"},
	}
	got, err := fanout.verifyPersonImageCandidates(context.Background(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if scorer.calls != 0 {
		t.Fatalf("scorer calls = %d, want 0 — the durable verdict must serve the cache hit without a VLM call", scorer.calls)
	}
	if len(got) != 1 || got[0].SemanticStatus != entitycatalog.CandidateSemanticAccepted || got[0].SemanticScore != 0.9 {
		t.Fatalf("verified = %+v, want the durable accepted verdict hydrated", got)
	}
}

// TestVidRushFanoutTransientRejectionStaysRetryable pins the complement: an
// outage-shaped rejection ("verification failed") is not a quality fact, so it
// is never hydrated as a reusable verdict and the candidate keeps going
// through the scorer.
func TestVidRushFanoutTransientRejectionStaysRetryable(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/mj/flaky.jpg")
	if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{
		CanonicalEntityID: "person:michael-jordan", Provider: "duckduckgo", Rank: 1,
		SourceURL:      "https://images.example/mj/flaky.jpg",
		SemanticStatus: entitycatalog.CandidateSemanticRejected, SemanticScore: 0,
		QualityReason: "visual verification failed: context deadline exceeded",
	}); err != nil {
		t.Fatal(err)
	}
	scorer := &rejectAllPersonImages{}
	fanout := NewVidRushProviderFanoutWithCatalog(nil, nil, repo).WithSemanticScorer(scorer)
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "flaky", Provider: scriptpkg.VidRushProviderInternetImages, Query: "Michael Jordan", Entity: "Michael Jordan", SourceURL: "https://images.example/mj/flaky.jpg"},
	}
	got, err := fanout.verifyPersonImageCandidates(context.Background(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1 — a transient rejection must stay retryable", scorer.calls)
	}
	if len(got) != 0 {
		t.Fatalf("verified = %+v, want the retry outcome (rejected by the scorer) filtered", got)
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
