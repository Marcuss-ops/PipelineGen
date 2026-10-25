package sqlite

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrations_282_StockSourceCacheBucketDemolition pins the cache-plane
// half of the legacy_file_md5 contract (the cache database is out of 281's
// primary scope): non-reconcilable values are cleared, the surviving values
// keep their exact bytes under the honest name `content_sha256`.
func TestMigrations_282_StockSourceCacheBucketDemolition(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	legacyDir := copyMigrationSubset(t, targetDir, 278, nil)
	fullDir := copyMigrationSubset(t, targetDir, 282, nil)
	dbPath := filepath.Join(t.TempDir(), "contract-282-cache.sqlite")

	if err := RunMigrationsOnDB(dbPath, nil, legacyDir, "cache"); err != nil {
		t.Fatalf("apply cache migrations up to 278: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	valid := strings.Repeat("AB", 32) // 64 hex chars, mixed case (rename keeps them verbatim)
	md5 := strings.Repeat("b", 32)    // 32 chars — MD5 length, must be cleared

	if _, err := db.Exec(`
		INSERT INTO stock_source_cache (cache_key, source_url, local_path, legacy_file_md5) VALUES
			('k-sha', 'https://src/sha', '/tmp/sha', ?),
			('k-md5', 'https://src/md5', '/tmp/md5', ?),
			('k-empty', 'https://src/empty', '/tmp/empty', '')
	`, valid, md5); err != nil {
		_ = db.Close()
		t.Fatalf("seed stock_source_cache: %v", err)
	}
	_ = db.Close()

	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "cache"); err != nil {
		t.Fatalf("apply migration 282: %v", err)
	}
	db = openMigrationTestDB(t, dbPath)
	defer func() { _ = db.Close() }()

	for _, check := range []struct{ key, want string }{
		{"k-sha", valid}, // valid digest survives verbatim
		{"k-md5", ""},    // non-reconcilable value cleared
		{"k-empty", ""},
	} {
		var got string
		if err := db.QueryRow(
			`SELECT content_sha256 FROM stock_source_cache WHERE cache_key = ?`, check.key,
		).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", check.key, err)
		}
		if got != check.want {
			t.Errorf("%s content_sha256 = %q, want %q", check.key, got, check.want)
		}
	}

	cols := scanColumnNames(t, db, "stock_source_cache")
	if _, ok := cols["legacy_file_md5"]; ok {
		t.Error("stock_source_cache still carries the demolished legacy_file_md5 column")
	}
	if _, ok := cols["content_sha256"]; !ok {
		t.Error("stock_source_cache missing content_sha256 after the 282 rename")
	}
}
