package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// TEMPORARY census helper — dumps live columns named file_hash plus the
// media_assets.asset_location mirror per migration scope.
func TestTmpSchemaDump(t *testing.T) {
	log := zaptest.NewLogger(t)
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	for _, scope := range []string{"primary", "cache", "all"} {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "dump.sqlite")
		if err := RunMigrationsOnDB(dbPath, log, targetDir, scope); err != nil {
			t.Fatalf("scope %s: migrate: %v", scope, err)
		}
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			t.Fatalf("tables: %v", err)
		}
		var tables []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			tables = append(tables, n)
		}
		_ = rows.Close()
		t.Logf("=== scope %s ===", scope)
		for _, tbl := range tables {
			cols := func() []string {
				cr, err := db.Query("PRAGMA table_info(" + tbl + ")")
				if err != nil {
					t.Fatalf("pragma %s: %v", tbl, err)
				}
				defer func() { _ = cr.Close() }()
				var out []string
				for cr.Next() {
					var cid int
					var name, typ string
					var notnull, pk int
					var dflt sql.NullString
					_ = cr.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
					out = append(out, name)
				}
				return out
			}()
			hasFH := false
			hasLoc := false
			for _, c := range cols {
				if c == "file_hash" {
					hasFH = true
				}
				if c == "asset_location" {
					hasLoc = true
				}
			}
			if hasFH || (hasLoc && tbl == "media_assets") {
				t.Logf("  %s: file_hash=%v asset_location=%v", tbl, hasFH, hasLoc)
			}
		}
		_ = db.Close()
	}
}
