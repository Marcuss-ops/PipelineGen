// Package overlays — preset_motion_pools.go: the certified, read-only motion
// pool accessors for the phrase/text lanes.
//
// Split out of preset_motion_selection.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package overlays

// CertifiedHeavyPhraseMotions exposes the read-only heavy-phrase entrance pool
// so a contract test can assert the heavy lane is a strict subset of the
// callable phrase catalog rather than a second, drifting vocabulary.
func CertifiedHeavyPhraseMotions() []string {
	return visiblePhraseEntrancePool(nil)
}

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// whose text, semantic-highlight span, animator and companion-layer lowering
// is implemented by RenderingGen. Typewriter stays retired; the new documentary
// highlighting family is phrase-relative and part of the certified pool.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}

// LongPhraseMotionCandidates exposes the read-only block-level entrance list
// the planner prefers for cards of 6+ words. selectLongPhraseMotion intersects
// a caller pool with it and falls back to the pool itself when that
// intersection cannot rotate, so operators and contract tests can check a
// channel profile's pool actually rotates its long cards.
func LongPhraseMotionCandidates() []string {
	return append([]string(nil), longPhraseMotionCandidates...)
}

// ShortPhraseMotionCandidates exposes the read-only ChrononTemplate motion
// pool used for phrases shorter than six words.
func ShortPhraseMotionCandidates() []string {
	return flattenShortPhraseMotionCandidates()
}

// ShortPhraseMotionsForWordCount exposes the read-only exact-cardinality
// recipe group for one to five words. A recipe only renders its own count,
// so payload policies and seeded group choices must match motion to wording.
// Callers receive a copy. Counts outside 1..5 yield no candidates.
func ShortPhraseMotionsForWordCount(wordCount int) []string {
	return append([]string(nil), shortPhraseMotionCandidates[wordCount]...)
}

// ShortPhraseMotionWordCount reports the exact word-count group a motion
// belongs to.
func ShortPhraseMotionWordCount(motionID string) (int, bool) {
	for wordCount := 1; wordCount <= 5; wordCount++ {
		for _, id := range shortPhraseMotionCandidates[wordCount] {
			if id == motionID {
				return wordCount, true
			}
		}
	}
	return 0, false
}

// IsShortPhraseRecipe reports whether motionID is one of the currently
// published exact-cardinality recipes. A prefix alone is not sufficient:
// retired IDs keep that wire prefix but must no longer be recognized.
func IsShortPhraseRecipe(motionID string) bool {
	for _, ids := range shortPhraseMotionCandidates {
		if containsMotion(ids, motionID) {
			return true
		}
	}
	return false
}

// SingleWordMotionCandidates exposes the read-only cinematic pool used for
// one-word phrase beats.
func SingleWordMotionCandidates() []string {
	return append([]string(nil), shortPhraseMotionCandidates[1]...)
}

func flattenShortPhraseMotionCandidates() []string {
	var candidates []string
	for wordCount := 1; wordCount <= 5; wordCount++ {
		candidates = append(candidates, shortPhraseMotionCandidates[wordCount]...)
	}
	return candidates
}

// certifiedPhraseFamily returns the subset of the production-safe phrase
// vocabulary that belongs to a public motion family. Families with no
// render-safe members are intentionally rejected by the planner.
func certifiedPhraseFamily(family string) []string {
	return append([]string(nil), phraseMotionFamilyCandidates(family)...)
}

// phraseMotionFamilyCandidates returns the family's members with the retired
// animations removed: a family pool is the planner's authoritative allowance,
// so a removed recipe must not survive inside one.
func phraseMotionFamilyCandidates(family string) []string {
	var pool []string
	switch family {
	case "modern_product_v1":
		pool = phraseMotionCandidates
	case "short_phrase_product_v1":
		pool = flattenShortPhraseMotionCandidates()
	case "modern_apple":
		pool = modernAppleMotionCandidates
	case "brush_v1":
		pool = brushPhraseMotionCandidates
	case "documentary_highlighting_v1":
		pool = documentaryHighlightMotionCandidates
	case "text_3d_v1", "3d":
		pool = text3DMotionCandidates
	case "classic_apple":
		pool = classicAppleMotionCandidates
	// documentary_clean_v1 was retired 2026-10-15: requesting it now falls
	// through to the default nil pool, which the planner rejects fail-closed
	// ("has no certified motions"). Its four motions stay callable through
	// modern_apple and the long-phrase lane.
	default:
		return nil
	}
	return excludeRemovedGeneratedPhraseMotions(pool)
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
