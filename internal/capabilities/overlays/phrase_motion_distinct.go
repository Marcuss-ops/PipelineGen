package overlays

import "strings"

// ensureDistinctPhraseMotions is a final run-wide guard over the category
// selectors. Short, long, heavy, and regular phrase lanes can each rotate
// without repeats internally while still colliding with one another; resolve
// those collisions against the same word-count-compatible catalog before the
// plan is sealed.
// EnsureDistinctPhraseMotions applies the run-wide no-repeat rule after
// scene-local plans have been joined. It is safe to call both during plan
// construction and at the final run boundary.
func EnsureDistinctPhraseMotions(items []OverlayItem, input PlanInput) {
	used := make(map[string]struct{})
	seed := input.PlanID
	if input.VideoID != "" {
		seed += ":" + input.VideoID
	}
	shortLimit := animationCount(input.AnimationCounts, "short_important_phrase", "short_phrases")
	phraseLimit := animationCount(input.AnimationCounts, "important_phrase", "important_phrases")
	for i := range items {
		item := &items[i]
		if item.Kind != "text_phrase" || strings.TrimSpace(item.MotionID) == "" {
			continue
		}
		if _, duplicate := used[item.MotionID]; !duplicate {
			used[item.MotionID] = struct{}{}
			continue
		}
		candidates := phraseMotionCandidatesForItem(*item, input, seed, shortLimit, phraseLimit)
		if len(candidates) == 0 {
			continue
		}
		start := 0
		for n, candidate := range candidates {
			if candidate == item.MotionID {
				start = n + 1
				break
			}
		}
		for n := 0; n < len(candidates); n++ {
			candidate := candidates[(start+n)%len(candidates)]
			if _, exists := used[candidate]; exists {
				continue
			}
			item.MotionID = candidate
			used[candidate] = struct{}{}
			break
		}
	}
}

// EnsureDistinctNumberMotions rotates date and metric presentation recipes
// across the complete plan. Their stable per-item selector can otherwise
// choose the same motion independently in separate scenes.
func EnsureDistinctNumberMotions(items []OverlayItem, animationCounts map[string]int) {
	used := map[string]map[string]struct{}{}
	for i := range items {
		item := &items[i]
		if item.Kind != "number" || strings.TrimSpace(item.MotionID) == "" {
			continue
		}
		var key string
		var candidates []string
		var limit int
		switch item.TemplateID {
		case "TIMELINE_DATE_CARD":
			key = item.TemplateID
			candidates = DatePresentationMotionCandidates()
			limit = animationCount(animationCounts, "timeline_date", "dates")
		case "METRIC_STAT_CARD":
			key = item.TemplateID
			candidates = MetricPresentationMotionCandidates()
			limit = animationCount(animationCounts, "metric_stat", "metrics")
		default:
			continue
		}
		if used[key] == nil {
			used[key] = map[string]struct{}{}
		}
		if _, duplicate := used[key][item.MotionID]; !duplicate {
			used[key][item.MotionID] = struct{}{}
			continue
		}
		candidates = limitedMotionPool(candidates, limit)
		start := 0
		for n, candidate := range candidates {
			if candidate == item.MotionID {
				start = n + 1
				break
			}
		}
		for n := 0; n < len(candidates); n++ {
			candidate := candidates[(start+n)%len(candidates)]
			if _, exists := used[key][candidate]; exists {
				continue
			}
			item.MotionID = candidate
			used[key][candidate] = struct{}{}
			break
		}
	}
}

func phraseMotionCandidatesForItem(item OverlayItem, input PlanInput, seed string, shortLimit, phraseLimit int) []string {
	words := len(strings.Fields(item.Text))
	var candidates []string
	switch {
	case EditorialSectionForItem(item) == EditorialSectionShortPhrase:
		candidates = append([]string(nil), shortPhraseMotionCandidates[words]...)
		candidates = intersectMotionPool(candidates, input.PhraseMotions)
		if len(candidates) == 0 {
			candidates = append([]string(nil), shortPhraseMotionCandidates[words]...)
		}
		return limitedMotionPool(candidates, shortLimit)
	case input.HeavyPhrasePriority > 0 && itemPriority(item) >= input.HeavyPhrasePriority:
		return visiblePhraseEntrancePool(input.PhraseMotions)
	case words >= 6:
		candidates = append([]string(nil), longPhraseMotionCandidates...)
		if len(input.PhraseMotions) > 0 {
			compatible := intersectMotionPool(candidates, input.PhraseMotions)
			if len(compatible) >= 2 {
				candidates = compatible
			}
		}
		if phraseLimit > 0 {
			candidates = limitedMotionPool(candidates, phraseLimit)
		}
		return candidates
	default:
		if len(input.PhraseMotions) > 0 {
			return limitedMotionPool(input.PhraseMotions, phraseLimit)
		}
		candidates = defaultPhraseMotionSequence(seed, "run")
		if phraseLimit > 0 {
			candidates = limitedMotionPool(candidates, phraseLimit)
		}
		return candidates
	}
}

func intersectMotionPool(candidates, allowed []string) []string {
	if len(allowed) == 0 {
		return candidates
	}
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if _, ok := set[id]; ok {
			out = append(out, id)
		}
	}
	return uniqueMotionPool(out)
}
