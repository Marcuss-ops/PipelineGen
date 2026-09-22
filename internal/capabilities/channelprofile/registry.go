package channelprofile

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// DocumentVersion is the only accepted channel-profile document version. A
// future shape change bumps it, so a stale file fails at boot instead of
// being read with today's meaning.
const DocumentVersion = 1

// DefaultChannelID is the reserved profile key applied to any request that
// DECLARES a channel but matches no specific profile. It is opt-in: the
// document only falls back to it when it contains one. A request with an
// empty channel_id never matches — channel defaults are opt-in per request,
// never a silent global rewrite.
const DefaultChannelID = "default"

// document is the YAML file shape (config/channel_profiles.yaml).
type document struct {
	Version  int       `yaml:"version"`
	Profiles []Profile `yaml:"profiles"`
}

var (
	registryMu sync.RWMutex
	installed  = map[string]Profile{}
)

// Parse decodes and validates a document without touching the registry.
// Validation is fail-closed: one bad profile rejects the whole document, so a
// typo can never ship half a channel's defaults.
func Parse(data []byte) ([]Profile, error) {
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("channelprofile: decode document: %w", err)
	}
	if doc.Version != DocumentVersion {
		return nil, fmt.Errorf("channelprofile: document version %d is not %d", doc.Version, DocumentVersion)
	}
	seen := make(map[string]bool, len(doc.Profiles))
	for i, p := range doc.Profiles {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("channelprofile: profiles[%d]: %w", i, err)
		}
		key := strings.TrimSpace(p.ChannelID)
		if seen[key] {
			return nil, fmt.Errorf("channelprofile: channel %q is declared twice", key)
		}
		seen[key] = true
	}
	return doc.Profiles, nil
}

// Install replaces the registry with the given (already validated) profiles.
// It is a whole-set swap: the registry never holds a partially applied edit.
func Install(profiles []Profile) {
	next := make(map[string]Profile, len(profiles))
	for _, p := range profiles {
		next[strings.TrimSpace(p.ChannelID)] = p
	}
	registryMu.Lock()
	installed = next
	registryMu.Unlock()
}

// Load reads path, validates it and installs it. Any failure is an error: a
// configured profile file that does not parse must stop the process rather
// than quietly disable every channel default.
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("channelprofile: read %s: %w", path, err)
	}
	profiles, err := Parse(data)
	if err != nil {
		return err
	}
	Install(profiles)
	return nil
}

// LoadOptional is Load with one tolerated absence: a missing file means "no
// channel profiles configured" (the feature is additive), while a file that
// exists and is wrong still fails. The bool reports whether a file was found.
func LoadOptional(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("channelprofile: stat %s: %w", path, err)
	}
	return true, Load(path)
}

// Lookup resolves the profile for a channel id: an exact match first, then
// the reserved default profile. The second result is false when the request
// declared no channel or no profile covers it — the caller then applies
// nothing (channels are open-ended, profiles are curated).
func Lookup(channelID string) (Profile, bool) {
	key := strings.TrimSpace(channelID)
	if key == "" {
		return Profile{}, false
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	if p, ok := installed[key]; ok {
		return p, true
	}
	if p, ok := installed[DefaultChannelID]; ok && key != DefaultChannelID {
		return p, true
	}
	return Profile{}, false
}

// Snapshot returns the installed profiles in no particular order. It exists
// for observability and tests; mutating the returned slice changes nothing.
func Snapshot() []Profile {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Profile, 0, len(installed))
	for _, p := range installed {
		out = append(out, p)
	}
	return out
}

// Reset clears the registry. Tests only.
func Reset() {
	registryMu.Lock()
	installed = map[string]Profile{}
	registryMu.Unlock()
}
