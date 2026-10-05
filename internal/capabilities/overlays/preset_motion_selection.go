// Package overlays — preset_motion_selection.go: the deterministic phrase /
// text motion selectors.
//
// Split out of preset_selection.go to respect max_lines_per_file_strict=600
// (same package, no behavior change).
package overlays

import (
	"strings"
)

func selectPhrasePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_phrase", phrasePresetCandidates)
}

func selectPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	if len(pool) > 0 {
		// Explicit channel pools retain their editorial entrance preference,
		// while remaining strictly inside the caller's certified vocabulary.
		visibleEntrances := visiblePhraseEntrancePool(pool)
		if len(visibleEntrances) > 0 && ordinal%3 == 0 {
			return selectMotionFromPool(jobID, sceneID, "visible_entrance", ordinal/3, visibleEntrances)
		}
		return selectMotionFromPool(jobID, sceneID, "explicit", ordinal, pool)
	}
	// The default is a full deterministic no-repeat walk of the 123-motion
	// catalog. Do not siphon every third phrase into a smaller "visible"
	// subset: that makes the large catalog repeat much earlier than necessary.
	sequence := defaultPhraseMotionSequence(jobID, sceneID)
	if len(sequence) == 0 {
		return ""
	}
	return sequence[ordinal%len(sequence)]
}

// selectLongPhraseMotion keeps longer cards on block-level entrances such as
// a soft reveal, line slide, or restrained scale — but the rotation the caller
// asked for always survives. The editorial preference is the intersection of
// the long-phrase list with pool; when that intersection is degenerate (empty,
// or a SINGLE motion) it cannot rotate, and every long phrase would render the
// identical animation. That is the defect this guard closes: a narrow channel
// pool can intersect the long-phrase list in only one id, collapsing every
// long phrase onto that single animation.
//
// Precedence: a rotating intersection (>= 2 motions) > the caller's pool (a
// channel profile's explicit, already-certified vocabulary) > the long-phrase
// list (only when the caller supplied no pool at all). A single-motion pool is
// honoured verbatim — an operator who names one motion asked for one motion.
func selectLongPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	compatible := make([]string, 0, len(longPhraseMotionCandidates))
	allowed := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		allowed[id] = struct{}{}
	}
	for _, id := range longPhraseMotionCandidates {
		if len(pool) == 0 {
			compatible = append(compatible, id)
			continue
		}
		if _, ok := allowed[id]; ok {
			compatible = append(compatible, id)
		}
	}
	switch {
	case len(compatible) >= 2 && len(pool) == 0:
		// Keep the default long-card lane a simple no-repeat walk too; routing
		// it through selectPhraseMotion would apply a smaller explicit-pool
		// entrance subset and reintroduce repeats before the pool is exhausted.
		return selectMotionFromPool(jobID, sceneID, "long_phrase_default", ordinal, compatible)
	case len(compatible) >= 2:
		return selectPhraseMotion(jobID, sceneID, ordinal, compatible)
	case len(pool) > 0:
		return selectPhraseMotion(jobID, sceneID, ordinal, pool)
	default:
		return selectPhraseMotion(jobID, sceneID, ordinal, compatible)
	}
}

// visiblePhraseEntrancePool limits the guaranteed entrance slot to certified
// motions with an unmistakable reveal. The general phrase pool remains fully
// available in the other slots, including calmer fades and settles.
//
// When a caller supplies an explicit rotation pool, the pool is honoured
// verbatim: a calmer pool with no visible entrances stays calmer because the
// caller asked for it. The default rotation bypasses this filtering and walks
// the complete catalog without repeats.
func visiblePhraseEntrancePool(pool []string) []string {
	if len(pool) == 0 {
		pool = phraseMotionCandidates
	}
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if strings.HasPrefix(id, "typewriter_") ||
			strings.Contains(id, "slide") || strings.Contains(id, "reveal") ||
			strings.Contains(id, "stagger") || strings.Contains(id, "cascade") ||
			strings.Contains(id, "lift") || strings.Contains(id, "fold") ||
			strings.Contains(id, "pop") || strings.Contains(id, "scale_in") ||
			strings.Contains(id, "scale_push") || strings.Contains(id, "focus_rise") {
			out = append(out, id)
		}
	}
	return out
}

