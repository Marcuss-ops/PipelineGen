// Package intent — stopwords.go has been removed.
// Stop-word sets are now loaded from config/lexicons/ at bootstrap
// via the linguistics.LexiconRegistry.
//
// The global registry is accessible through linguistics.DefaultLexicon()
// and is populated at composition-root time by calling
// linguistics.SetDefaultLexicon(lex).
//
// See internal/domain/linguistics/lexicon_registry.go for the
// canonical definition of the registry and its file-based profiles.
package intent
