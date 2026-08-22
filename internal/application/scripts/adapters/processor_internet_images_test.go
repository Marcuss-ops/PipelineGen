package adapters

import (
	"context"
	"fmt"
	"sync"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
)

type emptyInternetImageSearcher struct {
	calls int
}

func (s *emptyInternetImageSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	return nil, nil
}

// memoryVidRushCache is a minimal in-memory VidRushCachePort fake used to
// exercise the durable L2 cache path without SQLite.
type memoryVidRushCache struct {
	mu   sync.Mutex
	data map[string]map[string][]byte
}

func newMemoryVidRushCache() *memoryVidRushCache {
	return &memoryVidRushCache{data: map[string]map[string][]byte{}}
}

func (c *memoryVidRushCache) Get(_ context.Context, namespace, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, ok := c.data[namespace]
	if !ok {
		return nil, false, nil
	}
	raw, ok := ns[key]
	if !ok {
		return nil, false, nil
	}
	return raw, true, nil
}

func (c *memoryVidRushCache) Put(_ context.Context, namespace, key string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, ok := c.data[namespace]
	if !ok {
		ns = map[string][]byte{}
		c.data[namespace] = ns
	}
	ns[key] = payload
	return nil
}

var _ scriptports.VidRushCachePort = (*memoryVidRushCache)(nil)

// TestInternetImagesProcessorDoesNotCacheProviderMisses verifies the no-L2
// path: with a nil durable cache, an empty provider result must stay out of
// the no-TTL L1 in-memory map, so the provider is retried on every run
// (empty results are only durable-cached in L2 — see
// TestInternetImagesProcessorCachesEmptyResultsInL2).
func TestInternetImagesProcessorDoesNotCacheProviderMisses(t *testing.T) {
	searcher := &emptyInternetImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "negative-cache-images-test",
		TextHash:  "negative-cache-images-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"no result query"},
		},
	}}}

	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), plan, input); err != nil {
			t.Fatalf("process call %d failed: %v", i+1, err)
		}
	}
	if searcher.calls != 2 {
		t.Fatalf("expected provider to be retried after an empty result, calls = %d", searcher.calls)
	}
}

func TestInternetImagesProcessorCacheOnlyNeverCallsProvider(t *testing.T) {
	searcher := &emptyInternetImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: true,
		MediaPlan: media.MediaPlanSpec{
			Mode:               media.MediaPlanModeCacheOnly,
			ForceRefreshAssets: true,
			ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "cache-only-images-miss",
		TextHash:  "cache-only-images-hash",
		Insights:  scriptpkg.SegmentInsights{ImageQueries: []string{"Elon Musk"}},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.Len(t, result.VidRushSegments, 1)
	require.Equal(t, "CACHE_MISS", result.VidRushSegments[0].Cache.InternetImages)
	require.Equal(t, 0, searcher.calls, "cache_only must never call the image provider")
	require.NotEmpty(t, result.Warnings)
}

type multipleInternetImageSearcher struct{}

func (multipleInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, 3)
	for i := 0; i < 3; i++ {
		url := fmt.Sprintf("https://images.example/%s/%d", req.Query, i)
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID:   fmt.Sprintf("%s-%d", req.Query, i),
			Provider:  "internet_images",
			SourceURL: url,
		})
	}
	return out, nil
}

type recordingInternetImageSearcher struct{ queries chan string }

func (s recordingInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.queries <- req.Query
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "manual-image", Provider: "internet_images", Query: req.Query, SourceURL: "https://images.example/manual.jpg"}}, nil
}

func TestInternetImagesProcessorUsesManualSearchBeforeEntityExpansion(t *testing.T) {
	searcher := recordingInternetImageSearcher{queries: make(chan string, 1)}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: true,
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
			ForceRefreshAssets: true,
			Searches:           []media.SegmentMediaSearch{{SegmentID: "main", Slot: media.SlotSecondaryImage, Query: "Chichen Itza Maya pyramid Yucatan", Providers: []string{"internet_images"}, MediaTypes: []string{"image"}}},
			Extraction:         media.MediaExtractionPolicy{EntityImages: media.EntityImagePolicy{Enabled: true, EntityTypes: []string{"GPE"}}},
		},
	}
	input := ProcessInput{
		SpecScene:       scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{ID: "scene-0", SegmentID: "main", Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{Text: "Tikal", Type: "GPE"}}}}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: "main", SceneID: "scene-0", TextHash: "manual-image-query"}},
	}
	if _, err := processor.Process(context.Background(), plan, input); err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if got := <-searcher.queries; got != "Chichen Itza Maya pyramid Yucatan" {
		t.Fatalf("provider query = %q, want manual image query", got)
	}
}

