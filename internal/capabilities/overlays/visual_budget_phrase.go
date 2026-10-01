package overlays

import (
	"sort"
	"strings"
)

// ApplyPhraseOverlayBudget deduplicates phrase items across the entire run,
// chooses the highest-priority unique phrases up to the default cap, and
// retains the input ordering among admitted items. Ties preserve the original
// order. Non-phrase items are copied through unchanged.
func ApplyPhraseOverlayBudget(items []OverlayItem) ([]OverlayItem, PhraseOverlayBudget) {
	return ApplyPhraseOverlayBudgetWithLimit(items, MaxPhraseOverlaysPerRun)
}

// ApplyPhraseOverlayBudgetWithLimit is ApplyPhraseOverlayBudget with a
// caller-selected phrase ceiling. phraseLimit <= 0 keeps the default ceiling.
func ApplyPhraseOverlayBudgetWithLimit(items []OverlayItem, phraseLimit int) ([]OverlayItem, PhraseOverlayBudget) {
	limit := EffectivePhraseOverlayLimit(phraseLimit)
	phraseIndices := make([]int, 0, len(items))
	bestByText := make(map[string]int)
	for i, item := range items {
		if item.Kind != "text_phrase" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if key == "" {
			continue
		}
		if existing, ok := bestByText[key]; ok {
			if overlayItemPriority(item) > overlayItemPriority(items[existing]) {
				bestByText[key] = i
			}
			continue
		}
		bestByText[key] = i
	}
	for _, index := range bestByText {
		phraseIndices = append(phraseIndices, index)
	}
	sort.SliceStable(phraseIndices, func(i, j int) bool {
		left, right := phraseIndices[i], phraseIndices[j]
		if lp, rp := overlayItemPriority(items[left]), overlayItemPriority(items[right]); lp != rp {
			return lp > rp
		}
		return left < right
	})
	if len(phraseIndices) > limit {
		phraseIndices = phraseIndices[:limit]
	}
	keep := make(map[int]struct{}, len(phraseIndices))
	for _, index := range phraseIndices {
		keep[index] = struct{}{}
	}
	out := make([]OverlayItem, 0, len(items))
	seenPhraseText := make(map[string]struct{}, len(bestByText))
	for i, item := range items {
		if item.Kind != "text_phrase" {
			out = append(out, item)
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if _, admitted := keep[i]; !admitted {
			continue
		}
		if _, duplicate := seenPhraseText[key]; duplicate {
			continue
		}
		seenPhraseText[key] = struct{}{}
		out = append(out, item)
	}
	return out, MeasurePhraseOverlayBudgetWithLimit(out, limit)
}

// MeasurePhraseOverlayBudget reports how many unique grounded phrase items
// are present in an already compiled plan against the default ceiling. It
// does not change the plan.
func MeasurePhraseOverlayBudget(items []OverlayItem) PhraseOverlayBudget {
	return MeasurePhraseOverlayBudgetWithLimit(items, MaxPhraseOverlaysPerRun)
}

// MeasurePhraseOverlayBudgetWithLimit is MeasurePhraseOverlayBudget with a
// caller-selected ceiling. requested <= 0 keeps the default ceiling.
func MeasurePhraseOverlayBudgetWithLimit(items []OverlayItem, requested int) PhraseOverlayBudget {
	budget := PhraseOverlayBudget{Requested: EffectivePhraseOverlayLimit(requested)}
	seen := make(map[string]struct{})
	for _, item := range items {
		if item.Kind != "text_phrase" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	budget.Materialized = len(seen)
	if budget.Materialized > budget.Requested {
		budget.Materialized = budget.Requested
	}
	budget.Shortfall = budget.Requested - budget.Materialized
	return budget
}
