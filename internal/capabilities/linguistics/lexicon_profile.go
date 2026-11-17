package linguistics

// LexiconProfile is the complete set of per-language linguistic data
// used by intent resolvers, entity filters and language detectors.
//
// Phase 8 split: this struct lives in its own file because every
// capability file (morphology / stopwords / aliasing / scoring) reads
// a subset of its fields. Keeping the struct definition here avoids
// polluting the slim lexicon_registry.go (which only carries the
// orchestrating struct Profiles).
type LexiconProfile struct {
	StopWords         map[string]struct{}
	FunctionWords     map[string]struct{}
	EntityBlocklist   map[string]struct{}
	NegativeParticles map[string]struct{}
	VisualVerbs       map[string]struct{}
	VerbSuffixes      []string
	PhrasePolicy      PhraseExtractionPolicy

	// WordSets holds the additional named per-language word sets from the
	// Rust-generated profile (subordinate markers, pronouns,
	// articles, possessives, clitic pronouns, pro-drop auxiliaries, contrast
	// openers, numeric overlay vocabulary). A set that has no data file is
	// absent from the map rather than present and empty; callers treat the
	// returned sets as read-only.
	WordSets map[string]map[string]struct{}
}
