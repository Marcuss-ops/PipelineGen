package linguistics

// VerbSuffixes returns the verb suffixes for the given language.
// Phase 8 split: morphology domain. Suffixes drive lemma-stripping
// heuristics in intent resolvers (e.g., "running" → "run" via "-ing"
// stripping). Per-language suffixes live in the LexiconProfile.
// VerbSuffixes field; the built-in fallback lexicon (lexicon_builtin.go)
// supplies a cross-language union as the safety-net default.
func (r *LexiconRegistry) VerbSuffixes(language string) []string {
	return r.Resolve(language).VerbSuffixes
}
