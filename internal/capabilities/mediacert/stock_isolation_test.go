package mediacert

import (
	"testing"

	"github.com/stretchr/testify/require"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stockIsolationSpec mirrors a mixed Milton-style run: one generated scene
// with an image budget, one stock-folder scene that must stay media-clean.
func stockIsolationSpec() Spec {
	return Spec{
		Segments:           2,
		EntitiesPerSegment: 1,
		ImagesPerSegment:   1,
		SegmentsExpected: []SpecSegment{
			{ID: "scene-0", Subject: "sao paulo", RequiredConcepts: []string{"sao paulo"}},
			{ID: "scene-1", Subject: "pcc nos onibus", RequiredConcepts: []string{"pcc"}, StockBound: true},
		},
	}
}

func stockIsolationResult(stockSceneClean bool) MediaResult {
	clipImages := []script.SegmentAssetCandidate{{AssetID: "img-clip", Provider: "internet_images", SegmentID: "scene-0"}}
	result := MediaResult{
		JobStatus: "SUCCEEDED",
		Segments: []ResultSegment{
			{SegmentID: "scene-0", Position: 0, SourceText: "sao paulo", SourceTextHash: "h0",
				Insights: script.SegmentInsights{ImageQueries: []string{"sao paulo"}},
				Assets:   script.SegmentAssetSelection{SecondaryImages: clipImages}},
		},
	}
	stock := ResultSegment{SegmentID: "scene-1", Position: 1, SourceText: "pcc", SourceTextHash: "h1"}
	if !stockSceneClean {
		stock.Assets = script.SegmentAssetSelection{
			SecondaryImages: []script.SegmentAssetCandidate{{AssetID: "img-stock", Provider: "internet_images", SegmentID: "scene-1"}},
			PrimaryVideo:    &script.SegmentAssetCandidate{AssetID: "clip-stock", Provider: "artlist", SegmentID: "scene-1"},
		}
	}
	result.Segments = append(result.Segments, stock)
	return result
}

func TestRuleStockIsolationPassesCleanStockScene(t *testing.T) {
	check := ruleStockIsolation(stockIsolationSpec(), stockIsolationResult(true))
	require.True(t, check.Passed, "violations = %#v", check.Violations)
	require.Equal(t, 1, check.TotalCount)
	require.Equal(t, 1, check.PassCount)
}

func TestRuleStockIsolationRejectsProviderMediaOnStockScene(t *testing.T) {
	check := ruleStockIsolation(stockIsolationSpec(), stockIsolationResult(false))
	require.False(t, check.Passed, "provider media on a stock-bound segment must fail STOCK ISOLATION")
	require.Len(t, check.Violations, 2, "one violation for the winner, one for the image candidates")
	require.Equal(t, "scene-1", check.Violations[0].SegmentID)
}

func TestRuleStockIsolationVacuousWithoutStockScenes(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{ID: "scene-0", Subject: "x"}}}
	check := ruleStockIsolation(spec, MediaResult{Segments: []ResultSegment{{SegmentID: "scene-0"}}})
	require.True(t, check.Passed)
	require.Equal(t, 1, check.TotalCount, "boolean checks render 1/1")
}

func TestRuleImageFanoutSkipsStockBoundScene(t *testing.T) {
	spec := stockIsolationSpec()
	result := stockIsolationResult(true)
	// scene-1 carries no provider images at all; IMAGE FANOUT must not demand
	// the image budget for a stock-bound scene.
	check := ruleImageFanout(spec, result)
	require.True(t, check.Passed, "violations = %#v", check.Violations)
}

func TestCertifierRunsStockIsolationInCanonicalRuleSet(t *testing.T) {
	// The canonical set gains exactly one rule for stock isolation on top of
	// the previous ten (identity, immutability, profiles, relevance,
	// grounding, fanout, query ownership, asset ownership, reuse, policy,
	// contamination).
	require.Len(t, AllRules(), 12)
}
