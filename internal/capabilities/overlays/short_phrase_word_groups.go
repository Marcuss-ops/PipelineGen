package overlays

// shortPhraseRecipeWordGroups is derived from the canonical animation
// registry. Word count belongs to each registry record; this file intentionally
// carries no second copy of the short-phrase motion IDs.
var shortPhraseRecipeWordGroups = registryShortPhraseGroups()

func copyShortPhraseWordGroups() map[int][]string {
	out := make(map[int][]string, len(shortPhraseRecipeWordGroups))
	for wordCount, ids := range shortPhraseRecipeWordGroups {
		out[wordCount] = append([]string(nil), ids...)
	}
	return out
}
