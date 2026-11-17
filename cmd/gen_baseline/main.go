// Command gen_baseline regenerates the consolidated SQLite baseline.
//
// The baseline is the single idempotent dump a fresh database applies instead
// of replaying the historical migrations. Its filename keeps the freeze point
// promised by BASELINE_PLAN.md (268), while the `-- baseline-covers: 298`
// directive records the real schema window: the dump is taken after 297/298 so
// the retired vector-index storage names never exist on a fresh install.
//
// Run from the refactored module root:
//
//	go run ./cmd/gen_baseline
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
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

func main() {
	targetDir, _ := filepath.Abs("migrations/sqlite")
	if _, err := os.Stat(targetDir); err != nil {
		for _, c := range []string{"migrations/sqlite", "refactored/migrations/sqlite"} {
			if _, err := os.Stat(c); err == nil {
				abs, _ := filepath.Abs(c)
				targetDir = abs
				break
			}
		}
	}
	fmt.Println("targetDir", targetDir)

	// The generator must start from the incremental corpus: with a baseline
	// already present, the runner would skip 001..298 and dump that file back
	// to itself, so a mistake in the baseline could never be repaired.
	removeExistingBaselines(targetDir)

	tmpDir, _ := os.MkdirTemp("", "baseline-gen-*")
	defer os.RemoveAll(tmpDir)
	primaryPath := filepath.Join(tmpDir, "primary.db")
	obsPath := filepath.Join(tmpDir, "obs.db")
	mustRunMigrations(primaryPath, targetDir, "primary")
	mustRunMigrations(obsPath, targetDir, "observability")
	primaryStmts := schemaFromDB(primaryPath)
	obsStmts := schemaFromDB(obsPath)
	seen := map[string]bool{}
	var merged []string
	for _, s := range primaryStmts {
		k := objectKey(s)
		if !seen[k] {
			seen[k] = true
			merged = append(merged, s)
		}
	}
	for _, s := range obsStmts {
		k := objectKey(s)
		if !seen[k] {
			seen[k] = true
			merged = append(merged, s)
		}
	}
	seeds := []string{
		"INSERT INTO control_plane_meta (singleton_id, database_id, schema_family, instance_role, canonical_version, created_at) SELECT 1, 'cp_baseline', 'pipelinegen-control-plane', 'CANONICAL', 1, datetime('now') WHERE NOT EXISTS (SELECT 1 FROM control_plane_meta);",
		"INSERT OR IGNORE INTO _media_authority_retirement (id, note) VALUES (1, 'SQLite media authority retired — PostgreSQL media SSOT (P2-9, September 2026)');",
	}
	for _, s := range seeds {
		k := "__seed__" + s
		if !seen[k] {
			seen[k] = true
			merged = append(merged, s)
		}
	}
	var sb strings.Builder
	sb.WriteString("-- 000_baseline_268.sql — consolidated SQLite baseline (post-298 schema)\n")
	sb.WriteString("-- database: all\n")
	fmt.Fprintf(&sb, "-- baseline-covers: %d\n", baselineCovers)
	sb.WriteString("--\n")
	sb.WriteString("-- Generated from a clean DB bootstrapped via RunMigrationsOnDB\n")
	sb.WriteString("-- primary + observability (BASELINE_PLAN.md §1–3). The filename keeps\n")
	sb.WriteString("-- the freeze point promised by the plan; the `-- baseline-covers`\n")
	sb.WriteString("-- directive records the real window: this dump is taken after 297/298,\n")
	sb.WriteString("-- so the retired vector-index tables/columns never exist on a fresh\n")
	sb.WriteString("-- install and the unconditional RENAME/DROP statements do not replay.\n")
	sb.WriteString("-- This file is idempotent (IF NOT EXISTS / IF NOT EXISTS) and replaces\n")
	sb.WriteString("-- the incremental museum 001..298 for fresh installs. Old DBs that\n")
	sb.WriteString("-- already carry individual ledger rows skip this file (see\n")
	sb.WriteString("-- migrations_discovery.go::isHistoricalWindowCovered).\n")
	sb.WriteString("--\n")
	sb.WriteString("-- DO NOT EDIT MANUALLY — regenerate via `go run ./cmd/gen_baseline`\n")
	sb.WriteString("-- and verify with `go test ./internal/platform/sqlite -run TestMigrations_Smoke_Baseline`.\n\n")
	for _, s := range merged {
		sb.WriteString(rewriteIdempotent(s) + "\n")
	}
	if err := atomicwrite.WriteFile(filepath.Join(targetDir, baselineFilename), []byte(sb.String()), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("Wrote %d merged statements (primary %d, obs %d)\n", len(merged), len(primaryStmts), len(obsStmts))
	log := zap.NewNop()
	if err := sqlite.RunMigrationsOnDB(filepath.Join(tmpDir, "verify.db"), log, targetDir, "primary"); err != nil {
		fmt.Printf("verify primary failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("verify primary OK")
	if err := sqlite.RunMigrationsOnDB(filepath.Join(tmpDir, "verify_obs.db"), log, targetDir, "observability"); err != nil {
		fmt.Printf("verify obs failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("verify observability OK")
}
