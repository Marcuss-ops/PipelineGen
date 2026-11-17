package linguistics

import (
	"fmt"
	"sync"
)

// Phase 8 split: package-level global defaults isolated from the
// Profiles struct (which lives in lexicon_registry.go). The
// composition root calls SetDefaultProfiles once at bootstrap;
// downstream callers reach for DefaultProfiles when they need a
// ready-to-use registry without an explicit constructor argument.

// defaultProfilesMu protects reads + writes of defaultProfiles against
// concurrent SetDefaultProfiles / DefaultProfiles calls.
var defaultProfilesMu sync.RWMutex

// defaultProfiles is installed by the composition root. It is never
// lazily manufactured because that would hide a broken deployment.
var defaultProfiles *Profiles

// SetDefaultProfiles installs the package-level default lexicon profiles.
// The composition root calls this once at bootstrap; tests may replace
// it to exercise file-backed fixtures. Nil is rejected explicitly.
func SetDefaultProfiles(r *Profiles) error {
	if r == nil {
		return fmt.Errorf("lexicon profiles: nil default is not allowed")
	}
	defaultProfilesMu.Lock()
	defer defaultProfilesMu.Unlock()
	defaultProfiles = r
	return nil
}

// DefaultProfiles returns the global default lexicon profiles. The
// process must install it during composition-root bootstrap first.
func DefaultProfiles() *Profiles {
	defaultProfilesMu.RLock()
	if defaultProfiles != nil {
		lex := defaultProfiles
		defaultProfilesMu.RUnlock()
		return lex
	}
	defaultProfilesMu.RUnlock()

	defaultProfilesMu.Lock()
	defer defaultProfilesMu.Unlock()
	if defaultProfiles == nil {
		panic("lexicon profiles: default profiles has not been installed")
	}
	return defaultProfiles
}

// DefaultProfilesOrNil exposes the installed registry to low-level helpers
// that can operate without linguistic filtering during isolated tests. It
// never constructs fallback data; production composition still uses
// DefaultProfiles and therefore fails fast when bootstrap is incomplete.
func DefaultProfilesOrNil() *Profiles {
	defaultProfilesMu.RLock()
	defer defaultProfilesMu.RUnlock()
	return defaultProfiles
}
