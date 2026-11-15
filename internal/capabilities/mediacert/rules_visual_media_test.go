package mediacert

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRuleImageFanoutFailsZeroImageJob(t *testing.T) {
	spec := Spec{ImagesPerSegment: 1, SegmentsExpected: []SpecSegment{{ID: "segment-1"}}}
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments:  []ResultSegment{{SegmentID: "segment-1"}},
	}
	if check := ruleImageFanout(spec, result); check.Passed {
		t.Fatalf("zero-image job passed: %+v", check)
	}
	result.Segments[0].Assets.SecondaryImages = []script.SegmentAssetCandidate{{
		SegmentID: "segment-1",
		AssetID:   "image-1",
		Provider:  script.VidRushProviderInternetImages,
	}}
	if check := ruleImageFanout(spec, result); !check.Passed {
		t.Fatalf("segment with an image failed: %+v", check)
	}
}

func TestRuleStockIsolationCountsCandidatePool(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{ID: "segment-1", StockBound: true}}}
	result := MediaResult{Segments: []ResultSegment{{
		SegmentID: "segment-1",
		Assets: script.SegmentAssetSelection{
			Candidates: []script.SegmentAssetCandidate{{
				SegmentID: "segment-1",
				AssetID:   "provider-candidate",
				Provider:  script.VidRushProviderInternetImages,
			}},
		},
	}}}
	if check := ruleStockIsolation(spec, result); check.Passed {
		t.Fatalf("stock-bound segment with a provider candidate passed: %+v", check)
	}
}
