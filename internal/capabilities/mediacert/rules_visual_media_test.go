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

func TestRuleStockIsolationAllowsOnlyEntityOverlayCandidates(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{ID: "segment-1", StockBound: true}}}
	result := MediaResult{Segments: []ResultSegment{{
		SegmentID: "segment-1",
		Insights:  script.SegmentInsights{Entities: []script.ExtractedEntity{{Value: "Dwayne Johnson", Type: "PERSON", IdentityID: "Q41323", SearchIntents: []string{"Dwayne Johnson portrait"}}}},
		Assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{{
			AssetID: "portrait", Provider: script.VidRushProviderInternetImages, Entity: "Dwayne Johnson portrait",
		}}},
	}}}
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("entity overlay candidate failed stock isolation: %+v", check)
	}
	result.Segments[0].Assets.Candidates[0].Entity = ""
	result.Segments[0].Assets.Candidates[0].Query = "Dwayne Johnson portrait stock photo"
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("candidate owned by an extracted entity search intent failed: %+v", check)
	}
	result.Segments[0].Assets.Candidates[0].Query = "unrelated image"
	if check := ruleStockIsolation(spec, result); check.Passed {
		t.Fatalf("unrelated provider candidate passed stock isolation: %+v", check)
	}
	result.Segments[0].Assets.Candidates[0].Provider = script.VidRushProviderInternetImages
	result.Segments[0].Assets.Candidates[0].EntityID = "Q41323"
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("candidate with the extracted entity identity failed: %+v", check)
	}
}

func TestRuleStockIsolationAcceptsPlaceAliasEntityCandidate(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{ID: "scene-1", StockBound: true}}}
	result := MediaResult{Segments: []ResultSegment{{
		SegmentID: "scene-1",
		Insights: script.SegmentInsights{
			Entities:                []script.ExtractedEntity{{Value: "Villa Pamphili", Type: "LOC"}},
			ImageEntityCanonicalIDs: map[string]string{"Villa Pamphili Roma indagini polizia giugno 2025": "entity:villa-pamphili-roma-indagini-polizia-giugno-2025"},
		},
		Assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{{
			AssetID: "place-photo", Provider: script.VidRushProviderInternetImages,
			Entity: "Villa Pamphili Roma indagini polizia giugno 2025",
		}}},
	}}}
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("LOC entity candidate failed stock isolation: %+v", check)
	}
	result.Segments[0].Assets.Candidates[0].Entity = "unrelated"
	result.Segments[0].Assets.Candidates[0].EntityID = "entity:villa-pamphili-roma-indagini-polizia-giugno-2025"
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("candidate bound to the semantic canonical entity ID failed: %+v", check)
	}
}

func TestRuleStockIsolationAllowsExplicitEntityImageSidecarPool(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{
		ID: "scene-1", StockBound: true, AllowEntityImageCandidates: true,
	}}}
	result := MediaResult{Segments: []ResultSegment{{
		SegmentID: "scene-1",
		Assets: script.SegmentAssetSelection{Candidates: []script.SegmentAssetCandidate{{
			AssetID: "place-photo", Provider: script.VidRushProviderInternetImages,
			Entity: "Villa Pamphili Roma indagini polizia giugno 2025",
		}}},
	}}}
	if check := ruleStockIsolation(spec, result); !check.Passed {
		t.Fatalf("explicit entity-image sidecar candidate failed stock isolation: %+v", check)
	}
	result.Segments[0].Assets.SecondaryImages = append(result.Segments[0].Assets.Candidates, script.SegmentAssetCandidate{
		AssetID: "selected-place-photo", Provider: script.VidRushProviderInternetImages,
	})
	if check := ruleStockIsolation(spec, result); check.Passed {
		t.Fatalf("selected provider image passed stock isolation: %+v", check)
	}
}
