package sqlite

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
// (e.g. "primary", "observability", "cache", "jobs", "primary,observability", or "all")
// and falls back to "all" when no directive is present OR when the
// directive references an unknown scope.
//
// See migrationFile.
//
// Format:
//
//	-- database: <scope>[, <scope>...]
//
// Valid scope values: "primary", "observability", "cache", "jobs", "all".
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
			case "primary", "observability", "cache", "jobs", "all":
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
// processing ("primary", "observability", "cache", "jobs", or "all").
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

// isBaselineMigration reports whether the migration file is the
// consolidated baseline sentinel (version 000, filename 000_baseline_*.sql).
// The baseline sorts before every real migration and covers the frozen
// window 1..N described in docs/migrations/BASELINE_PLAN.md. It is
// defined here so both discovery and apply can reference it without a
// cross-file import cycle.
func isBaselineMigrationFile(m migrationFile) bool {
	return m.version == 0 && strings.HasPrefix(m.filename, "000_baseline")
}

// baselineThreshold extracts the frozen version N from a baseline
// filename "000_baseline_<N>.sql" (or "000_baseline_<N>_*.sql").
// Returns 0 when the filename does not carry a threshold or parsing fails.
func baselineThresholdFromFile(m migrationFile) int {
	if !isBaselineMigrationFile(m) {
		return 0
	}
	rest := strings.TrimPrefix(m.filename, "000_baseline_")
	rest = strings.TrimSuffix(rest, ".sql")
	if idx := strings.Index(rest, "_"); idx >= 0 {
		rest = rest[:idx]
	}
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			return 0
		}
	}
	if rest == "" {
		return 0
	}
	var threshold int
	if _, err := fmt.Sscanf(rest, "%d", &threshold); err != nil {
		return 0
	}
	if threshold <= 0 {
		return 0
	}
	return threshold
}

// baselineApplied reports whether the baseline sentinel (version 0) is
// already recorded in the ledger. When true, the frozen window 1..N is
// considered satisfied for fresh databases that bootstrapped from the
// single baseline dump.
func baselineApplied(applied map[int]appliedRecord) bool {
	_, ok := applied[0]
	return ok
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

// baselineThresholdForMigrations returns the frozen window N from the
// consolidated baseline file (000_baseline_<N>.sql) if present, else 0.
func baselineThresholdForMigrations(migrations []migrationFile) int {
	for _, m := range migrations {
		if isBaselineMigrationFile(m) {
			if v := baselineThresholdFromFile(m); v > 0 {
				return v
			}
		}
	}
	return 0
}

// hasAnyHistory reports whether the ledger already contains any
// non-baseline migration row. When true the database is already on
// the incremental path and the consolidated baseline must be skipped
// (old or partially-migrated DB — see BASELINE_PLAN.md §2.2).
func hasAnyHistory(applied map[int]appliedRecord) bool {
	for v := range applied {
		if v > 0 {
			return true
		}
	}
	return false
}

// isHistoricalWindowCovered reports whether every in-scope migration
// version 1..threshold is already recorded in the ledger. When true the
// baseline's historical window is already satisfied by individual rows
// (old database case) and the baseline file itself can be skipped.
func isHistoricalWindowCovered(applied map[int]appliedRecord, migrations []migrationFile, targetDB string, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	// Build a set of required in-scope versions up to threshold.
	required := make(map[int]bool)
	for _, m := range migrations {
		if m.version > 0 && m.version <= threshold && migrationAppliesToTargetDB(m.scope, targetDB) {
			required[m.version] = true
		}
	}
	for v := range required {
		if _, ok := applied[v]; !ok {
			return false
		}
	}
	return true
}

// validateAppliedMigrationSet fails closed when a later migration is recorded
// while an earlier in-scope migration is missing. MAX(version) alone cannot
// detect a ledger such as 193,195; this checks the exact applied/file set.
// The consolidated baseline (version 0) satisfies the window 1..N when its
// sentinel row is present, so fresh baseline-bootstrapped databases are not
// treated as gapped.
func validateAppliedMigrationSet(db queryable, applied map[int]appliedRecord, migrations []migrationFile, targetDB string) error {
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
			// Migration 253 was a historical cleanup migration that was
			// deployed to the operational DB but was not retained in this
			// checkout. Accept only its exact ledger identity and only when
			// its intended schema effect is already true (the table is gone).
			// This is a read-only compatibility gate: it never edits the
			// ledger and never permits arbitrary missing migrations.
			if version == 253 {
				record := applied[version]
				if record.filename == "253_drop_assembly_sessions.sql" &&
					record.checksum == "a1c9a3d698d1281b425a3aeaa22b4869f0053b22ad9158a6051d63a77fca960a" &&
					!migrationTableExists(db, "assembly_sessions") {
					continue
				}
			}
			return fmt.Errorf("storage: migration ledger contains version %03d but no migration file exists on disk", version)
		}
	}
	baselineThreshold := baselineThresholdForMigrations(migrations)
	baselineIsApplied := baselineApplied(applied)
	for index, migration := range migrations {
		if !migrationAppliesToTargetDB(migration.scope, targetDB) {
			continue
		}
		// Baseline sentinel is always "in-scope" for its threshold's DBs
		// (file scope defaults to "all" unless explicitly scoped).
		if isBaselineMigrationFile(migration) {
			continue
		}
		if _, ok := applied[migration.version]; ok {
			continue
		}
		// Fresh baseline-bootstrapped DB: 1..N satisfied by sentinel row.
		if baselineIsApplied && baselineThreshold > 0 && migration.version <= baselineThreshold {
			continue
		}
		// The execution-plane cutover (265) quarantines the legacy jobs
		// tables from the primary database. Some databases were cut over
		// before the historical 262 marker was recorded. In that already
		// quarantined state, replaying 262 would fail because its INSERT
		// reads from the deliberately removed jobs table. Accept only this
		// exact, observable state; arbitrary migration gaps remain fatal.
		if skipMigrationAfterExecutionCutover(db, applied, migration, targetDB) {
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
			if isBaselineMigrationFile(later) {
				continue
			}
			if _, ok := applied[later.version]; ok {
				return fmt.Errorf("storage: migration ledger gap: version %03d (%s) is missing while later version %03d (%s) is applied", migration.version, migration.filename, later.version, later.filename)
			}
		}
	}
	return nil
}

