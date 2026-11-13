package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// panickingImageSearcher fails the run if the provider path is ever entered:
// a pinned segment must never reach discovery.
type panickingImageSearcher struct {
	calls int
}

func (s *panickingImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	return nil, nil
}

func writeTestManifest(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "media_manifest.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func testPins(t *testing.T) *MediaManifestPins {
	t.Helper()
	path := writeTestManifest(t, map[string]any{
		"schema": "media_manifest.v1",
		"entries": []map[string]any{
			{
				"id": "chiara-portrait", "role": "entity_card", "scene_id": "SCN-01",
				"query": "Chiara Binda ritratto", "sha256": "437d7377a2a8adbe7f5be03a99aebff4ccb9f6edb8cf884da1ccc2c2e45de533",
				"bytes": 8192, "path": "assets/01_base_inkblue.png",
				"source":   map[string]any{"provider": "internet_images", "source_url": "https://images.example/chiara.jpg"},
				"verified": map[string]any{"identity": true, "structure": true},
			},
		},
	})
	pins, err := LoadMediaManifestPins(path)
	require.NoError(t, err)
	require.True(t, pins.Enabled())
	require.Equal(t, 1, pins.EntryCount())
	return pins
}

func manifestTestInput() ProcessInput {
	return ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "SEG-01",
		SceneID:   "SCN-01",
		TextHash:  "manifest-pin-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"Chiara Binda ritratto"},
		},
	}}}
}

func manifestTestPlan(force bool) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: force,
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
}

// TestMediaManifestPinsBypassProviderAndScorer is the fast-path contract: a
// pinned segment is served with the offline verdicts and zero provider calls,
// even under force refresh (the operator's pick wins over fresh discovery).
func TestMediaManifestPinsBypassProviderAndScorer(t *testing.T) {
	searcher := &panickingImageSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, nil).
		WithMediaManifestPins(testPins(t))

	for _, force := range []bool{false, true} {
		result, err := processor.Process(context.Background(), manifestTestPlan(force), manifestTestInput())
		require.NoError(t, err)
		require.Len(t, result.VidRushSegments, 1)
		seg := result.VidRushSegments[0]
		require.Equal(t, MediaManifestCacheState, seg.Cache.InternetImages)
		require.Equal(t, 0, seg.Cache.InternetImagesProviderSearches)
		require.Len(t, seg.Assets.Candidates, 1)
		cand := seg.Assets.Candidates[0]
		require.Equal(t, MediaManifestProvider, cand.Provider)
		require.Equal(t, entitycatalog.CandidateSemanticAccepted, cand.SemanticStatus)
		require.True(t, ReusablePersonImageVerdict(cand), "a pin must carry a durable verdict: zero VLM calls")
		require.Equal(t, "437d7377a2a8adbe7f5be03a99aebff4ccb9f6edb8cf884da1ccc2c2e45de533", cand.ContentSHA256)
		require.False(t, cand.LifecycleFailed())
	}
	require.Equal(t, 0, searcher.calls, "pinned segments must never enter provider discovery")
}

// TestMediaManifestPinsDoNotSuppressEntityImageDiscovery is the regression
// for the per-scene portrait lane: a contextual MANIFEST_PIN is a complete
// discovery verdict for the scene image, but never for a person portrait. The
// pinned candidate must survive while the entity query still reaches the
// provider.
func TestMediaManifestPinsDoNotSuppressEntityImageDiscovery(t *testing.T) {
	searcher := &panickingImageSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, nil).
		WithMediaManifestPins(testPins(t))

	input := manifestTestInput()
	input.VidRushSegments[0].Insights.Entities = []scriptpkg.ExtractedEntity{{
		Value: "Chiara Binda", Type: "PERSON", Confidence: 1,
	}}
	plan := manifestTestPlan(false)
	plan.MediaPlan.Extraction.EntityImages = media.EntityImagePolicy{Enabled: true, Scope: "per_scene"}

	result, err := processor.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.GreaterOrEqual(t, searcher.calls, 1, "a manifest pin must not suppress entity-image discovery")
	require.Len(t, result.VidRushSegments, 1)
	seg := result.VidRushSegments[0]
	require.NotEmpty(t, seg.Assets.Candidates, "the pinned contextual candidate must be preserved")
	require.Equal(t, MediaManifestProvider, seg.Assets.Candidates[0].Provider)
}