func TestInternetImagesProcessorRetainsResultsAcrossAllQueries(t *testing.T) {
	processor := NewInternetImagesProcessor(multipleInternetImageSearcher{})
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "full-images-test",
		TextHash:  "full-images-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"query-a", "query-b"},
		},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("expected one segment, got %d", len(result.VidRushSegments))
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 6 {
		t.Fatalf("expected all six image results, got %d", got)
	}
}

// rogueInternetImageSearcher returns a mix of valid and forbidden providers,
// simulating a misconfigured or compromised searcher that leaks YouTube results.
type rogueInternetImageSearcher struct{}

func (rogueInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return []scriptpkg.SegmentAssetCandidate{
		{AssetID: "good-1", Provider: "internet_images", SourceURL: "https://images.example/maya/1.jpg"},
		{AssetID: "bad-yt", Provider: "youtube", SourceURL: "https://youtube.com/watch?v=leaked"},
		{AssetID: "bad-gen", Provider: "generated_images", SourceURL: "https://ai.example/gen.png"},
		{AssetID: "good-2", Provider: "", SourceURL: "https://images.example/maya/2.jpg"},
		{AssetID: "bad-yt2", Provider: "YOUTUBE", SourceURL: "https://youtu.be/leaked2"},
		{AssetID: "good-3", Provider: "internet_images", SourceURL: "https://images.example/maya/3.jpg"},
	}, nil
}

// TestInternetImagesProcessor_RejectsNonInternetImagesProviders verifies
// the processor-level YouTube-block contract: when a searcher returns
// candidates with provider="youtube" (or any non-internet_images provider),
// the processor MUST filter them out at ingest time. Only candidates with
// provider="internet_images" (or empty, which gets defaulted) survive.
func TestInternetImagesProcessor_RejectsNonInternetImagesProviders(t *testing.T) {
	processor := NewInternetImagesProcessor(rogueInternetImageSearcher{})
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "provider-gate-test",
		TextHash:  "provider-gate-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"maya ruins"},
		},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(result.VidRushSegments))
	}
	candidates := result.VidRushSegments[0].Assets.Candidates
	if len(candidates) != 3 {
		t.Fatalf("expected exactly 3 valid candidates (good-1, good-2, good-3), got %d: %+v",
			len(candidates), candidates)
	}
	for _, c := range candidates {
		if c.Provider != "internet_images" {
			t.Errorf("candidate %q has provider=%q, want \"internet_images\" (forbidden providers must be filtered)",
				c.AssetID, c.Provider)
		}
		if c.Provider == "youtube" {
			t.Errorf("candidate %q has provider=youtube — FORBIDDEN, must be rejected at processor level", c.AssetID)
		}
	}
}

type entityImageSearcher struct{ calls int }

type recordingImageSearcher struct {
	requests []InternetImageSearchRequest
}

func (s *recordingImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.requests = append(s.requests, req)
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "img-chichen-itza", Provider: "internet_images", Query: req.Query, Entity: req.Entity,
		SourceURL: "https://example.test/chichen-itza.jpg", PreviewURL: "https://example.test/chichen-itza.jpg", RightsStatus: "unknown",
	}}, nil
}

func TestInternetImagesProcessor_SearchesPrimaryEntity(t *testing.T) {
	searcher := &recordingImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "it", MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		Extraction:     media.MediaExtractionPolicy{EntityImages: media.EntityImagePolicy{Enabled: true, EntityTypes: []string{"GPE"}, MaxPerEntity: 1}},
	}}
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
			ID: "scene-0", SegmentID: "main", Index: 0, Text: "Chichén Itzá fu una città Maya.",
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Chichén Itzá", Text: "Chichén Itzá", Type: "GPE"}}},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: "main", SceneID: "scene-0", TextHash: "maya-hash", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Chichén Itzá", Type: "GPE", Confidence: 0.90}}, ImageQueries: []string{"Chichén Itzá"}}}},
	}

	result, err := processor.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotEmpty(t, searcher.requests)
	require.Equal(t, "Chichén Itzá", searcher.requests[0].Query)
	require.NotEmpty(t, result.VidRushSegments)
	require.NotEmpty(t, result.VidRushSegments[0].Assets.Candidates)
}

