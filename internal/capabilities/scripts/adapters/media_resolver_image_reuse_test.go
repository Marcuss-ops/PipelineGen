package adapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/stretchr/testify/require"
)

func reuseTestPlan(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: forceRefresh,
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy:     media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
			ForceRefreshAssets: forceRefresh,
		},
	}
}

func fanoutReuseInput(state string, queries ...string) ProcessInput {
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:   "fanout-image-1",
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Query:     "Garlasco",
		SourceURL: "https://images.example/fanout.jpg",
	}
	return ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "fanout-reuse-segment",
		TextHash:  "fanout-reuse-hash",
		Cache:     scriptpkg.SegmentCacheState{InternetImages: state},
		Assets: scriptpkg.SegmentAssetSelection{
			Candidates:      []scriptpkg.SegmentAssetCandidate{candidate},
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{candidate},
		},
		Insights: scriptpkg.SegmentInsights{ImageQueries: queries},
	}}}
}

// TestInternetImagesStageReusesFanoutCandidatesWithoutProviderSearch certifies
// the postprocessor does not repeat the discovery the VidRush fan-out already
// performed. HIT_EXACT means those candidates are authoritative, so the stage
// must keep them and report zero provider searches.
func TestInternetImagesStageReusesFanoutCandidatesWithoutProviderSearch(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &countingImageSearcher{}
	processor := NewMediaResolverImageStage(searcher)

	result, err := processor.Process(context.Background(), reuseTestPlan(false), fanoutReuseInput("HIT_EXACT", "Garlasco"))
	require.NoError(t, err)
	require.Equal(t, 0, searcher.calls, "fan-out HIT_EXACT must not trigger a second provider pass")
	require.Len(t, result.VidRushSegments, 1)
	segment := result.VidRushSegments[0]
	require.Equal(t, "HIT_EXACT", segment.Cache.InternetImages)
	require.Equal(t, 0, segment.Cache.InternetImagesProviderSearches)
	require.Len(t, segment.Assets.Candidates, 1, "the fan-out candidate must be preserved exactly once")
	require.Len(t, segment.Assets.SecondaryImages, 1, "the fan-out candidate must be preserved exactly once")
}

// TestInternetImagesStageForceRefreshBypassesFanoutReuse certifies that a
// forced refresh is not satisfied by a pre-existing HIT_EXACT fan-out state:
// the provider must run and the segment must end REFRESHED.
func TestInternetImagesStageForceRefreshBypassesFanoutReuse(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &countingImageSearcher{}
	processor := NewMediaResolverImageStage(searcher)

	result, err := processor.Process(context.Background(), reuseTestPlan(true), fanoutReuseInput("HIT_EXACT", "Garlasco"))
	require.NoError(t, err)
	require.Equal(t, 1, searcher.calls, "forced refresh must re-run discovery")
	require.Len(t, result.VidRushSegments, 1)
	segment := result.VidRushSegments[0]
	require.Equal(t, "REFRESHED", segment.Cache.InternetImages)
	require.Equal(t, 1, segment.Cache.InternetImagesProviderSearches)
	require.Len(t, segment.Assets.SecondaryImages, 2, "the refreshed result joins the preserved fan-out candidate")
}

type duplicateInternetImageSearcher struct{ calls int }

func (s *duplicateInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:   "duplicate-" + req.Query,
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Query:     req.Query,
		SourceURL: "https://images.example/duplicate.jpg",
	}
	return []scriptpkg.SegmentAssetCandidate{candidate, candidate}, nil
}

// TestInternetImagesStageDedupesCandidatesInCache certifies that a provider
// response containing the same image twice is collapsed before it is written
// to the cache, and a warm replay still yields exactly one candidate.
func TestInternetImagesStageDedupesCandidatesInCache(t *testing.T) {
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	searcher := &duplicateInternetImageSearcher{}
	processor := NewMediaResolverImageStage(searcher)
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "dedupe-segment",
		TextHash:  "dedupe-hash",
		Insights:  scriptpkg.SegmentInsights{ImageQueries: []string{"Garlasco"}},
	}}}

	cold, err := processor.Process(context.Background(), reuseTestPlan(true), input)
	require.NoError(t, err)
	require.Equal(t, 1, searcher.calls)
	require.Len(t, cold.VidRushSegments[0].Assets.SecondaryImages, 1, "duplicate provider URLs must be collapsed")

	warm, err := processor.Process(context.Background(), reuseTestPlan(false), input)
	require.NoError(t, err)
	require.Equal(t, 1, searcher.calls, "warm replay must be served by the cache")
	require.Equal(t, "HIT_EXACT", warm.VidRushSegments[0].Cache.InternetImages)
	require.Len(t, warm.VidRushSegments[0].Assets.SecondaryImages, 1, "cached duplicates must not be re-expanded")
}

type countingMaterializationCatalog struct {
	*integrationEntityImageCatalog
	materializationLookups int
}

func (c *countingMaterializationCatalog) GetMaterialization(ctx context.Context, candidateID int64) (*entitycatalog.Materialization, error) {
	c.materializationLookups++
	return c.integrationEntityImageCatalog.GetMaterialization(ctx, candidateID)
}

// TestEntityImageCatalogCandidatesBoundsMaterializationLookups certifies that a
// warm catalog replay hydrates at most the requested pool size, rather than
// issuing one materialization query for every row in the 100-row scan window.
func TestEntityImageCatalogCandidatesBoundsMaterializationLookups(t *testing.T) {
	repo := newIntegrationEntityImageCatalog()
	urls := make([]string, 12)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://images.example/mj/bound-%d.jpg", i)
	}
	seedCatalogPerson(t, repo, "Michael Jordan", urls...)
	counting := &countingMaterializationCatalog{integrationEntityImageCatalog: repo}
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	require.NoError(t, err)

	pool, err := entityImageCatalogCandidates(context.Background(), counting, identity, 5)
	require.NoError(t, err)
	require.Len(t, pool.Candidates, 5)
	require.True(t, pool.Sufficient)
	require.Equal(t, 5, counting.materializationLookups,
		"materialization lookups must stop at the requested candidate limit")
}
