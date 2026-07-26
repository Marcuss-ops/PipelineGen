package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestFinalizeVidRushBindings(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-001",
		TextHash:  "hash-1",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			{AssetID: "bad", Provider: "artlist", Score: 0.99},
			{AssetID: "video-1", Provider: "artlist", Query: "factory", SourceURL: "https://artlist.example/video-1", Score: 0.8},
			{AssetID: "image-1", Provider: "internet_images", Query: "factory", PreviewURL: "https://images.example/image-1", Score: 0.7},
			{AssetID: "image-1", Provider: "internet_images", Query: "factory", PreviewURL: "https://images.example/image-1", Score: 0.6},
		}},
	}}

	got := FinalizeVidRushBindings(segments, false)
	if len(got) != 1 {
		t.Fatalf("segments = %d, want 1", len(got))
	}
	seg := got[0]
	if len(seg.Assets.Candidates) != 2 {
		t.Fatalf("candidates = %d, want invalid/duplicate candidates removed", len(seg.Assets.Candidates))
	}
	if seg.Assets.PrimaryVideo == nil || seg.Assets.PrimaryVideo.AssetID != "video-1" {
		t.Fatalf("primary video = %+v, want video-1", seg.Assets.PrimaryVideo)
	}
	if len(seg.Assets.SecondaryImages) != 1 || seg.Assets.SecondaryImages[0].AssetID != "image-1" {
		t.Fatalf("secondary images = %+v, want one image-1", seg.Assets.SecondaryImages)
	}
	if seg.Assets.CandidateSetHash == "" || seg.Assets.Candidates[0].CandidateSetHash == "" {
		t.Fatal("candidate set hash must be surfaced on selection and candidates")
	}
	if seg.Cache.Binding != "MISS" {
		t.Fatalf("binding cache = %q, want MISS", seg.Cache.Binding)
	}

	warm := FinalizeVidRushBindings(got, false)
	if warm[0].Cache.Binding != "HIT_EXACT" {
		t.Fatalf("warm binding cache = %q, want HIT_EXACT", warm[0].Cache.Binding)
	}
}
