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

// PhraseStopWords returns the token set that must not appear INSIDE an
// extracted keyphrase for the given language: the union of the language
// profile's stop words and its function words.
//
// Unlike ResolveRequired, a language the repository does not enumerate resolves
// to the cross-linguistic `fallback` profile instead of failing. Keyphrase
// extraction runs for whatever language a job carries, and the fallback profile
// exists for exactly that case (config/lexicons/fallback/stopwords.txt is
// documented as the cross-linguistic set for unknown languages), so an
// uncovered language degrades to a smaller cross-linguistic set rather than to
// an unrelated language's words — or, worse, to no filtering at all.
//
// The union is deliberate: profiles such as ru/pl/tr/id carry function words
// but no stop words, and a keyphrase boundary is broken by either class.
func (r *LexiconRegistry) PhraseStopWords(language string) map[string]struct{} {
	profile, err := r.ResolveRequired(language)
	if err != nil {
		profile, err = r.ResolveRequired("fallback")
		if err != nil {
			return nil
		}
	}
	words := make(map[string]struct{}, len(profile.StopWords)+len(profile.FunctionWords))
	for word := range profile.StopWords {
		words[word] = struct{}{}
	}
	for word := range profile.FunctionWords {
		words[word] = struct{}{}
	}
	return words
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
