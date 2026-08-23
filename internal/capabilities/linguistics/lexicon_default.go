package linguistics

import (
	"fmt"
	"sync"
)

// Phase 8 split: package-level global defaults isolated from the
// LexiconRegistry struct (which lives in lexicon_registry.go). The
// composition root calls SetDefaultLexicon once at bootstrap;
// downstream callers reach for DefaultLexicon when they need a
// ready-to-use registry without an explicit constructor argument.

// defaultLexiconMu protects reads + writes of defaultLexicon against
// concurrent SetDefaultLexicon / DefaultLexicon calls.
var defaultLexiconMu sync.RWMutex

// defaultLexicon is installed by the composition root. It is never
// lazily manufactured because that would hide a broken deployment.
var defaultLexicon *LexiconRegistry

// SetDefaultLexicon installs the package-level default lexicon registry.
// The composition root calls this once at bootstrap; tests may replace
// it to exercise file-backed fixtures. Nil is rejected explicitly.
func SetDefaultLexicon(r *LexiconRegistry) error {
	if r == nil {
		return fmt.Errorf("lexicon registry: nil default is not allowed")
	}
	defaultLexiconMu.Lock()
	defer defaultLexiconMu.Unlock()
	defaultLexicon = r
	return nil
}

// DefaultLexicon returns the global default lexicon registry. The
// process must install it during composition-root bootstrap first.
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
		panic("lexicon registry: default registry has not been installed")
	}
	return defaultLexicon
}

// DefaultLexiconOrNil exposes the installed registry to low-level helpers
// that can operate without linguistic filtering during isolated tests. It
// never constructs fallback data; production composition still uses
// DefaultLexicon and therefore fails fast when bootstrap is incomplete.
func DefaultLexiconOrNil() *LexiconRegistry {
	defaultLexiconMu.RLock()
	defer defaultLexiconMu.RUnlock()
	return defaultLexicon
}
