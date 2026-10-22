package linguistics

// Phase 8 split: pure data-cloning helpers. Extracted from the slim
// lexicon_registry.go so the orchestrator file stays focused on
// load + resolve orchestration. Used by Resolve to return a deep
// clone so callers cannot mutate the registered profile (the
// registry itself is constructed once at bootstrap and never
// mutated afterwards — but the cloned profile returned to callers
// must be safe for downstream filtering that may add/remove keys).

// cloneProfile returns a deep copy of p. Nil-passthrough is preserved
// so Resolve's fallback path can return cloneProfile(r.fallback)
// without a nil-check at every call site.
func cloneProfile(p *LexiconProfile) *LexiconProfile {
	if p == nil {
		return nil
	}
	out := &LexiconProfile{
		StopWords:         cloneStringSet(p.StopWords),
		FunctionWords:     cloneStringSet(p.FunctionWords),
		EntityBlocklist:   cloneStringSet(p.EntityBlocklist),
		NegativeParticles: cloneStringSet(p.NegativeParticles),
		VisualVerbs:       cloneStringSet(p.VisualVerbs),
		VerbSuffixes:      append([]string(nil), p.VerbSuffixes...),
		PhrasePolicy:      p.PhrasePolicy,
		WordSets:          cloneWordSets(p.WordSets),
		PhraseSets:        clonePhraseSets(p.PhraseSets),
	}
	return out
}

// cloneStringSet returns a deep copy of src. Empty-input preserves
// the empty-map contract (returns a fresh empty map, not nil) so
// downstream `_, ok := m[word]` lookups don't surprise with a nil-map
// assignment.
func cloneStringSet(src map[string]struct{}) map[string]struct{} {
	if len(src) == 0 {
		return map[string]struct{}{}
	}
	dst := make(map[string]struct{}, len(src))
	for k := range src {
		dst[k] = struct{}{}
	}
	return dst
}

// cloneWordSets deep-copies the named word sets so a resolved profile cannot
// share mutable maps with the registry.
func cloneWordSets(src map[string]map[string]struct{}) map[string]map[string]struct{} {
	if len(src) == 0 {
		return map[string]map[string]struct{}{}
	}
	dst := make(map[string]map[string]struct{}, len(src))
	for name, set := range src {
		dst[name] = cloneStringSet(set)
	}
	return dst
}

// clonePhraseSets deep-copies the named phrase lists.
func clonePhraseSets(src map[string][][]string) map[string][][]string {
	if len(src) == 0 {
		return map[string][][]string{}
	}
	dst := make(map[string][][]string, len(src))
	for name, phrases := range src {
		copied := make([][]string, len(phrases))
		for i, phrase := range phrases {
			copied[i] = append([]string(nil), phrase...)
		}
		dst[name] = copied
	}
	return dst
}