func (s *entityImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "asset-mike", Provider: "internet_images", Entity: req.Query,
		Query: req.Query, SourceURL: "https://images.example/mike.jpg", Score: 1,
		DriveLink: "https://drive.google.com/file/d/asset-mike/view", LegacyFileMD5: "hash-mike",
		RightsStatus: "unknown_allowed", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
		IndexStatus: scriptpkg.VidRushStatusIndexed,
	}}, nil
}

func TestInternetImagesProcessorProjectsAndCachesEntityImages(t *testing.T) {
	searcher := &entityImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "it", MediaPlan: media.MediaPlanSpec{
		Extraction: media.MediaExtractionPolicy{EntityImages: media.EntityImagePolicy{
			Enabled: true, EntityTypes: []string{"PERSON"}, MaxPerEntity: 1,
		}},
	}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
		{ID: "scene-1", SegmentID: "seg-1", Index: 0, Text: "Mike Tyson", Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "it", PrimaryEntities: []scriptpkg.AnnotatedEntity{{Text: "Mike Tyson", CanonicalName: "Mike Tyson", Type: "PERSON"}},
		}},
		{ID: "scene-2", SegmentID: "seg-2", Index: 1, Text: "Mike Tyson", Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "it", PrimaryEntities: []scriptpkg.AnnotatedEntity{{Text: "Mike Tyson", CanonicalName: "Mike Tyson", Type: "PERSON"}},
		}},
	}}, VidRushSegments: []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-1", SceneID: "scene-1", Position: 0, TextHash: "h1"},
		{SegmentID: "seg-2", SceneID: "scene-2", Position: 1, TextHash: "h2"},
	}}

	first, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 1 {
		t.Fatalf("image searches = %d, want 1 entity-cache lookup", searcher.calls)
	}
	for _, scene := range first.UpdatedSpecScene.Scenes {
		image := scene.Annotations.PrimaryEntities[0].Image
		if image == nil || image.Status != "resolved" || image.AssetID != "asset-mike" {
			t.Fatalf("scene %s image = %+v", scene.ID, image)
		}
	}
}

type countingImageSearcher struct{ calls int }

func (s *countingImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID: "img-cold-warm", Provider: "internet_images", Query: req.Query,
		SourceURL: "https://images.example/cold-warm.jpg",
	}}, nil
}

// TestInternetImagesCacheColdWarmForcedRefresh certifies the cache lifecycle
// from the certification spec §9:
//   - Run A (cold forced): force_refresh_assets=true → a real provider call,
//     cache state REFRESHED.
//   - Run B (warm): force_refresh_assets=false → no provider call, cache state
//     HIT_EXACT (the previous run's result is replayed).
//   - Run C (forced refresh): force_refresh_assets=true → the cache must NOT
//     suppress a fresh provider call, cache state REFRESHED.
func TestInternetImagesCacheColdWarmForcedRefresh(t *testing.T) {
	vidrushImageCache = sync.Map{}
	searcher := &countingImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)

	buildPlan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			MediaPlan: media.MediaPlanSpec{
				ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
				ForceRefreshAssets: forceRefresh,
			},
		}
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "cache-cold-warm-seg",
		TextHash:  "cache-cold-warm-hash",
		Insights:  scriptpkg.SegmentInsights{ImageQueries: []string{"Dwayne Johnson"}},
	}}}

	// Run A — cold forced: cache skipped, provider invoked, result stored.
	a, err := processor.Process(context.Background(), buildPlan(true), input)
	if err != nil {
		t.Fatalf("run A failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run A provider calls = %d, want 1 real search", searcher.calls)
	}
	if got := a.VidRushSegments[0].Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("run A cache state = %q, want REFRESHED", got)
	}

	// Run B — warm: same input without forced refresh → cache hit, no provider.
	b, err := processor.Process(context.Background(), buildPlan(false), input)
	if err != nil {
		t.Fatalf("run B failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run B provider calls = %d, want still 1 (cache hit must not search)", searcher.calls)
	}
	if got := b.VidRushSegments[0].Cache.InternetImages; got != "HIT_EXACT" {
		t.Fatalf("run B cache state = %q, want HIT_EXACT", got)
	}

	// Run C — forced refresh: the cache must not suppress a fresh provider call.
	c, err := processor.Process(context.Background(), buildPlan(true), input)
	if err != nil {
		t.Fatalf("run C failed: %v", err)
	}
	if searcher.calls != 2 {
		t.Fatalf("run C provider calls = %d, want 2 (forced refresh must re-search)", searcher.calls)
	}
	if got := c.VidRushSegments[0].Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("run C cache state = %q, want REFRESHED", got)
	}
}

