package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestEffectiveVisualMomentsCountsFusedChainSegments(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "standalone"},
		{
			ID: "chain",
			Params: map[string]any{
				"chain_segments": []any{
					map[string]any{"kind": "text_phrase", "text": "one"},
					map[string]any{"kind": "text_phrase", "text": "two"},
					map[string]any{"kind": "text_phrase", "text": "three"},
				},
			},
		},
	}
	if got := effectiveVisualMoments(items); got != 4 {
		t.Fatalf("effective visual moments = %d, want 4 (1 item + 3 chain segments)", got)
	}
}
