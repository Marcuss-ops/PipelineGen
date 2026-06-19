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
		migrations = append(migrations, migrationFile{
			version:  version,
			filename: e.Name(),
			path:     filepath.Join(targetDir, e.Name()),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
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