// TestVidRushProviderFanoutPinnedEntityImagesStillSearch is the fan-out
// regression for the production final-job path. The manifest pin satisfies
// the contextual scene image, but a per-scene PERSON request must still reach
// the identity provider; otherwise every entity overlay is SEARCH_EMPTY.
func TestVidRushProviderFanoutPinnedEntityImagesStillSearch(t *testing.T) {
	searcher := &panickingImageSearcher{}
	fanout := NewVidRushProviderFanout(searcher).WithMediaManifestPins(testPins(t))

	input := manifestTestInput()
	input.VidRushSegments[0].Insights.Entities = []scriptpkg.ExtractedEntity{{
		Value: "Chiara Binda", Type: "PERSON", Confidence: 1,
	}}
	plan := manifestTestPlan(false)
	plan.MediaPlan.Extraction.EntityImages = media.EntityImagePolicy{Enabled: true, Scope: "per_scene"}

	out, err := fanout.ResolveProviders(context.Background(), plan, input.VidRushSegments[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, searcher.calls, 1, "a manifest pin must not suppress fan-out entity discovery")
	require.NotEmpty(t, out.Assets.Candidates, "the pinned contextual candidate must be preserved")
	require.Equal(t, MediaManifestProvider, out.Assets.Candidates[0].Provider)
}

// TestMediaManifestPinsServeCacheOnly verifies the pin path does not violate
// the cache_only no-provider contract (pins are local verified bytes, not a
// network source) and stays labelled MANIFEST_PIN, not CACHE_MISS.
func TestMediaManifestPinsServeCacheOnly(t *testing.T) {
	searcher := &panickingImageSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, nil).
		WithMediaManifestPins(testPins(t))

	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			Mode:           media.MediaPlanModeCacheOnly,
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	result, err := processor.Process(context.Background(), plan, manifestTestInput())
	require.NoError(t, err)
	require.Empty(t, result.Warnings)
	seg := result.VidRushSegments[0]
	require.Equal(t, MediaManifestCacheState, seg.Cache.InternetImages)
	require.Len(t, seg.Assets.Candidates, 1)
}

// TestMediaResolverImageStageUnpinnedKeepsNormalPath is the no-pins guard:
// without the opt-in the stage must behave exactly as before (provider path).
func TestMediaResolverImageStageUnpinnedKeepsNormalPath(t *testing.T) {
	searcher := &panickingImageSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, nil).
		WithMediaManifestPins(nil)

	result, err := processor.Process(context.Background(), manifestTestPlan(false), manifestTestInput())
	require.NoError(t, err)
	require.Len(t, result.VidRushSegments, 1)
	seg := result.VidRushSegments[0]
	require.Equal(t, "MISS", seg.Cache.InternetImages)
	require.Equal(t, 1, searcher.calls, "no pins: normal discovery path is preserved")
}

// TestMediaResolverImageStageUnpinnedSegmentKeepsDiscovery: pins loaded for
// one scene must not suppress discovery of segments the manifest does not pin.
func TestMediaResolverImageStageUnpinnedSegmentKeepsDiscovery(t *testing.T) {
	searcher := &panickingImageSearcher{}
	processor := NewMediaResolverImageStageWithCatalog(searcher, nil, nil).
		WithMediaManifestPins(testPins(t))

	input := manifestTestInput()
	input.VidRushSegments[0].SceneID = "SCN-OTHER"

	result, err := processor.Process(context.Background(), manifestTestPlan(false), input)
	require.NoError(t, err)
	seg := result.VidRushSegments[0]
	require.Equal(t, "MISS", seg.Cache.InternetImages)
	require.GreaterOrEqual(t, searcher.calls, 1, "an unpinned segment must keep the discovery path")
}

