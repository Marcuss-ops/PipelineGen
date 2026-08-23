package linguistics

// PhraseExtractionPolicy controls how intent resolvers and entity
// filters extract meaningful phrases from text. Each language profile
// carries its own policy so extraction rules differ between, say,
// English and Italian.
//
// Phase 8 split: aliasing domain. Multi-word phrase extraction is
// treated as lexical aliasing — a phrase like "mountain river sunrise"
// aliases to a single intent token that downstream resolvers match
// against scene-level taxonomy rather than re-tokenising the words
// individually.
type PhraseExtractionPolicy struct {
	// MinWords is the minimum number of words in a valid phrase.
	MinWords int `yaml:"min_words" json:"min_words"`
	// MaxWords is the maximum number of words in a valid phrase.
	MaxWords int `yaml:"max_words" json:"max_words"`
	// MaxResults caps the number of extracted phrases.
	MaxResults int `yaml:"max_results" json:"max_results"`
	// RejectVerbsWhenAll when true rejects phrases where every word
	// looks like a verb form (common for Italian verb-only bigrams).
	RejectVerbsWhenAll bool `yaml:"reject_verbs_when_all" json:"reject_verbs_when_all"`
}

// DefaultPhraseExtractionPolicy returns a sensible default policy
// used by the fallback profile when no explicit phrase_policy file
// is present.
func DefaultPhraseExtractionPolicy() PhraseExtractionPolicy {
	return PhraseExtractionPolicy{
		MinWords:           2,
		MaxWords:           3,
		MaxResults:         5,
		RejectVerbsWhenAll: true,
	}
}

// PhrasePolicy returns the phrase extraction policy for the given
// language. Phase 8 split: aliasing-domain accessor.
func (r *LexiconRegistry) PhrasePolicy(language string) PhraseExtractionPolicy {
	return r.Resolve(language).PhrasePolicy
}
