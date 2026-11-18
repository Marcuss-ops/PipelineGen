package overlays

// Retired generated IDs remain resolvable only so stale explicit payloads can
// fail closed. Status and membership live in the shared animation registry.
var (
	retiredShortPhraseRecipes       = RetiredAnimationCandidates("phrase.short")
	removedGeneratedPhraseMotionIDs = RetiredAnimationCandidates("phrase.all")
	removedGeneratedPhraseMotions   = func() map[string]struct{} {
		removed := make(map[string]struct{}, len(removedGeneratedPhraseMotionIDs)+1)
		for _, id := range removedGeneratedPhraseMotionIDs {
			removed[id] = struct{}{}
		}
		for _, id := range retiredShortPhraseRecipes {
			removed[id] = struct{}{}
		}
		removed["chromatic_aberration_pop"] = struct{}{}
		return removed
	}()
)
