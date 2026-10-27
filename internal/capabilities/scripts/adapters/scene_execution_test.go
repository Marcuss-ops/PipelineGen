package adapters

import (
	"context"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func fixedMediaScene() scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "intro", SegmentID: "intro-segment", Index: 0, Text: "authoritative intro",
		Kind: scriptpkg.SceneIntro, ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "intro-clip"}},
	}
}

func fixedMediaSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: "intro-segment", SceneID: "intro", Position: 0,
		Text: "authoritative intro", TextHash: "intro-hash",
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Insights:      scriptpkg.SegmentInsights{ImageQueries: []string{"must not search"}},
		Assets:        scriptpkg.SegmentAssetSelection{PrimaryVideo: &scriptpkg.SegmentAssetCandidate{AssetID: "intro-clip", Provider: "youtube"}},
	}
}

func TestFixedMediaDoesNotEnterVisualPlanning(t *testing.T) {
	resolver := &countingVisualResolver{}
	visualProcessor := NewVisualPlanningProcessor(resolver, fixedPlanner{id: "replacement"}, nil, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "job", MediaPlan: mediadomain.MediaPlanSpec{Mode: mediadomain.MediaPlanModeHybrid}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{fixedMediaScene()}}}

	result, err := visualProcessor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 || result.Changed || len(result.VisualPlans) != 0 {
		t.Fatalf("fixed scene entered visual planning: calls=%d result=%+v", resolver.calls, result)
	}

	// The image-generation half of this guard now lives with the processor
	// that owns it: adapters/processor fixed_media_guard_test.go
}

func TestFixedMediaSkipsInternetImageSearch(t *testing.T) {
	images := &countingImageSearcher{}
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "fixed", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{fixedMediaScene()}}, VidRushSegments: []scriptpkg.VidRushSegmentResult{fixedMediaSegment()}}

	imageResult, err := NewMediaResolverImageStageWithCatalog(images, nil, nil).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if images.calls != 0 || imageResult.VidRushSegments[0].Cache.InternetImages != "BYPASSED" {
		t.Fatalf("fixed scene entered image search: calls=%d result=%+v", images.calls, imageResult)
	}
}

func TestFixedMediaBlocksIncrementalFanout(t *testing.T) {
	images := &countingImageSearcher{}
	fanout := NewVidRushProviderFanout(images)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
		InternetImages: mediadomain.MediaToggleEnabled,
	}}}
	segment := fixedMediaSegment()
	got, err := fanout.ResolveProviders(context.Background(), plan, segment)
	if err != nil {
		t.Fatal(err)
	}
	if images.calls != 0 || got.Assets.PrimaryVideo == nil || got.Assets.PrimaryVideo.AssetID != "intro-clip" {
		t.Fatalf("fixed fanout changed authoritative media: images=%d result=%+v", images.calls, got)
	}

	// The old SourceResolver fallback half is gone with VidRushSourceResolver;
	// the fanout guard above remains the production surface.
}

func TestFixedMediaSkipsMaterializerAndPreservesAuthoritativeAsset(t *testing.T) {
	materializer := newTestMaterializationProcessor(nil, nil)
	segment := fixedMediaSegment()
	materialized, err := materializer.Materialize(context.Background(), nil, segment)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Assets.PrimaryVideo == nil || materialized.Assets.PrimaryVideo.AssetID != "intro-clip" {
		t.Fatalf("fixed materializer changed authoritative asset: %+v", materialized)
	}
}
