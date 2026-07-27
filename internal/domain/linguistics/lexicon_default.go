package linguistics

import "sync"

// Phase 8 split: package-level global defaults isolated from the
// LexiconRegistry struct (which lives in lexicon_registry.go). The
// composition root calls SetDefaultLexicon once at bootstrap;
// downstream callers reach for DefaultLexicon when they need a
// ready-to-use registry without an explicit constructor argument.

// defaultLexiconMu protects reads + writes of defaultLexicon against
// concurrent SetDefaultLexicon / DefaultLexicon calls.
var defaultLexiconMu sync.RWMutex

// defaultLexicon is the lazily-initialised package-level registry.
// Initialised on first DefaultLexicon() call with newBuiltInLexicon()
// if SetDefaultLexicon has not yet been called.
var defaultLexicon *LexiconRegistry

// SetDefaultLexicon installs the package-level default lexicon registry.
// The composition root calls this once at bootstrap; tests may replace
// it to exercise file-backed fixtures. A nil r is silently ignored
// (matches pre-Phase-8 behaviour; composition sites rely on it).
func SetDefaultLexicon(r *LexiconRegistry) {
	if r == nil {
		return
	}
	defaultLexiconMu.Lock()
	defer defaultLexiconMu.Unlock()
	defaultLexicon = r
}

// DefaultLexicon returns the global default lexicon registry. If none
// has been installed yet, it creates a built-in registry with
// per-language profiles (en, it, es, fr, de) plus a union fallback.
// That fallback exists only so tests and explicit dev bootstrap can
// keep running before the composition root installs a file-backed
// registry.
func DefaultLexicon() *LexiconRegistry {
	defaultLexiconMu.RLock()
	if defaultLexicon != nil {
		lex := defaultLexicon
		defaultLexiconMu.RUnlock()
		return lex
	}
	defaultLexiconMu.RUnlock()

	defaultLexiconMu.Lock()
	defer defaultLexiconMu.Unlock()
	if defaultLexicon == nil {
		defaultLexicon = newBuiltInLexicon()
	}
	return defaultLexicon
}
