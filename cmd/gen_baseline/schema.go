// schema.go — the pure schema helpers behind cmd/gen_baseline.
//
// The command itself stays a thin composition: locate the corpus, run the
// migrations, merge the two databases and write the dump. Everything that
// reads, rewrites or classifies a SQL statement lives here so main.go remains
// under the thin-command ceiling.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func mustRunMigrations(dbPath, targetDir, targetDB string) {
	if err := sqlite.RunMigrationsOnDB(dbPath, zap.NewNop(), targetDir, targetDB); err != nil {
		panic(fmt.Sprintf("migrations %s: %v", targetDB, err))
	}
}

func rewriteIdempotent(s string) string {
	s = strings.TrimSpace(s)
	u := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(u, "CREATE TABLE ") && !strings.Contains(u, "IF NOT EXISTS"):
		return "CREATE TABLE IF NOT EXISTS " + strings.TrimSpace(s[len("CREATE TABLE "):])
	case strings.HasPrefix(u, "CREATE UNIQUE INDEX ") && !strings.Contains(u, "IF NOT EXISTS"):
		return "CREATE UNIQUE INDEX IF NOT EXISTS " + strings.TrimSpace(s[len("CREATE UNIQUE INDEX "):])
	case strings.HasPrefix(u, "CREATE INDEX ") && !strings.Contains(u, "IF NOT EXISTS"):
		return "CREATE INDEX IF NOT EXISTS " + strings.TrimSpace(s[len("CREATE INDEX "):])
	}
	return s
}

func schemaFromDB(dbPath string) []string {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, sql FROM sqlite_master WHERE type IN ('table','index','trigger') AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations' AND sql NOT NULL AND sql NOT LIKE '%schema_migrations%' ORDER BY rowid`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	var stmts []string
	for rows.Next() {
		var typ string
		var v sql.NullString
		if err := rows.Scan(&typ, &v); err != nil {
			panic(err)
		}
		if !v.Valid || strings.TrimSpace(v.String) == "" {
			continue
		}
		s := strings.TrimSpace(v.String)
		if !strings.HasSuffix(s, ";") {
			s += ";"
		}
		if strings.Contains(s, "schema_migrations") {
			continue
		}
		stmts = append(stmts, s)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	return stmts
}

func objectKey(sqlStr string) string {
	s := strings.TrimSpace(sqlStr)
	u := strings.ToUpper(s)
	parts := strings.Fields(u)
	if len(parts) < 3 {
		return s
	}
	idx := 2
	if len(parts) > 1 && parts[1] == "UNIQUE" {
		idx++
	}
	if idx < len(parts) && parts[idx] == "IF" {
		idx += 3
	}
	if idx < len(parts) {
		return parts[idx]
	}
	return s
}

// corpusWithoutPlaneBaselines copies the migration corpus into a temporary
// directory, leaving out the plane dumps this command generates. The frozen
// shared dump (`000_baseline_268.sql`) stays in the copy on purpose: a plane
// dump is the state a fresh plane database reaches TODAY, and that path runs
// the shared dump first under its version-0 sentinel. The historical path alone
// is not self-consistent per plane — 267 quarantines
// `asset_subtitle_artifacts` on observability while 302 (scoped
// primary,observability,cache) rebuilds it unconditionally, a gap the shared
// dump masks and which a plane dump must preserve the final effect of (307
// drops the table anyway).
func corpusWithoutPlaneBaselines(targetDir string, planeFiles map[string]bool) string {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		panic(err)
	}
	tmpDir, err := os.MkdirTemp("", "baseline-corpus-*")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if planeFiles[e.Name()] {
			continue
		}
		src := filepath.Join(targetDir, e.Name())
		dst := filepath.Join(tmpDir, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			panic(err)
		}
		if err := atomicwrite.WriteFile(dst, data, 0o644); err != nil {
			panic(err)
		}
	}
	return tmpDir
}

// maxMigrationVersion returns the highest numbered migration in the corpus
// (ignoring baselines). A plane dump is taken after the whole corpus ran, so
// every in-scope migration up to that version is already inside it — and an
// out-of-scope one would have been skipped anyway.
func maxMigrationVersion(targetDir string) int {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		panic(err)
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || strings.HasPrefix(e.Name(), "000_baseline") {
			continue
		}
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			continue
		}
		v, err := strconv.Atoi(e.Name()[:idx])
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	return max
}
