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
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

const (
	baselineFilename = "000_baseline_268.sql"
	baselineCovers   = 298
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

func removeExistingBaselines(targetDir string) {
	matches, err := filepath.Glob(filepath.Join(targetDir, "000_baseline_*.sql"))
	if err != nil {
		panic(err)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			panic(err)
		}
	}
}
