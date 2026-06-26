package scripts

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type utilityEntityExtractor struct {
	calls int
	model string
}

func (f *utilityEntityExtractor) ExtractEntitiesFromScriptWithModel(_ context.Context, segments []string, _ int, model string) (*asset.FullEntityAnalysis, error) {
	f.calls++
	f.model = model
	return &asset.FullEntityAnalysis{
		TotalSegments: len(segments),
		TotalEntities: 1,
		SegmentEntities: []asset.SegmentEntities{{
			SegmentIndex: 0,
			SegmentText: segments[0],
			NomiSpeciali: []string{"AcmeAI"},
			ParoleImportanti: []string{"model"},
		}},
	}, nil
}

type utilityInsightBuilder struct{ calls int }
func (b *utilityInsightBuilder) Build(_ context.Context, _, _, _ string) ScriptInsights {
	b.calls++
	return ScriptInsights{ImportantWords: []string{"model"}, SpecialNames: []string{"AcmeAI"}}
}

func TestEntityExtractionUtilityRunAndApplyToMap(t *testing.T) {
	extractor := &utilityEntityExtractor{}
	insights := &utilityInsightBuilder{}
	utility := NewEntityExtractionUtility(extractor, insights, "metadata-model", zap.NewNop())
	result, err := utility.Run(context.Background(), "Title", "AcmeAI released a model.", "")
	require.NoError(t, err)
	require.Equal(t, 1, extractor.calls)
	require.Equal(t, "metadata-model", extractor.model)
	require.Equal(t, 1, insights.calls)
	require.Contains(t, result.EntitiesJSON, "AcmeAI")
	out := map[string]any{}
	result.ApplyToMap(out)
	require.Equal(t, result.EntitiesJSON, out["entities_json"])
	require.Equal(t, []string{"AcmeAI"}, out["special_names"])
	require.Equal(t, []string{"model"}, out["important_words"])
}
