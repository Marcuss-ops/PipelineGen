package linguistics

// VerbSuffixes returns the verb suffixes for the given language.
// Phase 8 split: morphology domain. Suffixes drive lemma-stripping
// heuristics in intent resolvers (e.g., "running" → "run" via "-ing"
// stripping). Per-language suffixes live in the LexiconProfile.
// VerbSuffixes field; missing language configuration is an error rather than
// a synthesized cross-language safety net.
func (r *LexiconRegistry) VerbSuffixes(language string) []string {
	return r.Resolve(language).VerbSuffixes
}
