package mediacert

import (
	"strings"
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestZeroImageReasonNamesTheStageThatLostTheScene(t *testing.T) {
	verifyFailed := script.SegmentAssetCandidate{AssetID: "a", Provider: script.VidRushProviderInternetImages}
	verifyFailed.FailLifecycle(script.VidRushLifecycleStageVerify, "identity mismatch")
	acquireFailed := script.SegmentAssetCandidate{AssetID: "b", Provider: script.VidRushProviderInternetImages}
	acquireFailed.FailLifecycle(script.VidRushLifecycleStageAcquire, "http 404")
	watermarked := script.SegmentAssetCandidate{
		AssetID: "c", Provider: script.VidRushProviderInternetImages, HasWatermark: true,
	}

	cases := []struct {
		name     string
		assets   script.SegmentAssetSelection
		warnings []string
		want     string
	}{
		{
			name:     "provider outage",
			warnings: []string{"internet images provider degraded: searxng timeout"},
			want:     ZeroReasonProviderError,
		},
		{
			name:     "resolve budget",
			warnings: []string{"provider resolve exceeded 45s; continuing with 0 candidate(s) retained"},
			want:     ZeroReasonProviderTimeout,
		},
		{
			name: "empty catalog",
			want: ZeroReasonCatalogEmpty,
		},
		{
			name:   "watermark wins over lifecycle",
			assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{watermarked, verifyFailed}},
			want:   ZeroReasonWatermarkDetected,
		},
		{
			name:   "verification rejected",
			assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{verifyFailed}},
			want:   ZeroReasonVerificationRejected,
		},
		{
			name:   "acquisition failed",
			assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{acquireFailed}},
			want:   ZeroReasonAcquisitionFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ZeroImageReason(tc.assets, tc.warnings); got != tc.want {
				t.Fatalf("ZeroImageReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckSegmentImageFanoutCarriesZeroReason(t *testing.T) {
	spec := Spec{Segments: 1, ImagesPerSegment: 3, SegmentsExpected: []SpecSegment{{ID: "scene-1"}}}
	violation := CheckSegmentImageFanout(spec, ResultSegment{
		SegmentID: "scene-1",
		Warnings:  []string{"internet images provider degraded: ddg cooldown"},
	})
	if violation == nil {
		t.Fatal("expected an IMAGE FANOUT violation")
	}
	if !strings.Contains(violation.Detail, "zero_reason="+ZeroReasonProviderError) {
		t.Fatalf("violation detail = %q, want zero_reason=%s", violation.Detail, ZeroReasonProviderError)
	}
}

func TestAvailableImageCountIgnoresVideosAndDuplicates(t *testing.T) {
	assets := script.SegmentAssetSelection{
		Candidates: []script.SegmentAssetCandidate{
			{AssetID: "img-1", Provider: script.VidRushProviderInternetImages, ContentSHA256: strings.Repeat("a", 64)},
			{AssetID: "img-1-dupe", Provider: script.VidRushProviderInternetImages, ContentSHA256: strings.Repeat("a", 64)},
			{AssetID: "vid-1", Provider: "youtube", MIMEType: "video/mp4"},
		},
	}
	if got := AvailableImageCount(assets); got != 1 {
		t.Fatalf("AvailableImageCount = %d, want 1 (one unique image, video excluded)", got)
	}
}
