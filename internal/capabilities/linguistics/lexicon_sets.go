package linguistics

// Named word-set accessors (Phase 9 split). These are the
// remaining linguistic vocabulary that capability code used to keep as
// literals: subordinate markers, pronouns, articles, possessives, clitic
// pronouns, pro-drop auxiliaries, contrast openers and the
// cross-language numeric overlay vocabulary. The data lives in
// config/lexicons/<lang>/<name>.txt; this file is only the read surface.

// WordSet returns the named word set for the given language, or nil when the
// language or the set is not configured. The returned map is owned by the
// registry and is read-only: callers must not mutate it (the registry is
// immutable after construction, so sharing it avoids a clone on hot paths).
func (r *LexiconRegistry) WordSet(language, name string) map[string]struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	profile := r.profiles[normalizeLang(language)]
	if profile == nil {
		return nil
	}
	return profile.WordSets[name]
}
