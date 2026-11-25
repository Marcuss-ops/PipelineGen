package overlays

import (
	"errors"
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// Phrase budget policies. Warn is the compatibility default: the run keeps
// rendering and reports the missing phrases. Strict is fail-closed before the
// GPU render, for callers that treat the requested phrase count as a contract.
const (
	PhraseBudgetPolicyWarn   = "warn"
	PhraseBudgetPolicyStrict = "strict"
)

// ErrPhraseOverlayShortfall marks a strict-policy refusal. It is typed so a
// durable runner can classify the failure without parsing the message.
var ErrPhraseOverlayShortfall = errors.New("phrase overlay budget shortfall")

// MeasurePhraseOverlayBudgetWithLimit is MeasurePhraseOverlayBudget with a
// caller-selected ceiling. requested <= 0 keeps the default ceiling.
// ApplyPhraseBudgetPolicy turns the measured shortfall into an explicit
// outcome. An empty policy is warn (backward compatible); an unknown policy is
// treated as warn so a typo cannot silently make runs fail closed. Strict
// returns ErrPhraseOverlayShortfall when any requested phrase is missing.
func ApplyPhraseBudgetPolicy(budget PhraseOverlayBudget, policy string) (PhraseOverlayBudget, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy != PhraseBudgetPolicyStrict {
		policy = PhraseBudgetPolicyWarn
	}
	budget.Policy = policy
	if budget.Shortfall <= 0 {
		budget.Warning = ""
		return budget, nil
	}
	budget.Warning = fmt.Sprintf(
		"phrase overlay shortfall: requested %d, materialized %d, missing %d (policy=%s)",
		budget.Requested, budget.Materialized, budget.Shortfall, policy)
	if policy == PhraseBudgetPolicyStrict {
		return budget, fmt.Errorf("%w: requested %d, materialized %d, missing %d", ErrPhraseOverlayShortfall, budget.Requested, budget.Materialized, budget.Shortfall)
	}
	return budget, nil
}

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
