package sqlite

import "testing"

// TestMigrations_197_AssetContentLink verifies that migration
// 197_asset_content_link.sql links logical assets to physical CAS content:
// the content_sha256 column on media_assets and the asset_sources
// provenance table, both on a fresh primary-DB apply.
func TestMigrations_197_AssetContentLink(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	// 1. media_assets gained the content_sha256 column.
	assetCols := scanColumnNames(t, db, "media_assets")
	if _, ok := assetCols["content_sha256"]; !ok {
		t.Fatalf("media_assets missing column content_sha256 after migration 197 (present: %v)", assetCols)
	}

	// 2. media_asset_sources exists with the canonical provenance column set.
	sourceCols := scanColumnNames(t, db, "media_asset_sources")
	for _, want := range []string{
		"source_id",
		"asset_id",
		"content_sha256",
		"source_type",
		"source_uri",
		"source_version",
		"discovered_at",
		"is_primary",
	} {
		if _, ok := sourceCols[want]; !ok {
			t.Fatalf("media_asset_sources missing column %q after migration 197 (present: %v)", want, sourceCols)
		}
	}

	// source_id must be the primary key (deterministic provenance key).
	var pkColumns []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('media_asset_sources') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatalf("read media_asset_sources pk: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		pkColumns = append(pkColumns, name)
	}
	if len(pkColumns) != 1 || pkColumns[0] != "source_id" {
		t.Fatalf("media_asset_sources primary key = %v, want [source_id]", pkColumns)
	}

	// 3. End-to-end round-trip: link an asset and register two provenance
	//    records for the same content (multi-source provenance invariant).
	assetID := "asset-cas-001"
	// The 189 state triggers require explicit valid lifecycle_state + index_state.
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, content_sha256, lifecycle_state, index_state) VALUES (?, '', 'ACTIVE', 'NOT_INDEXABLE')`,
		assetID); err != nil {
		t.Fatalf("insert media_asset: %v", err)
	}
	if _, err := db.Exec(`UPDATE media_assets SET content_sha256 = 'a1f8c72e' WHERE id = ?`, assetID); err != nil {
		t.Fatalf("link content: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO media_asset_sources
		(source_id, asset_id, content_sha256, source_type, source_uri, source_version, discovered_at, is_primary)
		VALUES
		('src-drive-1', ?, 'a1f8c72e', 'drive', 'drive://file/1', 'etag-v1', '2026-08-12T00:00:00Z', 1),
		('src-yt-1',    ?, 'a1f8c72e', 'youtube', 'https://youtu.be/x', '', '2026-08-12T00:01:00Z', 0)`,
		assetID, assetID); err != nil {
		t.Fatalf("insert provenance rows: %v", err)
	}

	var linked string
	if err := db.QueryRow(`SELECT content_sha256 FROM media_assets WHERE id = ?`, assetID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != "a1f8c72e" {
		t.Fatalf("media_assets.content_sha256 = %q, want a1f8c72e", linked)
	}
	var srcCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_asset_sources WHERE asset_id = ? AND content_sha256 = 'a1f8c72e'`, assetID).Scan(&srcCount); err != nil {
		t.Fatal(err)
	}
	if srcCount != 2 {
		t.Fatalf("media_asset_sources rows for one content object = %d, want 2 (two provenance records, one content)", srcCount)
	}
}
