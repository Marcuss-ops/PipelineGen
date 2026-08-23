package linguistics

// Stop-words domain accessors (Phase 8 split). These are the
// read-only accessors that downstream intent resolvers, entity
// filters and language detectors use to filter out high-frequency /
// low-semantic-value tokens from candidate phrase streams. Every
// accessor delegates to the explicitly configured language profile.

// StopWords returns the stop-word set for the given language.
func (r *LexiconRegistry) StopWords(language string) map[string]struct{} {
	return r.Resolve(language).StopWords
}

// FunctionWords returns the function-word set for the given language.
func (r *LexiconRegistry) FunctionWords(language string) map[string]struct{} {
	return r.Resolve(language).FunctionWords
}

// EntityBlocklist returns the entity blocklist for the given language.
func (r *LexiconRegistry) EntityBlocklist(language string) map[string]struct{} {
	return r.Resolve(language).EntityBlocklist
}

// NegativeParticles returns the negative-particle set for the given
// language.
func (r *LexiconRegistry) NegativeParticles(language string) map[string]struct{} {
	return r.Resolve(language).NegativeParticles
}

// VisualVerbs returns the visual-verb set for the given language.
func (r *LexiconRegistry) VisualVerbs(language string) map[string]struct{} {
	return r.Resolve(language).VisualVerbs
}
