package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestZZDumpMirrorFields(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "c.sqlite")
	if err := RunMigrationsOnDB(dbPath, zap.NewNop(), "../../../migrations/sqlite", "cache"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table' AND p.name IN ('file_hash','thumb_url','legacy_file_md5')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl, col string
		rows.Scan(&tbl, &col)
		t.Logf("CACHECOL %s.%s", tbl, col)
	}
	rows.Close()
}
