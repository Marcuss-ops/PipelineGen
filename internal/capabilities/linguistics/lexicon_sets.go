package linguistics

// Named word-set accessors (Phase 9 split). These are the
// remaining linguistic vocabulary that capability code used to keep as
// literals: subordinate markers, pronouns, articles, possessives, clitic
// pronouns, pro-drop auxiliaries, contrast openers and the
// cross-language numeric overlay vocabulary. The data is generated from
// BrainRust/lexicon/profiles; this file is only the read surface.

// WordSet returns the named word set for the given language, or nil when the
// language or the set is not configured. The returned map is owned by the
// registry and is read-only: callers must not mutate it (the snapshot is
// immutable after construction, so sharing it avoids a clone on hot paths).
func (r *Profiles) WordSet(language, name string) map[string]struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lang := normalizeLang(language)
	profile := r.profiles[lang]
	if profile == nil && lang == "fallback" {
		profile = r.fallback
	}
	if profile == nil {
		return nil
	}
	return profile.WordSets[name]
}
