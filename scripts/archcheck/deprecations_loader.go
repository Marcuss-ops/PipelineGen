package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// deprecationManifest is the in-memory shape of the canonical
// deprecation registry, regardless of whether it was loaded from a
// single .yaml file or from a sharded directory.
//
// The on-disk shapes supported by loadDeprecationManifest are:
//
//  1. Legacy single-file (current production form):
//     architecture/deprecations.yaml with top-level keys
//     `deprecations:` and `audit:`. The optional
//     `open_questions:` block, if present, is parsed but NOT
//     surfaced — that field has no validator consumer today and
//     is left to a future PR to type properly.
//
//  2. Sharded directory (the planned split):
//     architecture/deprecations/
//       records/*.yaml        # one or more shards, each with a
//                             # `deprecations:` list under the
//                             # file's top-level key
//       audit.yaml            # optional, contributes `audit:`
//
// Both shapes normalize into deprecationManifest so downstream
// validators (deprecations_validator.go) do not branch on layout.
//
// Cross-shard duplicate `id` is a hard error: sharding must NEVER
// split a single record's id across files (the canonical registry
// is single-source-of-truth per AGENTS.md / zero legacy policy).
type deprecationManifest struct {
	// Deprecations is the concatenation of every `deprecations:`
	// list found in every shard, in the order returned by the
	// filesystem walk. Stable order matters for error messages —
	// the validator reports violation labels via index into this
	// slice.
	Deprecations []deprecationRecord `yaml:"deprecations"`

	// Audit comes from a single designated file (`audit.yaml`)
	// or from the legacy single-file form. Last-wins when
	// multiple sources carry the block (the directory-mode
	// layout guarantees only one source).
	Audit auditBlock `yaml:"audit"`
}

// loadDeprecationManifest dispatches on the input path. A path
// whose `os.Stat` resolves to a directory is loaded in sharded
// mode; a path that resolves to a regular YAML file is loaded in
// legacy single-file mode. Any other shape (missing path, special
// file, neither file nor directory) is a hard error so the
// validator fails closed rather than silently treating an
// unreadable source as "no records".
//
// The second return value is the list of duplicate-YAML-key
// violations (one string per offending key) detected per file.
// Callers are expected to surface those as CI violations INSTEAD
// of treating them as fatal load errors: the historical
// `checkDeprecations` returned them as one-string-per-key entries
// in the violations slice, and any CI dashboard matching that
// shape must keep doing so on the duplicate-key failure path.
//
// A non-nil error from this function means the file/directory is
// unreadable or unparseable; duplicate-key violations are NOT
// surfaced as errors.
func loadDeprecationManifest(path string) (*deprecationManifest, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("deprecations: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return loadManifestFromDirectory(path)
	}
	return loadManifestFromFile(path)
}

// loadManifestFromFile reads the legacy single-file form. It
// preserves the duplicate-YAML-key detection that the existing
// validator relied on: a YAML mapping node carrying two keys with
// the same name is a parse-time violation (yaml.v3 silently keeps
// the last value), so we surface it as one string per offending
// key in the violations slice instead of failing the load.
func loadManifestFromFile(path string) (*deprecationManifest, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("deprecations: read %s: %w", path, err)
	}

	// Per-key duplicate detection — preserve the original
	// "deprecations: duplicate YAML key …" violation shape so CI
	// dashboards that match on it keep working. yaml.v3 itself
	// silently overwrites duplicates; without this pass a malformed
	// registry would silently truncate records.
	dupKeyViolations := detectDuplicateYAMLKeys(raw, path)

	var manifest deprecationManifest
	if uerr := yaml.Unmarshal(raw, &manifest); uerr != nil {
		return nil, dupKeyViolations, fmt.Errorf("deprecations: parse %s: %w", path, uerr)
	}
	return &manifest, dupKeyViolations, nil
}

// loadManifestFromDirectory walks the directory, reads every
// shard under `records/` (sorted), and (when present) reads
// `audit.yaml`. Other YAML files at the top level are ignored;
// this is intentional so auxiliary artifact files do not silently
// pollute the registry.
//
// Cross-shard duplicate `id:` is reported via the returned error
// so callers can fail closed without losing the offending ids.
// Per-file duplicate-YAML-key violations are accumulated into a
// single slice so callers can preserve the original one-string-
// per-key emission shape.
func loadManifestFromDirectory(dir string) (*deprecationManifest, []string, error) {
	recordsDir := filepath.Join(dir, "records")
	var dupKeyViolations []string
	manifest := &deprecationManifest{}

	// Records shards: records/*.yaml (any depth = 1 under
	// `records/`). Sorted so the merged Deprecations slice is
	// deterministic across filesystems (Mac vs Linux alphabetic
	// order can otherwise scatter records differently).
	shardPaths, err := filepath.Glob(filepath.Join(recordsDir, "*.yaml"))
	if err != nil {
		return nil, dupKeyViolations, fmt.Errorf("deprecations: glob %s: %w", recordsDir, err)
	}
	sort.Strings(shardPaths)

	seenIDs := make(map[string]string) // id -> first shard path
	for _, shard := range shardPaths {
		shardManifest, shardDup, err := loadManifestFromFile(shard)
		if err != nil {
			return nil, dupKeyViolations, err
		}
		dupKeyViolations = append(dupKeyViolations, shardDup...)
		for _, rec := range shardManifest.Deprecations {
			if rec.ID == "" {
				// Empty id is a record-level violation; defer to the
				// validator's required-field check rather than fail
				// here.
				manifest.Deprecations = append(manifest.Deprecations, rec)
				continue
			}
			if prev, dup := seenIDs[rec.ID]; dup {
				return nil, dupKeyViolations, fmt.Errorf(
					"deprecations: duplicate id %q across shards (first seen in %s, again in %s)",
					rec.ID, prev, shard)
			}
			seenIDs[rec.ID] = shard
			manifest.Deprecations = append(manifest.Deprecations, rec)
		}
	}

	// audit.yaml (optional, last-wins if multiple — but the
	// directory-mode contract guarantees only one source).
	auditPath := filepath.Join(dir, "audit.yaml")
	if _, err := os.Stat(auditPath); err == nil {
		raw, err := os.ReadFile(auditPath)
		if err != nil {
			return nil, dupKeyViolations, fmt.Errorf("deprecations: read %s: %w", auditPath, err)
		}
		if v := detectDuplicateYAMLKeys(raw, auditPath); len(v) > 0 {
			dupKeyViolations = append(dupKeyViolations, v...)
		}
		// Mirror the legacy single-file shape: audit.yaml
		// carries an `audit:` top-level block, so unmarshal
		// into a wrapper with the same field name. Reading the
		// raw bytes directly into auditBlock would silently
		// produce a zero value (yaml.v3 ignores leading
		// unmapped keys).
		var wrapped struct {
			Audit auditBlock `yaml:"audit"`
		}
		if err := yaml.Unmarshal(raw, &wrapped); err != nil {
			return nil, dupKeyViolations, fmt.Errorf("deprecations: parse %s: %w", auditPath, err)
		}
		manifest.Audit = wrapped.Audit
	}

	return manifest, dupKeyViolations, nil
}
