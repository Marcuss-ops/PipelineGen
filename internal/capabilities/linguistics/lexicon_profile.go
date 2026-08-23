package linguistics

// LexiconProfile is the complete set of per-language linguistic data
// used by intent resolvers, entity filters and language detectors.
//
// Phase 8 split: this struct lives in its own file because every
// capability file (morphology / stopwords / aliasing / scoring) reads
// a subset of its fields. Keeping the struct definition here avoids
// polluting the slim lexicon_registry.go (which only carries the
// orchestrating struct LexiconRegistry).
type LexiconProfile struct {
	StopWords         map[string]struct{}
	FunctionWords     map[string]struct{}
	EntityBlocklist   map[string]struct{}
	NegativeParticles map[string]struct{}
	VisualVerbs       map[string]struct{}
	VerbSuffixes      []string
	PhrasePolicy      PhraseExtractionPolicy
}
