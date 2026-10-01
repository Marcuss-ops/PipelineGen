package wiring

import (
	"testing"

	media "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildRuntimeMediaCertSpecDoesNotInventTextSceneIdentity(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Mode: "text"}
	spec := buildRuntimeMediaCertSpec(plan)
	if spec.Segments != 0 {
		t.Fatalf("segments = %d, want 0 without an authored segment contract", spec.Segments)
	}
	if len(spec.SegmentsExpected) != 0 {
		t.Fatalf("segments_expected = %+v, want none for free-form generated scenes", spec.SegmentsExpected)
	}
}

func TestBuildRuntimeMediaCertSpecUsesExplicitIDsAndDeterministicFallback(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Mode: "text",
		Segments: []scriptpkg.ScriptSegment{
			{ID: "authored-a", Topic: "Donald Trump"},
			{Topic: "United States"},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if len(spec.SegmentsExpected) != 2 {
		t.Fatalf("segments_expected = %d, want 2", len(spec.SegmentsExpected))
	}
	if spec.SegmentsExpected[0].ID != "authored-a" || spec.SegmentsExpected[1].ID != "scene-1" {
		t.Fatalf("unexpected explicit identity contract: %+v", spec.SegmentsExpected)
	}
}

func TestBuildRuntimeMediaCertSpecAllowsEntityImageReuseFromGenericExtractionSurface(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Mode: "text",
		MediaPlan: media.MediaPlanSpec{
			Extraction: media.MediaExtractionPolicy{Include: []string{"entities"}},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if !spec.AllowCrossSceneAssetReuse {
		t.Fatal("entity extraction surface must allow canonical identity-image reuse across scenes")
	}
}

func TestBuildRuntimeMediaCertSpecDoesNotRequireSecondaryImagesForClipOnly(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode:      scriptpkg.MediaModeClipOnly,
		ImagesPerScene: 1,
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if spec.ImagesPerSegment != 0 {
		t.Fatalf("images_per_segment = %d, want 0 for clip-only video scenes", spec.ImagesPerSegment)
	}
	if spec.VideoProvider != "" {
		t.Fatalf("video_provider = %q, want none for caller-selected clip-only scenes", spec.VideoProvider)
	}
}

func TestBuildRuntimeMediaCertSpecDoesNotAssumeArtlistForMixedFolderBindings(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode: scriptpkg.MediaModeMixed,
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleDisabled},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if spec.VideoProvider != "" {
		t.Fatalf("video_provider = %q, want none when Artlist is disabled", spec.VideoProvider)
	}
}

func TestBuildRuntimeMediaCertSpecRequiresArtlistRelevanceWhenEnabled(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode: scriptpkg.MediaModeMixed,
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if spec.VideoProvider != scriptpkg.VidRushProviderArtlist {
		t.Fatalf("video_provider = %q, want %q when Artlist is enabled", spec.VideoProvider, scriptpkg.VidRushProviderArtlist)
	}
}

func TestBuildRuntimeMediaCertSpecMarksStockFolderScenesStockBound(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaMode:      scriptpkg.MediaModeMixed,
		ImagesPerScene: 1,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "scene-0", Kind: "clip", Topic: "clip scene"},
			{ID: "scene-1", Kind: "stock", Topic: "stock scene", StockFolderID: "1Sr15dwjKQkuNn9yaPBav8TwNS_lfEzd-"},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if len(spec.SegmentsExpected) != 2 {
		t.Fatalf("segments_expected = %d, want 2", len(spec.SegmentsExpected))
	}
	if spec.SegmentsExpected[0].StockBound {
		t.Fatal("clip scene must not be marked stock_bound")
	}
	if !spec.SegmentsExpected[1].StockBound {
		t.Fatal("stock-folder scene must be marked stock_bound so IMAGE FANOUT skips it and STOCK ISOLATION certifies it")
	}
}

func TestBuildRuntimeMediaCertSpecStockBoundViaFolderLink(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Segments: []scriptpkg.ScriptSegment{
			{ID: "scene-0", StockFolderLink: "https://drive.google.com/drive/folders/1Sr15dwjKQkuNn9yaPBav8TwNS_lfEzd-"},
		},
	}
	spec := buildRuntimeMediaCertSpec(plan)
	if !spec.SegmentsExpected[0].StockBound {
		t.Fatal("a stock_folder_link segment must be marked stock_bound")
	}
}
