package overlays

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// MeasurePhraseOverlayBudgetWithLimit is MeasurePhraseOverlayBudget with a
// caller-selected ceiling. requested <= 0 keeps the default ceiling.
func MeasurePhraseOverlayBudgetWithLimit(items []capmodel.OverlayItem, requested int) PhraseOverlayBudget {
	budget := PhraseOverlayBudget{Requested: EffectivePhraseOverlayLimit(requested)}
	seen := make(map[string]struct{})
	add := func(text string) {
		if key := strings.ToLower(strings.Join(strings.Fields(text), " ")); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, item := range items {
		switch item.Kind {
		case "text_phrase":
			add(item.Text)
		case capmodel.KindPhraseChain, capmodel.KindMixedChain:
			// Fused chains carry their visible text per segment. Mixed chains
			// also contain entity cards, quotes and numbers, so count only
			// text_phrase segments there; otherwise the audit understates the
			// phrase materialization and reports a false shortfall.
			if segments, ok := item.Segments(); ok {
				for _, segment := range segments {
					if item.Kind == capmodel.KindPhraseChain || segment.Kind == "text_phrase" {
						add(segment.Text)
					}
				}
			}
		}
	}
	budget.Materialized = len(seen)
	if budget.Materialized > budget.Requested {
		budget.Materialized = budget.Requested
	}
	budget.Shortfall = budget.Requested - budget.Materialized
	return budget
}
