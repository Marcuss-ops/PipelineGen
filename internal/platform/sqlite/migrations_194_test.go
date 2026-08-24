package sqlite

import "testing"

// TestMigrations_194_ContentObjectsTable verifies that migration
// 194_content_objects.sql creates the CAS content registry table with the
// canonical column set on a fresh primary-DB apply.
func TestMigrations_194_ContentObjectsTable(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	cols := scanColumnNames(t, db, "content_objects")
	for _, want := range []string{
		"sha256",
		"size_bytes",
		"mime_type",
		"storage_uri",
		"created_at",
		"verified_at",
		"integrity_status",
	} {
		if _, ok := cols[want]; !ok {
			t.Fatalf("content_objects missing column %q after migration 194 (present: %v)", want, cols)
		}
	}

	// sha256 must be the primary key.
	var pkColumns []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('content_objects') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatalf("read content_objects pk: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		pkColumns = append(pkColumns, name)
	}
	if len(pkColumns) != 1 || pkColumns[0] != "sha256" {
		t.Fatalf("content_objects primary key = %v, want [sha256]", pkColumns)
	}

	// Insert + dedup: same sha256 collapses to one row (idempotent registry).
	if _, err := db.Exec(`
		INSERT INTO content_objects (sha256, size_bytes, storage_uri, created_at, integrity_status)
		VALUES ('abc', 100, 'cas://ab/abc', '2026-08-12T00:00:00Z', 'UNVERIFIED')
		ON CONFLICT(sha256) DO UPDATE SET size_bytes = excluded.size_bytes`); err != nil {
		t.Fatalf("insert content_object: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO content_objects (sha256, size_bytes, storage_uri, created_at, integrity_status)
		VALUES ('abc', 200, 'cas://ab/abc', '2026-08-12T00:00:00Z', 'UNVERIFIED')
		ON CONFLICT(sha256) DO UPDATE SET size_bytes = excluded.size_bytes`); err != nil {
		t.Fatalf("re-insert content_object: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM content_objects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("content_objects rows after same-digest upsert = %d, want 1 (CAS dedup)", n)
	}
}