// TestEntityImageCacheColdWarmForcedRefresh certifies the cold/warm/refresh
// lifecycle of the per-query entityImageCache — the second, narrower cache
// layer that is active only when entity_images.enabled=true. Each run reuses
// the same entity query but a distinct segment identity, so the segment-level
// cache (keyed on SegmentID+TextHash) cannot mask the per-query cache under
// test:
//   - Run A (cold forced): force_refresh_assets=true → a real provider call,
//     result stored in the entity-image cache.
//   - Run B (warm): same entity query, force_refresh_assets=false → the
//     entity-image cache satisfies the lookup with no provider call.
//   - Run C (forced refresh): force_refresh_assets=true → the entity-image
//     cache must NOT suppress a fresh provider call.
func TestEntityImageCacheColdWarmForcedRefresh(t *testing.T) {
	vidrushImageCache = sync.Map{}
	entityImageCache = sync.Map{}
	searcher := &entityImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)

	entityPlan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Language: "en",
			MediaPlan: media.MediaPlanSpec{
				Extraction: media.MediaExtractionPolicy{EntityImages: media.EntityImagePolicy{
					Enabled: true, EntityTypes: []string{"PERSON"}, MaxPerEntity: 1,
				}},
				ForceRefreshAssets: forceRefresh,
			},
		}
	}
	inputFor := func(segmentID, textHash string) ProcessInput {
		return ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
				ID: segmentID, SegmentID: segmentID, Index: 0,
				Annotations: &scriptpkg.SceneAnnotations{
					Version: 1, Language: "en",
					PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: "Dwayne Johnson", Text: "Dwayne Johnson", Type: "PERSON"}},
				},
			}}},
			VidRushSegments: []scriptpkg.VidRushSegmentResult{{
				SegmentID: segmentID, SceneID: segmentID, TextHash: textHash,
			}},
		}
	}
	assertResolved := func(t *testing.T, label string, out *PostProcessResult) {
		t.Helper()
		image := out.UpdatedSpecScene.Scenes[0].Annotations.PrimaryEntities[0].Image
		if image == nil || image.Status != "resolved" || image.AssetID != "asset-mike" {
			t.Fatalf("%s entity image = %+v, want resolved asset-mike", label, image)
		}
	}

	// Run A — cold forced: entity-image cache skipped, provider invoked.
	a, err := processor.Process(context.Background(), entityPlan(true), inputFor("seg-a", "hash-a"))
	if err != nil {
		t.Fatalf("run A failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run A provider calls = %d, want 1 real entity search", searcher.calls)
	}
	assertResolved(t, "run A", a)

	// Run B — warm: same entity query, new segment identity, no forced refresh
	// → the per-query entity-image cache satisfies the lookup, no provider call.
	b, err := processor.Process(context.Background(), entityPlan(false), inputFor("seg-b", "hash-b"))
	if err != nil {
		t.Fatalf("run B failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run B provider calls = %d, want still 1 (entity cache hit must not search)", searcher.calls)
	}
	assertResolved(t, "run B", b)

	// Run C — forced refresh: the entity-image cache must not suppress a fresh
	// provider call for the same entity query.
	c, err := processor.Process(context.Background(), entityPlan(true), inputFor("seg-c", "hash-c"))
	if err != nil {
		t.Fatalf("run C failed: %v", err)
	}
	if searcher.calls != 2 {
		t.Fatalf("run C provider calls = %d, want 2 (forced refresh must re-search)", searcher.calls)
	}
	assertResolved(t, "run C", c)
}

