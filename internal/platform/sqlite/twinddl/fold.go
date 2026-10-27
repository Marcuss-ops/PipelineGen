package twinddl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// migrationFile is one discovered SQL migration file.
type migrationFile struct {
	version int
	path    string
	scope   string
}

// ParseScope reads the optional `-- database: <scope>[, ...]` directive from a
// migration file's SQL comment header ("all" when absent or unknown), with
// the same semantics as internal/platform/sqlite's parseMigrationScope.
func ParseScope(content []byte) string {
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		content = content[3:]
	}
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "--") {
			return "all"
		}
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "-- database:") {
			continue
		}
		rest := strings.TrimSpace(trimmed[len("-- database:"):])
		if rest == "" {
			return "all"
		}
		var out []string
		for _, s := range strings.Split(strings.ToLower(rest), ",") {
			s = strings.TrimSpace(s)
			switch s {
			case "primary", "observability", "cache", "jobs", "all":
				out = append(out, s)
			default:
				return "all"
			}
		}
		return strings.Join(out, ",")
	}
	return "all"
}

// scopeCovers reports whether a parsed scope covers the named target.
func scopeCovers(scope, target string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == "all" {
		return true
	}
	for _, s := range strings.Split(scope, ",") {
		if strings.TrimSpace(s) == target {
			return true
		}
	}
	return false
}

// parseVersion mirrors the runner's filename versioning (NNN_*.sql, with 000
// reserved for the consolidated baseline).
func parseVersion(name string) (int, error) {
	idx := strings.Index(name, "_")
	if idx <= 0 || idx > 3 {
		return 0, fmt.Errorf("twinddl: invalid migration filename %q", name)
	}
	return strconv.Atoi(name[:idx])
}

// DiscoverCorpus returns the in-scope migration files of a corpus directory,
// sorted by version ascending (baseline first).
func DiscoverCorpus(dir, scope string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("twinddl: read corpus %s: %w", dir, err)
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseVersion(e.Name())
		if err != nil {
			continue // non-migration .sql files are skipped silently
		}
		full := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		fileScope := ParseScope(raw)
		if !scopeCovers(fileScope, scope) {
			continue
		}
		out = append(out, migrationFile{version: version, path: full, scope: fileScope})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// FoldCorpus folds every in-scope migration file of a corpus into one live-
// shaped schema snapshot: the statement order of the version-ordered corpus is
// the order a database sees them.
func FoldCorpus(dir, scope string) (*FoldState, error) {
	files, err := DiscoverCorpus(dir, scope)
	if err != nil {
		return nil, err
	}
	st := NewFoldState()
	for _, f := range files {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			return nil, err
		}
		if err := FoldDeclarations(string(raw), st); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f.path), err)
		}
	}
	return st, nil
}