// TestMediaManifestPinsScopedToProject pins the collision guard: the same
// scene ids recur in every project, so a scoped manifest must only apply when
// the plan title/topic carries one of its markers.
func TestMediaManifestPinsScopedToProject(t *testing.T) {
	path := writeTestManifest(t, map[string]any{
		"schema": "media_manifest.v1",
		"scope":  map[string]any{"title_contains": []string{"Garlasco"}},
		"entries": []map[string]any{{
			"id": "scoped-portrait", "role": "entity_image", "scene_id": "SCN-01",
			"entity": "Chiara", "query": "Chiara", "sha256": "437d7377a2a8adbe7f5be03a99aebff4ccb9f6edb8cf884da1ccc2c2e45de533",
			"bytes": 8192, "path": "assets/01_base_inkblue.png",
			"source": map[string]any{"provider": "internet_images"},
		}},
	})
	pins, err := LoadMediaManifestPins(path)
	require.NoError(t, err)

	plan := manifestTestPlan(false)
	plan.Title = "Garlasco: cronologia giudiziaria"
	require.True(t, pins.AppliesTo(plan))
	plan.Title = "Un altro progetto"
	require.False(t, pins.AppliesTo(plan))

	fanout := NewVidRushProviderFanout(&panickingImageSearcher{}).WithMediaManifestPins(pins)
	out, err := fanout.ResolveProviders(context.Background(), plan, manifestTestInput().VidRushSegments[0])
	require.NoError(t, err)
	require.NotEqual(t, MediaManifestCacheState, out.Cache.InternetImages, "a non-matching plan must not receive pins")
}

// TestVidRushProviderFanoutPinsBypassProviders is the fan-out fast-path
// contract: pinned segments skip YouTube and image providers entirely. This is
// the path a plan without the internet_images postprocessor takes.
func TestVidRushProviderFanoutPinsBypassProviders(t *testing.T) {
	searcher := &panickingImageSearcher{}
	fanout := NewVidRushProviderFanout(searcher).WithMediaManifestPins(testPins(t))

	out, err := fanout.ResolveProviders(context.Background(), manifestTestPlan(false), manifestTestInput().VidRushSegments[0])
	require.NoError(t, err)
	require.Equal(t, MediaManifestCacheState, out.Cache.InternetImages)
	require.Len(t, out.Assets.Candidates, 1)
	require.Equal(t, MediaManifestProvider, out.Assets.Candidates[0].Provider)
	require.Equal(t, 0, searcher.calls, "pinned segments must never reach the provider")
}

// TestVidRushProviderFanoutUnpinnedKeepsProviders: without pins the fan-out
// keeps its normal discovery behavior.
func TestVidRushProviderFanoutUnpinnedKeepsProviders(t *testing.T) {
	searcher := &panickingImageSearcher{}
	fanout := NewVidRushProviderFanout(searcher)

	out, err := fanout.ResolveProviders(context.Background(), manifestTestPlan(false), manifestTestInput().VidRushSegments[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, searcher.calls, 1, "no pins: provider discovery is preserved")
	require.NotEqual(t, MediaManifestCacheState, out.Cache.InternetImages)
}

// TestLoadMediaManifestPinsFailClosed covers the loader contract: unknown
// schema and malformed digests are load-time errors, never silent corruption.
func TestLoadMediaManifestPinsFailClosed(t *testing.T) {
	_, err := LoadMediaManifestPins("/nonexistent/media_manifest.json")
	require.Error(t, err)

	_, err = LoadMediaManifestPins(writeTestManifest(t, map[string]any{
		"schema":  "media_manifest.v0",
		"entries": []map[string]any{},
	}))
	require.ErrorContains(t, err, "media_manifest.v1")

	_, err = LoadMediaManifestPins(writeTestManifest(t, map[string]any{
		"schema": "media_manifest.v1",
		"entries": []map[string]any{
			{"id": "bad", "scene_id": "S", "sha256": "md5-shaped-digest"},
		},
	}))
	require.ErrorContains(t, err, "malformed sha256")
}

// TestMediaManifestPinsForSceneIDNormalization: scene keys are lowercase-
// normalized so authoring tools can mix cases freely.
func TestMediaManifestPinsForSceneIDNormalization(t *testing.T) {
	pins := testPins(t)
	require.Len(t, pins.ForSceneID("scn-01"), 1)
	require.Len(t, pins.ForSceneID(" SCN-01 "), 1)
	require.Empty(t, pins.ForSceneID("SCN-02"))
}

// TestLoadMediaManifestPinsFromEnv is the opt-in contract: unset env = nil
// (no behavior change anywhere in the composition root).
func TestLoadMediaManifestPinsFromEnv(t *testing.T) {
	t.Setenv(veloxMediaManifestEnv, "")
	pins, err := LoadMediaManifestPinsFromEnv()
	require.NoError(t, err)
	require.Nil(t, pins, "unset opt-in must keep the stage off")
}
