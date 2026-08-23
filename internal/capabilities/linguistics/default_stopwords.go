package linguistics

import "strings"

// IsStopWord checks the configured fallback lexicon. Registry lookup belongs
// to this capability; leaf utilities receive already-selected data instead.
func IsStopWord(term string) bool {
	registry := DefaultLexiconOrNil()
	if registry == nil {
		return false
	}
	_, ok := registry.StopWords("fallback")[strings.ToLower(term)]
	return ok
}

// IsStopWordForLanguage checks the requested configured language.
func IsStopWordForLanguage(term, language string) bool {
	registry := DefaultLexiconOrNil()
	if registry == nil {
		return false
	}
	profile, err := registry.ResolveRequired(language)
	if err != nil {
		return false
	}
	_, ok := profile.StopWords[strings.ToLower(term)]
	return ok
}

// DefaultStopWords returns a defensive copy of the fallback stop-word set.
func DefaultStopWords() map[string]struct{} {
	registry := DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	return registry.StopWords("fallback")
}
