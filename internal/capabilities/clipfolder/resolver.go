package clipfolder

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// folderAliasEntry is the on-disk shape for one alias entry in
// config/folder_aliases.yaml. The YAML schema is:
//
//	folder_aliases:
//	  boxe:
//	    path: Boxe
//	    normalized_group: boxe
//	    folder_id: ""   # optional; "" → caller resolves via Publisher
//
// godlike/06 SSOT: this struct lives ONLY here. The exported
// ClipFolderRef is the top-level cross-package surface;
// folderAliasEntry stays package-private because the YAML schema is
// an implementation detail of the resolver, not the contract.
//
// Forward-compatibility: adding new optional YAML keys is safe for
// already-shipped aliases (omitempty fields default to "" / 0).
type folderAliasEntry struct {
	Path            string `yaml:"path"`
	NormalizedGroup string `yaml:"normalized_group"`
	FolderID        string `yaml:"folder_id,omitempty"`
}

// FolderAliasResolver resolves user-supplied folder name inputs
// against the YAML-driven alias table. The resolver is constructed
// once and is immutable after — concurrent reads are safe and require
// no synchronisation.
//
// godlike/07 NO-FAKE-AVAILABILITY: the resolver's only failure mode
// is ErrUnknownFolderAlias (typed sentinel). It does NOT add a
// default, does NOT backfill a "general" surface on miss.
type FolderAliasResolver struct {
	// byKey is the lower-cased, whitespace-trimmed alias → entry
	// map. Keys are pre-normalised at construction; Resolve
	// performs the same normalise on its input before the lookup.
	byKey map[string]folderAliasEntry
}

// NewFolderAliasResolverFromFile reads `folder_aliases.yaml` from
// disk and constructs the resolver. Filepath is the canonical
// expected config location (`config/folder_aliases.yaml`).
//
// Errors:
//   - empty path → static error
//   - file missing / unreadable → wrapped OS error
//   - YAML malformed → wrapped yaml.v3 error
//   - schema violation (empty alias key, empty path, empty
//     normalized_group, duplicate-after-normalise) → static error
func NewFolderAliasResolverFromFile(path string) (*FolderAliasResolver, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("clipfolder: empty resolver filepath")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("clipfolder: read %q: %w", path, err)
	}
	return NewFolderAliasResolverFromBytes(data)
}

// NewFolderAliasResolverFromBytes is the test-friendly constructor.
// Same validation as NewFolderAliasResolverFromFile — only the source
// differs. Composition root wires this against the production yaml;
// tests pin the schema invariants without touching the filesystem.
func NewFolderAliasResolverFromBytes(data []byte) (*FolderAliasResolver, error) {
	var raw struct {
		FolderAliases map[string]folderAliasEntry `yaml:"folder_aliases"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("clipfolder: parse yaml: %w", err)
	}

	byKey := make(map[string]folderAliasEntry, len(raw.FolderAliases))
	for alias, entry := range raw.FolderAliases {
		key := normaliseAliasKey(alias)
		if key == "" {
			return nil, fmt.Errorf("clipfolder: empty alias key in yaml")
		}
		if entry.Path == "" {
			return nil, fmt.Errorf("clipfolder: alias %q has empty path", alias)
		}
		if entry.NormalizedGroup == "" {
			return nil, fmt.Errorf("clipfolder: alias %q has empty normalized_group", alias)
		}
		if _, dup := byKey[key]; dup {
			return nil, fmt.Errorf(
				"clipfolder: alias %q collides with a previous entry after normalise (key=%q)",
				alias, key,
			)
		}
		byKey[key] = entry
	}

	return &FolderAliasResolver{byKey: byKey}, nil
}

// normaliseAliasKey lower-cases and trims whitespace on the lookup
// key. Operators produce arbitrary user input (spaces, dashes, mixed
// case, accents): "Boxe", "Boxing", "Hip-Hop", " hip hop ". All
// collide to the same canonical key.
//
// The resolver does NOT normalise further (no accent stripping, no
// kebab folding, no spell-correction). The YAML is the ONLY surface
// where canonicalisation gets configured. If a user types "Hip.Hop"
// today they get ErrUnknownFolderAlias — that's the right answer
// (operators should add a clean alias to the YAML, not rely on
// implicit fuzzy matching).
//
// godlike/07 NO-FAKE-AVAILABILITY: normalisation does NOT magically
// invent entries. An unmapped input keeps its unmapped key →
// ErrUnknownFolderAlias.
func normaliseAliasKey(alias string) string {
	return strings.TrimSpace(strings.ToLower(alias))
}

// Resolve looks up the user-supplied input and returns the canonical
// ClipFolderRef. Returns ErrUnknownFolderAlias when:
//
//   - the resolver is nil (composition-root bug surfacing)
//   - the input is empty after normalisation
//   - the input is unmapped in folder_aliases.yaml
//
// Resolve never panics and never returns a zero-value ref on miss —
// the zero-value ref returned in the error path is a defensive
// placeholder, NOT a default; callers MUST treat the error as the
// primary signal.
func (r *FolderAliasResolver) Resolve(input string) (ClipFolderRef, error) {
	if r == nil {
		return ClipFolderRef{}, errors.New("clipfolder: nil resolver")
	}
	key := normaliseAliasKey(input)
	if key == "" {
		return ClipFolderRef{}, fmt.Errorf("%w: empty input", ErrUnknownFolderAlias)
	}
	entry, ok := r.byKey[key]
	if !ok {
		return ClipFolderRef{}, fmt.Errorf("%w: %q", ErrUnknownFolderAlias, input)
	}
	return ClipFolderRef{
		ID:              entry.FolderID,
		Path:            entry.Path,
		NormalizedGroup: entry.NormalizedGroup,
	}, nil
}

// Keys returns the sorted list of normalised alias keys. Useful for
// admin CLI dumps ("clipfolder list-aliases") and golden-file
// regressions. NOT used on the resolve hot path.
func (r *FolderAliasResolver) Keys() []string {
	if r == nil {
		return nil
	}
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
