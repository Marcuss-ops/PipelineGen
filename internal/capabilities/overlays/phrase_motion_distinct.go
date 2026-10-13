package overlays

import (
	"strconv"
	"strings"
)

// removedGeneratedPhraseMotions contains animations that are no longer
// allowed in generated overlays. They remain catalogued for historical
// render inspection, but no selector, family pool or explicit payload policy
// may assign them to a new phrase.
//
// The short-phrase entries were retired after the 2026-10-13 render review:
// the run owner rejected their look, so they must never reach a production
// render again even when a payload names one directly.
var removedGeneratedPhraseMotions = func() map[string]struct{} {
	removed := map[string]struct{}{"chromatic_aberration_pop": {}}
	for _, id := range retiredShortPhraseRecipes {
		removed[id] = struct{}{}
	}
	return removed
}()

// retiredShortPhraseRecipes are the short-phrase recipes the run owner
// rejected in the 2026-10-13 review. They stay in the ChrononTemplate catalog
// so historical plans still resolve, but no lane, family or payload policy may
// select them again.
var retiredShortPhraseRecipes = []string{
	"short_phrase_editorial_baseline_rise",
	"short_phrase_editorial_claude_diff_patch",
	"short_phrase_editorial_claude_prompt_response",
	"short_phrase_editorial_claude_terminal_focus",
	"short_phrase_editorial_lift_and_rule",
	"short_phrase_editorial_side_glide",
	"short_phrase_editorial_underline_draw",
	"short_phrase_semantic_chain_curve",
	"short_phrase_phrase_build_focus",
	"short_phrase_product_decrypted_text",
	"short_phrase_product_glare_hover",
	"short_phrase_product_line_by_line_slide",
	"short_phrase_product_warp_text",
	"short_phrase_product_weight_wave",
}

// IsRemovedGeneratedPhraseMotion reports whether an animation was retired from
// generated overlays. Payload validation uses it to fail closed instead of
// silently accepting a motion no selector may emit.
func IsRemovedGeneratedPhraseMotion(motionID string) bool {
	_, removed := removedGeneratedPhraseMotions[strings.TrimSpace(motionID)]
	return removed
}

// ensureDistinctPhraseMotions is a final run-wide guard over the category
// selectors. Short, long, heavy, and regular phrase lanes can each rotate
// without repeats internally while still colliding with one another; resolve
// those collisions against the same word-count-compatible catalog before the
// plan is sealed.
// EnsureDistinctPhraseMotions applies the run-wide no-repeat rule after
// scene-local plans have been joined. It is safe to call both during plan
// construction and at the final run boundary.
func EnsureDistinctPhraseMotions(items []OverlayItem, input PlanInput) {
	EnsureDistinctPhraseMotionsPinned(items, input, nil)
}

// EnsureDistinctPhraseMotionsPinned is the policy-aware form: indexes in
// pinned keep their MotionID verbatim (explicit animation_policies choices
// outrank automatic rotation) while their motions are pre-registered as used
// so unpinned items rotate away from them. Duplicate motions between two
// pinned items are the caller's explicit responsibility and are left alone.
func EnsureDistinctPhraseMotionsPinned(items []OverlayItem, input PlanInput, pinned map[int]bool) {
	used := make(map[string]struct{})
	for i := range items {
		if !pinned[i] || items[i].Kind != "text_phrase" {
			continue
		}
		if motion := strings.TrimSpace(items[i].MotionID); motion != "" {
			if _, removed := removedGeneratedPhraseMotions[motion]; !removed {
				used[motion] = struct{}{}
			}
		}
	}
	seed := strings.TrimSpace(input.MotionSeed)
	if seed == "" {
		seed = input.PlanID
		if input.VideoID != "" {
			seed += ":" + input.VideoID
		}
	}
	shortLimit := laneAnimationCount(input.AnimationCounts, laneShortPhrase)
	phraseLimit := laneAnimationCount(input.AnimationCounts, laneImportantPhrase)
	for i := range items {
		item := &items[i]
		if item.Kind != "text_phrase" || strings.TrimSpace(item.MotionID) == "" {
			continue
		}
		_, removed := removedGeneratedPhraseMotions[item.MotionID]
		_, duplicate := used[item.MotionID]
		if pinned[i] && !removed {
			continue
		}
		if !removed && !duplicate {
			used[item.MotionID] = struct{}{}
			continue
		}
		candidates := phraseMotionCandidatesForItem(*item, input, seed, shortLimit, phraseLimit)
		candidates = excludeRemovedGeneratedPhraseMotions(candidates)
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

func excludeRemovedGeneratedPhraseMotions(candidates []string) []string {
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if IsRemovedGeneratedPhraseMotion(candidate) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

// EnsureDistinctNumberMotions rotates date and metric presentation recipes
// across the complete plan. Their stable per-item selector can otherwise
// choose the same motion independently in separate scenes.
func EnsureDistinctNumberMotions(items []OverlayItem, animationCounts map[string]int) {
	EnsureDistinctNumberMotionsPinned(items, animationCounts, nil)
}

// EnsureDistinctNumberMotionsPinned is the policy-aware form for date/metric
// cards: pinned indexes keep their MotionID while unpinned duplicates rotate
// away. See EnsureDistinctPhraseMotionsPinned.
func EnsureDistinctNumberMotionsPinned(items []OverlayItem, animationCounts map[string]int, pinned map[int]bool) {
	used := map[string]map[string]struct{}{}
	for i := range items {
		if !pinned[i] || items[i].Kind != "number" {
			continue
		}
		if motion := strings.TrimSpace(items[i].MotionID); motion != "" {
			if used[items[i].TemplateID] == nil {
				used[items[i].TemplateID] = map[string]struct{}{}
			}
			used[items[i].TemplateID][motion] = struct{}{}
		}
	}
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
			limit = laneAnimationCount(animationCounts, laneTimelineDate)
		case "METRIC_STAT_CARD":
			key = item.TemplateID
			candidates = MetricPresentationMotionCandidates()
			limit = laneAnimationCount(animationCounts, laneMetricStat)
		default:
			continue
		}
		if used[key] == nil {
			used[key] = map[string]struct{}{}
		}
		if pinned[i] {
			continue
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
		// A lane count caps the rotation window, but never in catalog order: the
		// seeded shuffle keeps the first N recipes of a run from being the same
		// first N of every other run.
		candidates = limitedShuffledMotionPool(candidates, shortLimit, seed+"\x00short_phrase\x00"+strconv.Itoa(words))
		return excludeRemovedGeneratedPhraseMotions(candidates)
	case input.HeavyPhrasePriority > 0 && itemPriority(item) >= input.HeavyPhrasePriority:
		return excludeRemovedGeneratedPhraseMotions(visiblePhraseEntrancePool(input.PhraseMotions))
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
		return excludeRemovedGeneratedPhraseMotions(candidates)
	default:
		if len(input.PhraseMotions) > 0 {
			candidates = limitedShuffledMotionPool(input.PhraseMotions, phraseLimit, seed+"\x00phrase\x00"+strconv.Itoa(words))
			return excludeRemovedGeneratedPhraseMotions(candidates)
		}
		candidates = defaultPhraseMotionSequence(seed, "run")
		if phraseLimit > 0 {
			candidates = limitedMotionPool(candidates, phraseLimit)
		}
		return excludeRemovedGeneratedPhraseMotions(candidates)
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