func defaultPhraseMotionSequence(jobID, sceneID string) []string {
	families := append([]string(nil), defaultPhraseMotionFamilies...)
	if len(families) == 0 {
		return nil
	}
	seededFamily := selectPreset(jobID, sceneID, "run", "important_phrase_motion_family", families)
	for i, family := range families {
		if family == seededFamily {
			families = append(families[i:], families[:i]...)
			break
		}
	}
	rotated := make(map[string][]string, len(families))
	maxLen := 0
	for _, family := range families {
		candidates := phraseMotionFamilyCandidates(family)
		if len(candidates) == 0 {
			continue
		}
		seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, candidates)
		start := 0
		for i, candidate := range candidates {
			if candidate == seeded {
				start = i
				break
			}
		}
		order := make([]string, len(candidates))
		for i := range candidates {
			order[i] = candidates[(start+i)%len(candidates)]
		}
		rotated[family] = order
		if len(order) > maxLen {
			maxLen = len(order)
		}
	}
	sequence := make([]string, 0, len(phraseMotionCandidates))
	for rank := 0; rank < maxLen; rank++ {
		for _, family := range families {
			if motions := rotated[family]; rank < len(motions) {
				sequence = append(sequence, motions[rank])
			}
		}
	}
	return sequence
}

func selectMotionFromPool(jobID, sceneID, family string, ordinal int, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, candidates)
	start := 0
	for i, candidate := range candidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return candidates[(start+ordinal)%len(candidates)]
}

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// that lowers to composition tracks only, so an id outside this list either
// needs a text-animator stack or a glow the native text lane rejects — it
// cannot render here.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}

// LongPhraseMotionCandidates exposes the read-only block-level entrance list
// the planner prefers for cards of 8+ words. selectLongPhraseMotion intersects
// a caller pool with it and falls back to the pool itself when that
// intersection cannot rotate, so operators and contract tests can check a
// channel profile's pool actually rotates its long cards.
func LongPhraseMotionCandidates() []string {
	return append([]string(nil), longPhraseMotionCandidates...)
}

// certifiedPhraseFamily returns the subset of the production-safe phrase
// vocabulary that belongs to a public motion family. Families with no
// render-safe members are intentionally rejected by the planner.
func certifiedPhraseFamily(family string) []string {
	return append([]string(nil), phraseMotionFamilyCandidates(family)...)
}

func phraseMotionFamilyCandidates(family string) []string {
	switch family {
	case "modern_apple":
		return modernAppleMotionCandidates
	case "typewriter":
		return typewriterMotionCandidates
	case "text_3d_v1", "3d":
		return text3DMotionCandidates
	case "classic_apple":
		return classicAppleMotionCandidates
	case "documentary_clean_v1":
		return documentaryCleanPhraseMotionCandidates
	default:
		return nil
	}
}

func combineMotionPools(pools ...[]string) []string {
	total := 0
	for _, pool := range pools {
		total += len(pool)
	}
	out := make([]string, 0, total)
	for _, pool := range pools {
		out = append(out, pool...)
	}
	return out
}

// RenderSafeTextMotions returns the render-safe text-motion vocabulary. It is
// the read-only projection of the single owner of that list (the rotation pool
// itself), so a consumer — including a structural test — never keeps a second
// copy that could drift from what the planner can actually emit.
func RenderSafeTextMotions() []string {
	return append([]string(nil), renderSafeTextMotions...)
}

// SelectTextMotion chooses the entrance motion of one generated TEXT item
// (entity name card, quote, important word, number, keyword). The item's
// preset alone would fall back to the preset's own motion — which on the
// installed catalog is the glyph-level apple_phrase_v2, i.e. exactly the
// animator stack this lane rejects — so every generated text item states its
// motion explicitly. A new job fingerprint can select another treatment while
// retries of the same job stay bit-identical.
func SelectTextMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "text_motion", generatedTextMotions)
}

func containsMotion(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