// TestInternetImagesResearchPathCacheColdWarm certifies the research-path
// cache lifecycle: entity_images binding is DISABLED and the single-shot
// LLM scene text (and therefore the segment-level TextHash) changes between
// runs, yet the per-query internet_images cache keyed on (topic, query,
// language) must still replay assets without re-calling the provider:
//   - Run A (cold forced): force_refresh_assets=true → a real provider call.
//   - Run B (warm, regenerated SegmentID/TextHash): force_refresh_assets=false
//     → every query is served from the per-query cache, no provider call,
//     cache state HIT_EXACT.
func TestInternetImagesResearchPathCacheColdWarm(t *testing.T) {
	vidrushImageCache = sync.Map{}
	entityImageCache = sync.Map{}
	searcher := &countingImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)

	plan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Topic:    "civiltà maya",
			Language: "it",
			MediaPlan: media.MediaPlanSpec{
				ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
				ForceRefreshAssets: forceRefresh,
				// EntityImages is deliberately left disabled: the research
				// path derives image queries from Insights.ImageQueries, not
				// from entity-image bindings.
			},
		}
	}
	input := func(segmentID, textHash string) ProcessInput {
		return ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
			SegmentID: segmentID,
			TextHash:  textHash,
			Insights: scriptpkg.SegmentInsights{
				ImageQueries: []string{"chichen itza maya"},
			},
		}}}
	}

	// Run A — cold forced: real provider call, per-query cache stored.
	a, err := processor.Process(context.Background(), plan(true), input("scene-0", "cold-text-hash"))
	if err != nil {
		t.Fatalf("run A failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run A provider calls = %d, want 1 real search", searcher.calls)
	}
	if got := a.VidRushSegments[0].Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("run A cache state = %q, want REFRESHED", got)
	}

	// Run B — warm, regenerated scene (different TextHash): the segment-level
	// cache (keyed on TextHash) misses, but the per-query cache (keyed on
	// topic+query+language) must hit and produce HIT_EXACT with no provider call.
	b, err := processor.Process(context.Background(), plan(false), input("scene-0", "warm-text-hash"))
	if err != nil {
		t.Fatalf("run B failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run B provider calls = %d, want still 1 (per-query cache hit must not search)", searcher.calls)
	}
	if got := b.VidRushSegments[0].Cache.InternetImages; got != "HIT_EXACT" {
		t.Fatalf("run B cache state = %q, want HIT_EXACT", got)
	}
	if got := len(b.VidRushSegments[0].Assets.SecondaryImages); got != 1 {
		t.Fatalf("run B secondary images = %d, want 1 replayed candidate", got)
	}
}

// TestInternetImagesProcessorCachesEmptyResultsInL2 certifies that an empty
// provider result is durable-cached in L2 (TTL 48h) so a warm replay is
// deterministic and does not re-call the provider, while staying out of the
// no-TTL L1 in-memory map:
//   - Run A (cold forced): force_refresh_assets=true → a real provider call,
//     the empty result is stored in L2.
//   - Run B (warm): force_refresh_assets=false → the L2 hit replays the empty
//     result as HIT_EXACT with zero provider calls.
func TestInternetImagesProcessorCachesEmptyResultsInL2(t *testing.T) {
	vidrushImageCache = sync.Map{}
	entityImageCache = sync.Map{}
	searcher := &emptyInternetImageSearcher{}
	processor := NewInternetImagesProcessorWithCache(searcher, newMemoryVidRushCache())

	plan := func(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Topic:    "civiltà maya",
			Language: "it",
			MediaPlan: media.MediaPlanSpec{
				ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
				ForceRefreshAssets: forceRefresh,
			},
		}
	}
	input := func(segmentID, textHash string) ProcessInput {
		return ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
			SegmentID: segmentID,
			TextHash:  textHash,
			Insights: scriptpkg.SegmentInsights{
				ImageQueries: []string{"valle remota"},
			},
		}}}
	}

	// Run A — cold forced: real provider call, empty result cached in L2.
	a, err := processor.Process(context.Background(), plan(true), input("scene-0", "cold-text-hash"))
	if err != nil {
		t.Fatalf("run A failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run A provider calls = %d, want 1 real search", searcher.calls)
	}
	if got := a.VidRushSegments[0].Cache.InternetImages; got != "REFRESHED" {
		t.Fatalf("run A cache state = %q, want REFRESHED", got)
	}

	// Run B — warm: L2 hit replays the empty result, no provider call.
	b, err := processor.Process(context.Background(), plan(false), input("scene-0", "cold-text-hash"))
	if err != nil {
		t.Fatalf("run B failed: %v", err)
	}
	if searcher.calls != 1 {
		t.Fatalf("run B provider calls = %d, want still 1 (L2 empty hit must not search)", searcher.calls)
	}
	if got := b.VidRushSegments[0].Cache.InternetImages; got != "HIT_EXACT" {
		t.Fatalf("run B cache state = %q, want HIT_EXACT", got)
	}
	if got := len(b.VidRushSegments[0].Assets.SecondaryImages); got != 0 {
		t.Fatalf("run B secondary images = %d, want 0 replayed candidates", got)
	}
}
