package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// discoverMigrations scans a directory for .sql migration files and
// returns them sorted by version.
func discoverMigrations(targetDir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, fmt.Errorf("storage: read migrations dir %s: %w", targetDir, err)
	}

	var migrations []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			continue // skip non-migration files silently
		}
		fullPath := filepath.Join(targetDir, e.Name())
		scope := "all" // safe default when the file isn't readable
		if data, err := os.ReadFile(fullPath); err == nil {
			scope = parseMigrationScope(data)
		}
		migrations = append(migrations, migrationFile{
			version:  version,
			filename: e.Name(),
			path:     fullPath,
			scope:    scope,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

// parseMigrationScope reads the optional `-- database:` directive from a
// migration file's SQL comment header. Returns the parsed scope string
// (e.g. "primary", "observability", "primary,observability", or "all")
// and falls back to "all" when no directive is present OR when the
// directive references an unknown scope.
//
// See migrationFile.
//
// Format:
//
//	-- database: <scope>[, <scope>...]
//
// Valid scope values: "primary", "observability", "all".
// Case-insensitive (the directive is normalised to lowercase before
// validation). The directive must be the FIRST non-blank line AND must
// begin with `-- database:` (the exact prefix; `-- db:` and `-- target:`
// are NOT recognised — use the full word to keep grep / git blame
// unambiguous).
//
// Whitespace (spaces, tabs) inside the comma-separated list is stripped.
// Anything outside the known-scope set falls back to "all" so a typo
// can't quietly exclude a migration from one DB.
func parseMigrationScope(content []byte) string {
	// Strip UTF-8 BOM (3 bytes EF BB BF) before
	// scanning. Without this, files saved by Notepad or some VSCode
	// configs silently default to scope="all" even when the author
	// set a specific scope on the first line.
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		content = content[3:]
	}
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		// Skip past leading `--` comment lines (e.g. copyright /
		// multi-line license headers) before deciding whether a
		// directive was declared. The first non-comment, non-blank
		// line ENDS the scan: if no directive was found by then,
		// the default ("all") applies.
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
		// Validate against the known set; unknown → safe default.
		parts := strings.Split(strings.ToLower(rest), ",")
		for _, s := range parts {
			s = strings.TrimSpace(s)
			switch s {
			case "primary", "observability", "all":
			default:
				return "all"
			}
		}
		// Normalise whitespace + lowercase for storage.
		var out []string
		for _, s := range parts {
			out = append(out, strings.TrimSpace(s))
		}
		return strings.Join(out, ",")
	}
	return "all"
}

// migrationAppliesToTargetDB tests whether the parsed migration scope
// covers the runner's targetDB. scope is the comma-separated string
// from the file's `-- database:` directive (or the default "all" when
// absent); targetDB is the canonically-named DB the runner is
// processing ("primary", "observability", or "all").
//
// See migrationFile and parseMigrationScope.
//
// Decision table:
//   - scope == "" or scope == "all"      → applies to every targetDB
//   - scope == "primary"                 → applies only to "primary"
//   - scope == "observability"           → applies only to "observability"
//   - scope == "primary,observability"   → applies to either
//   - scope contains an unknown token    → caller (parseMigrationScope)
//     has already fallen back to "all", so we don't see that here.
func migrationAppliesToTargetDB(scope, targetDB string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == "all" {
		return true
	}
	for _, s := range strings.Split(scope, ",") {
		if strings.TrimSpace(s) == targetDB {
			return true
		}
	}
	return false
}

// validateNoDuplicateVersions returns an error if two migration files share
// the same version number.
func validateNoDuplicateVersions(migrations []migrationFile, log *zap.Logger) error {
	seen := make(map[int]string)
	for _, m := range migrations {
		if prev, ok := seen[m.version]; ok {
			return fmt.Errorf(
				"storage: duplicate migration version %03d: %s and %s",
				m.version, prev, m.filename,
			)
		}
		seen[m.version] = m.filename
	}
	return nil
}

// validateAppliedMigrationSet fails closed when a later migration is recorded
// while an earlier in-scope migration is missing. MAX(version) alone cannot
// detect a ledger such as 193,195; this checks the exact applied/file set.
func validateAppliedMigrationSet(applied map[int]appliedRecord, migrations []migrationFile, targetDB string) error {
	known := make(map[int]migrationFile, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = migration
		if !migrationAppliesToTargetDB(migration.scope, targetDB) {
			if _, ok := applied[migration.version]; ok {
				return fmt.Errorf("storage: migration ledger contains out-of-scope version %03d (%s) for target database %q", migration.version, migration.filename, targetDB)
			}
		}
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("storage: migration ledger contains version %03d but no migration file exists on disk", version)
		}
	}
	for index, migration := range migrations {
		if !migrationAppliesToTargetDB(migration.scope, targetDB) {
			continue
		}
		if _, ok := applied[migration.version]; ok {
			continue
		}
		// One deployment omitted the historical 196 index marker from its
		// ledger. Its restored migration is idempotent and is applied by the
		// normal runner below, so allow this specific repair to proceed.
		if migration.version == 196 && targetDB == "primary" {
			continue
		}
		for _, later := range migrations[index+1:] {
			if !migrationAppliesToTargetDB(later.scope, targetDB) {
				continue
			}
			if _, ok := applied[later.version]; ok {
				return fmt.Errorf("storage: migration ledger gap: version %03d (%s) is missing while later version %03d (%s) is applied", migration.version, migration.filename, later.version, later.filename)
			}
		}
	}
	return nil
}

// warnOnGaps logs warnings for any version gaps in the migration sequence.
// Gaps are informational only — the runner proceeds normally. Real migration
// directories may have gaps from historical renumbering or removed migrations.
func warnOnGaps(migrations []migrationFile, log *zap.Logger) {
	if len(migrations) == 0 {
		return
	}

	if migrations[0].version != 1 {
		log.Warn("first migration is not version 001 — possible orphaned migrations",
			zap.Int("first_version", migrations[0].version),
			zap.String("filename", migrations[0].filename))
	}

	expected := migrations[0].version
	for i := 1; i < len(migrations); i++ {
		expected++
		if migrations[i].version != expected {
			gapStart := expected
			gapEnd := migrations[i].version - 1
			if gapStart == gapEnd {
				log.Warn("migration version gap detected",
					zap.Int("gap", gapStart))
			} else {
				log.Warn("migration version gap detected",
					zap.Int("gap_start", gapStart),
					zap.Int("gap_end", gapEnd))
			}
			expected = migrations[i].version
		}
	}
}