func skipMigrationAfterExecutionCutover(db queryable, applied map[int]appliedRecord, migration migrationFile, targetDB string) bool {
	if migration.version != 262 || targetDB != "primary" {
		return false
	}
	cutover, ok := applied[265]
	return ok && cutover.filename == "265_execution_plane_quarantine.sql" &&
		migrationTableExists(db, "legacy_jobs") && !migrationTableExists(db, "jobs")
}

// migrationTableExists is intentionally read-only and narrowly scoped to
// historical migration compatibility checks.
func migrationTableExists(db queryable, name string) bool {
	if db == nil {
		return false
	}
	rows, err := db.Query("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name)
	if err != nil {
		return false
	}
	defer rows.Close()
	if !rows.Next() {
		return false
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// warnOnGaps logs warnings for any version gaps in the migration sequence.
// Gaps are informational only — the runner proceeds normally. Real migration
// directories may have gaps from historical renumbering or removed migrations.
// The consolidated baseline (version 0) is excluded from gap detection.
func warnOnGaps(migrations []migrationFile, log *zap.Logger) {
	if len(migrations) == 0 {
		return
	}
	// Exclude the baseline sentinel (version 0) from gap accounting — it
	// represents the consolidated historical window 1..N.
	filtered := migrations
	if len(migrations) > 0 && migrations[0].version == 0 && isBaselineMigrationFile(migrations[0]) {
		filtered = migrations[1:]
	}
	if len(filtered) == 0 {
		return
	}

	if filtered[0].version != 1 {
		log.Warn("first migration is not version 001 — possible orphaned migrations",
			zap.Int("first_version", filtered[0].version),
			zap.String("filename", filtered[0].filename))
	}

	expected := filtered[0].version
	for i := 1; i < len(filtered); i++ {
		expected++
		if filtered[i].version != expected {
			gapStart := expected
			gapEnd := filtered[i].version - 1
			if gapStart == gapEnd {
				log.Warn("migration version gap detected",
					zap.Int("gap", gapStart))
			} else {
				log.Warn("migration version gap detected",
					zap.Int("gap_start", gapStart),
					zap.Int("gap_end", gapEnd))
			}
			expected = filtered[i].version
		}
	}
}
