// Package storage — migrations_099_test.go holds the scenario tests
// for migration 099 (qdrant asset columns). Migration 099 added 9
// columns to media_assets so the qdrant-side coordinate checks can
// be re-derived from a fresh, canonical schema rather than only
// from a legacy DB that already shipped the columns.
//
// Covers:
//   - TestMigrations_099_QdrantAssetColumnsPresent
//     PRAGMA table_info(media_assets) lists the 9 columns added by
//     migration 099 (youtube_video_id, youtube_url, start_time,
//     end_time, workspace_id, channel_id, license,
//     source_version, style). Mirrors the migration chain schema
//     in canonical.go — drift here means a future in-memory DB
//     created from canonical.go would diverge from a legacy DB
//     that has applied 099.
//   - TestMigrations_099_QdrantAssetColumnsRoundTrip
//     Raw-SQL round-trip on a fresh DB: insert a media_assets row
//     with all 9 new columns populated, select it back, assert
//     each column survives. The schema-layer test covers the
//     user's "FetchAsset works on fixture in-memory" requirement;
//     the qdrant-package TestSQLiteAssetStore_FetchAssetAfterMigrations
//     covers the typed FetchAsset path.
package storage

import (
	"database/sql"
	"testing"
)

func TestMigrations_099_QdrantAssetColumnsPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	required := []string{
		"youtube_video_id",
		"youtube_url",
		"start_time",
		"end_time",
		"workspace_id",
		"channel_id",
		"license",
		"source_version",
		"style",
	}
	rows, err := db.Query(`PRAGMA table_info(media_assets)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(media_assets): %v", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{}, 64)
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		seen[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}
	for _, col := range required {
		if _, ok := seen[col]; !ok {
			t.Errorf("media_assets missing column %q (added by migration 099_qdrant_asset_columns.sql; canonical.go must mirror it)", col)
		}
	}
}

func TestMigrations_099_QdrantAssetColumnsRoundTrip(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO media_assets (
			id, source, name, tags, media_type, lifecycle_state,
			youtube_video_id, youtube_url, start_time, end_time,
			workspace_id, channel_id, license, source_version, style
		) VALUES (?, 'artlist', 'round-trip smoke', '[]', 'video', 'ACTIVE',
		          'yt-123', 'https://www.youtube.com/watch?v=yt-123',
		          '10.0', '20.0',
		          'ws-1', 'chan-9', 'standard', 'src-v1', 'cinematic')
	`, "rt-asset-1")
	if err != nil {
		t.Fatalf("insert round-trip asset: %v", err)
	}
	var youtubeVideoID, youtubeURL, startTime, endTime string
	var workspaceID, channelID, lic, sourceVersion, styleStr string
	err = db.QueryRow(`
		SELECT youtube_video_id, youtube_url, start_time, end_time,
		       workspace_id, channel_id, license, source_version, style
		FROM media_assets WHERE id = ?
	`, "rt-asset-1").Scan(&youtubeVideoID, &youtubeURL, &startTime, &endTime,
		&workspaceID, &channelID, &lic, &sourceVersion, &styleStr)
	if err != nil {
		t.Fatalf("select round-trip asset: %v", err)
	}
	expectations := map[string]string{
		"youtube_video_id": youtubeVideoID,
		"youtube_url":      youtubeURL,
		"start_time":       startTime,
		"end_time":         endTime,
		"workspace_id":     workspaceID,
		"channel_id":       channelID,
		"license":          lic,
		"source_version":   sourceVersion,
		"style":            styleStr,
	}
	want := map[string]string{
		"youtube_video_id": "yt-123",
		"youtube_url":      "https://www.youtube.com/watch?v=yt-123",
		"start_time":       "10.0",
		"end_time":         "20.0",
		"workspace_id":     "ws-1",
		"channel_id":       "chan-9",
		"license":          "standard",
		"source_version":   "src-v1",
		"style":            "cinematic",
	}
	for col, got := range expectations {
		if got != want[col] {
			t.Errorf("round-trip %s = %q, want %q", col, got, want[col])
		}
	}
}
