package adapters

import (
	"context"
	"errors"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSceneExecutionFiltersProtectFixedMedia(t *testing.T) {
	scenes := []scriptpkg.SpecScene{
		{ID: "intro", Index: 0, Text: "fixed", ExecutionMode: scriptpkg.SceneExecutionFixedMedia},
		{ID: "body", Index: 1, Text: "generated", ExecutionMode: scriptpkg.SceneExecutionGenerated},
	}

	nlp := filterNLPScenes(scenes)
	if len(nlp) != 1 || nlp[0].ID != "body" {
		t.Fatalf("NLP filter = %#v, want only generated body", nlp)
	}

	media := filterMediaResolutionScenes(scenes)
	if len(media) != 1 || media[0].ID != "body" {
		t.Fatalf("media filter = %#v, want only generated body", media)
	}

	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}
	if sceneAllowsNLP(spec, "intro", "", 0) {
		t.Fatal("fixed scene allowed NLP")
	}
	if sceneAllowsMediaSearch(spec, "intro", "", 0) {
		t.Fatal("fixed scene allowed media search")
	}
}

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
		Insights:      scriptpkg.SegmentInsights{ArtlistQueries: []string{"must not search"}, ImageQueries: []string{"must not search"}},
		Assets:        scriptpkg.SegmentAssetSelection{PrimaryVideo: &scriptpkg.SegmentAssetCandidate{AssetID: "intro-clip", Provider: "youtube"}},
	}
}

func TestFixedMediaDoesNotEnterVisualPlanningOrImageGeneration(t *testing.T) {
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

	imageResult, err := NewImageProcessor(nil, nil).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if imageResult.Changed || len(imageResult.SceneImages) != 0 {
		t.Fatalf("fixed scene entered image generation: %+v", imageResult)
	}
}

func TestFixedMediaSkipsArtlistAndInternetImageSearch(t *testing.T) {
	artlist := &countingArtlistSearcher{}
	images := &countingImageSearcher{}
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "fixed", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{
			Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled,
		},
	}}
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{fixedMediaScene()}}, VidRushSegments: []scriptpkg.VidRushSegmentResult{fixedMediaSegment()}}

	artlistResult, err := NewClipSearchProcessor(artlist).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if artlist.calls != 0 || artlistResult.VidRushSegments[0].Cache.Artlist != "BYPASSED" {
		t.Fatalf("fixed scene entered Artlist search: calls=%d result=%+v", artlist.calls, artlistResult)
	}

	imageResult, err := NewMediaResolverImageStage(images).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if images.calls != 0 || imageResult.VidRushSegments[0].Cache.InternetImages != "BYPASSED" {
		t.Fatalf("fixed scene entered image search: calls=%d result=%+v", images.calls, imageResult)
	}
}

func TestFixedMediaBlocksIncrementalFanoutAndSourceFallback(t *testing.T) {
	artlist := &countingArtlistSearcher{}
	images := &countingImageSearcher{}
	fanout := NewVidRushProviderFanout(artlist, images)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
		Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled,
	}}}
	segment := fixedMediaSegment()
	got, err := fanout.ResolveProviders(context.Background(), plan, segment)
	if err != nil {
		t.Fatal(err)
	}
	if artlist.calls != 0 || images.calls != 0 || got.Assets.PrimaryVideo == nil || got.Assets.PrimaryVideo.AssetID != "intro-clip" {
		t.Fatalf("fixed fanout changed authoritative media: artlist=%d images=%d result=%+v", artlist.calls, images.calls, got)
	}

	fallback := &sourceResolverStub{stage: "fallback"}
	_, err = (VidRushSourceResolver{ImageFallback: fallback}).Resolve(context.Background(), SourceResolutionRequest{
		Plan: plan, Segment: scriptpkg.CanonicalSegment{ID: "intro-segment", Text: "fixed", ExecutionMode: scriptpkg.SceneExecutionFixedMedia},
		Slot: mediadomain.SlotPrimaryVideo,
	})
	if !errors.Is(err, scriptpkg.ErrFixedMediaDownstreamForbidden) || fallback.calls != 0 {
		t.Fatalf("fixed source fallback was invoked: err=%v calls=%d", err, fallback.calls)
	}
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
