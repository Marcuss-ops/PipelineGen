package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stockGateSpec builds a two-scene spec mirroring the Milton mixed payload:
// scene-0 is a generated clip scene, scene-1 carries a direct stock binding
// (the segment stock_folder shorthand expanded into the caller bindings).
func stockGateSpec() (scriptpkg.SpecSceneOutput, []scriptpkg.StockBindingInput) {
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
		{ID: "scene-0", SegmentID: "scene-0", Index: 0, Text: "generated clip scene"},
		{ID: "scene-1", SegmentID: "scene-1", Index: 1, Text: "stock scene", Kind: scriptpkg.SceneStock},
	}}
	bindings := []scriptpkg.StockBindingInput{{
		Index: 1, SceneID: "scene-1", SegmentID: "scene-1",
		FolderID: "stock-folder", FolderLink: "https://drive.google.com/drive/folders/stock-folder", Source: "drive",
	}}
	return spec, bindings
}

func stockGateSegments() []scriptpkg.VidRushSegmentResult {
	return []scriptpkg.VidRushSegmentResult{
		{SegmentID: "scene-0", SceneID: "scene-0", Position: 0, TextHash: "hash-0",
			Insights: scriptpkg.SegmentInsights{ArtlistQueries: []string{"clip query"}, ImageQueries: []string{"clip image query"}}},
		{SegmentID: "scene-1", SceneID: "scene-1", Position: 1, TextHash: "hash-1",
			Insights: scriptpkg.SegmentInsights{ArtlistQueries: []string{"stock query"}, ImageQueries: []string{"stock image query"}}},
	}
}

// Recorders capture WHICH queries reached each provider, so the tests can
// prove the stock query never searched while the generated scene keeps its
// legitimate discovery.
type stockGateArtlistSearcher struct{ queries []string }

func (s *stockGateArtlistSearcher) SearchClips(_ context.Context, _ string, queries []string) ([]ArtlistClipMatch, error) {
	s.queries = append(s.queries, queries...)
	if len(queries) == 0 {
		return nil, nil
	}
	return []ArtlistClipMatch{{Phrase: queries[0], ClipNames: []string{"n"}, ClipDriveLinks: []string{"https://cdn.example/n"}, Remote: true}}, nil
}

type stockGateImageSearcher struct{ queries []string }

func (s *stockGateImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.queries = append(s.queries, req.Query)
	return []scriptpkg.SegmentAssetCandidate{{AssetID: "img-gate", Provider: "internet_images", Query: req.Query, SourceURL: "https://images.example/x.jpg"}}, nil
}

func containsQuery(queries []string, want string) bool {
	for _, q := range queries {
		if strings.TrimSpace(q) == want {
			return true
		}
	}
	return false
}

func TestSceneHasDirectStockBindingMatchesBothSurfaces(t *testing.T) {
	spec, bindings := stockGateSpec()

	require.True(t, sceneHasDirectStockBinding(spec, bindings, "scene-1", "scene-1", 1),
		"scene-1 must be detected as stock-bound via caller bindings")
	require.False(t, sceneHasDirectStockBinding(spec, bindings, "scene-0", "scene-0", 0),
		"scene-0 is not stock-bound")

	// The projected SpecScene binding surface must satisfy the gate too.
	spec.Scenes[1].Bindings.Stock = &scriptpkg.StockBinding{FolderID: "stock-folder"}
	require.True(t, sceneHasDirectStockBinding(spec, nil, "scene-1", "scene-1", 1),
		"projected Bindings.Stock must mark the scene stock-bound")
}

func TestStockBoundScenesBypassArtlistAndImageSearch(t *testing.T) {
	spec, bindings := stockGateSpec()
	artlist := &stockGateArtlistSearcher{}
	images := &stockGateImageSearcher{}
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "mixed", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{
			Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled,
		},
	}}
	input := ProcessInput{SpecScene: spec, StockBindings: bindings, VidRushSegments: stockGateSegments()}

	artlistResult, err := NewClipSearchProcessor(artlist).Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.False(t, containsQuery(artlist.queries, "stock query"),
		"the stock-bound segment's query must never reach the Artlist searcher (searched: %v)", artlist.queries)
	require.True(t, containsQuery(artlist.queries, "clip query"),
		"the generated clip scene keeps its legitimate search")
	require.Equal(t, "BYPASSED", artlistResult.VidRushSegments[1].Cache.Artlist)

	imageResult, err := NewMediaResolverImageStage(images).Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.False(t, containsQuery(images.queries, "stock image query"),
		"the stock-bound segment's query must never reach the image searcher (searched: %v)", images.queries)
	require.Equal(t, "BYPASSED", imageResult.VidRushSegments[1].Cache.InternetImages)
}

func TestStockBoundSegmentNeverEntersMaterialization(t *testing.T) {
	provider := &generationMaterializationProviderStub{}
	registry := NewVidRushAssetProviderRegistry()
	require.NoError(t, registry.Register(provider))
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, materializationFinalizerStub{})

	spec, bindings := stockGateSpec()
	plan := &scriptpkg.ResolvedGenerationPlan{PromptVersion: "stock-gate-v1", ImagesPerScene: 1}
	plan.MediaPlan.ProviderPolicy.ImageGeneration = mediadomain.MediaToggleEnabled

	result, err := processor.Process(context.Background(), plan, ProcessInput{
		SpecScene: spec, StockBindings: bindings, VidRushSegments: stockGateSegments(),
	})
	require.NoError(t, err)

	stock := result.VidRushSegments[1]
	// Exactly ONE provider call is allowed: scene-0's own generated image.
	// A broken gate would add the stock scene's fallback generation on top.
	require.Equal(t, 1, provider.calls, "provider calls must come from the generated scene only")
	require.Empty(t, stock.Assets.SecondaryImages, "stock-bound segment must not materialize provider images")
	require.Equal(t, "STOCK_BOUND", stock.Cache.Binding)
	require.Equal(t, "BYPASSED", stock.Cache.InternetImages)

	clip := result.VidRushSegments[0]
	require.NotEmpty(t, clip.Assets.SecondaryImages, "the generated scene must still materialize its images")
}
