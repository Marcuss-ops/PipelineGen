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

func TestValidVidRushCandidate_RejectsForbiddenProviders(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		sourceURL string
		wantValid bool
	}{
		// Forbidden providers — MUST be rejected
		{name: "youtube provider rejected", provider: "youtube", sourceURL: "https://example.com/video.mp4", wantValid: false},
		{name: "generated_images rejected", provider: "generated_images", sourceURL: "https://example.com/img.png", wantValid: false},
		{name: "image_generation rejected", provider: "image_generation", sourceURL: "https://example.com/img.png", wantValid: false},
		{name: "local_youtube_stock rejected", provider: "local_youtube_stock", sourceURL: "https://example.com/video.mp4", wantValid: false},
		{name: "local_stock rejected", provider: "local_stock", sourceURL: "https://example.com/video.mp4", wantValid: false},

		// Allowed providers — MUST pass when provenance is present
		{name: "artlist with source_url", provider: "artlist", sourceURL: "https://artlist.io/clip/123", wantValid: true},
		{name: "artlist without provenance", provider: "artlist", sourceURL: "", wantValid: false},
		{name: "internet_images with preview_url", provider: "internet_images", sourceURL: "https://images.example/1.jpg", wantValid: true},
		{name: "pexels accepted", provider: "pexels", sourceURL: "https://images.pexels.com/1.jpg", wantValid: true},
		{name: "pixabay accepted", provider: "pixabay", sourceURL: "https://cdn.pixabay.com/1.jpg", wantValid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := scriptpkg.SegmentAssetCandidate{
				AssetID:   "candidate-1",
				Provider:  tt.provider,
				SourceURL: tt.sourceURL,
				PreviewURL: func() string {
					if tt.provider == "artlist" {
						return ""
					}
					return tt.sourceURL
				}(),
				DriveLink: func() string {
					if tt.provider == "artlist" && tt.sourceURL == "" {
						return ""
					}
					if tt.provider == "artlist" {
						return tt.sourceURL
					}
					return ""
				}(),
				Score: 1.0,
			}
			got := validVidRushCandidate(candidate)
			if got != tt.wantValid {
				t.Errorf("validVidRushCandidate() = %v, want %v (provider=%q, sourceURL=%q)",
					got, tt.wantValid, tt.provider, tt.sourceURL)
			}
		})
	}
}

func TestValidVidRushCandidate_RejectsYouTubeURLs(t *testing.T) {
	// Even if the provider field is not "youtube", a candidate whose
	// SourceURL contains youtube.com or youtu.be MUST be rejected.
	tests := []struct {
		name      string
		provider  string
		sourceURL string
	}{
		{name: "youtube.com in URL rejected", provider: "artlist", sourceURL: "https://www.youtube.com/watch?v=abc123"},
		{name: "youtu.be in URL rejected", provider: "internet_images", sourceURL: "https://youtu.be/abc123"},
		{name: "youtube-nocookie rejected", provider: "artlist", sourceURL: "https://www.youtube-nocookie.com/embed/abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := scriptpkg.SegmentAssetCandidate{
				AssetID:   "candidate-yt-url",
				Provider:  tt.provider,
				SourceURL: tt.sourceURL,
				DriveLink: tt.sourceURL,
				Score:     1.0,
			}
			if validVidRushCandidate(candidate) {
				t.Errorf("validVidRushCandidate() = true, want false for URL %q", tt.sourceURL)
			}
		})
	}
}

func TestFinalizeVidRushBindings_StripsForbiddenProviders(t *testing.T) {
	// End-to-end: FinalizeVidRushBindings must strip candidates whose
	// provider is in the forbidden list, even when they have provenance.
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-yt-test",
		TextHash:  "hash-yt",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			{AssetID: "good-video", Provider: "artlist", SourceURL: "https://artlist.io/clip/123", DriveLink: "https://drive.example/123", Score: 0.9},
			{AssetID: "bad-yt", Provider: "youtube", SourceURL: "https://youtube.com/watch?v=abc", Score: 0.95},
			{AssetID: "bad-yt-url", Provider: "pexels", SourceURL: "https://youtube.com/watch?v=xyz", Score: 0.8},
			{AssetID: "bad-gen", Provider: "generated_images", SourceURL: "https://ai.example/gen.png", Score: 0.7},
			{AssetID: "good-img", Provider: "pexels", SourceURL: "https://images.pexels.com/1.jpg", Score: 0.6},
		}},
	}}

	got := FinalizeVidRushBindings(segments, false)
	if len(got) != 1 {
		t.Fatalf("segments = %d, want 1", len(got))
	}
	candidates := got[0].Assets.Candidates
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (only good-video + good-img); got: %+v", len(candidates), candidates)
	}
	for _, c := range candidates {
		if c.AssetID == "bad-yt" || c.AssetID == "bad-yt-url" || c.AssetID == "bad-gen" {
			t.Errorf("forbidden candidate %q was not stripped", c.AssetID)
		}
	}
}
