// cmd/admin/backfill_provider_timestamps_test.go — contract tests for the
// provider/timestamp key convergence backfill.
//
// Pins four invariants of backfillProviderTimestamps:
//   - canonical keys (source_provider / source_video_id / start_sec /
//     end_sec) are stamped from their columns when absent;
//   - an existing canonical key is never overwritten (additive);
//   - start_ms/end_ms are mirrored as float seconds (ms / 1000.0);
//   - the operation is idempotent: a second run matches zero rows.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newProviderTimestampsTestDB builds an in-memory media_assets table
// with the columns the provider/timestamp backfill touches.
func newProviderTimestampsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		media_type TEXT NOT NULL DEFAULT '',
		source_provider TEXT NOT NULL DEFAULT '',
		source_video_id TEXT NOT NULL DEFAULT '',
		start_ms INTEGER NOT NULL DEFAULT 0,
		end_ms INTEGER NOT NULL DEFAULT 0,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		updated_at TEXT NOT NULL DEFAULT ''
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',)`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	return db
}

func insertProviderRow(t *testing.T, db *sql.DB, id, mediaType, provider, videoID string, startMS, endMS int64, metadataJSON string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, media_type, source_provider, source_video_id, start_ms, end_ms, metadata_json, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, mediaType, provider, videoID, startMS, endMS, metadataJSON, "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func providerMetadataKey(t *testing.T, db *sql.DB, id, key string) any {
	t.Helper()
	var metaJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = ?`, id).Scan(&metaJSON); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("decode %s metadata: %v", id, err)
	}
	return meta[key]
}

func TestBackfillProviderTimestamps_StampsCanonicalKeys(t *testing.T) {
	db := newProviderTimestampsTestDB(t)
	// Legacy YouTube clip: columns populated, no canonical keys
	// (only the legacy video_id key present).
	insertProviderRow(t, db, "yt-1", "video", "youtube", "abc123XYZ", 12500, 35000, `{"video_id":"abc123XYZ"}`)
	// Row with only provider populated.
	insertProviderRow(t, db, "stock-1", "clip", "stock", "", 0, 0, `{}`)

	matched, updated, err := backfillProviderTimestamps(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if matched != 2 || updated != 2 {
		t.Fatalf("matched=%d updated=%d, want 2/2", matched, updated)
	}

	if got := providerMetadataKey(t, db, "yt-1", "source_provider"); got != "youtube" {
		t.Fatalf("yt-1 source_provider=%v, want youtube", got)
	}
	if got := providerMetadataKey(t, db, "yt-1", "source_video_id"); got != "abc123XYZ" {
		t.Fatalf("yt-1 source_video_id=%v, want abc123XYZ", got)
	}
	// ms columns mirrored as float seconds.
	if got, ok := providerMetadataKey(t, db, "yt-1", "start_sec").(float64); !ok || got != 12.5 {
		t.Fatalf("yt-1 start_sec=%v, want 12.5", got)
	}
	if got, ok := providerMetadataKey(t, db, "yt-1", "end_sec").(float64); !ok || got != 35.0 {
		t.Fatalf("yt-1 end_sec=%v, want 35.0", got)
	}
	// Legacy alias untouched.
	if got := providerMetadataKey(t, db, "yt-1", "video_id"); got != "abc123XYZ" {
		t.Fatalf("yt-1 video_id legacy alias lost: %v", got)
	}

	if got := providerMetadataKey(t, db, "stock-1", "source_provider"); got != "stock" {
		t.Fatalf("stock-1 source_provider=%v, want stock", got)
	}
}

func TestBackfillProviderTimestamps_DoesNotOverwriteExistingKeys(t *testing.T) {
	db := newProviderTimestampsTestDB(t)
	insertProviderRow(t, db, "yt-1", "video", "youtube", "abc123XYZ", 12500, 35000,
		`{"source_provider":"artlist","source_video_id":"keep-me","start_sec":9.0,"end_sec":20.0}`)

	matched, updated, err := backfillProviderTimestamps(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if matched != 0 || updated != 0 {
		t.Fatalf("matched=%d updated=%d, want 0/0 (all keys present)", matched, updated)
	}
	if got := providerMetadataKey(t, db, "yt-1", "source_provider"); got != "artlist" {
		t.Fatalf("existing source_provider overwritten: %v", got)
	}
	if got := providerMetadataKey(t, db, "yt-1", "source_video_id"); got != "keep-me" {
		t.Fatalf("existing source_video_id overwritten: %v", got)
	}
	if got, ok := providerMetadataKey(t, db, "yt-1", "start_sec").(float64); !ok || got != 9.0 {
		t.Fatalf("existing start_sec overwritten: %v", got)
	}
}

func TestBackfillProviderTimestamps_IsIdempotent(t *testing.T) {
	db := newProviderTimestampsTestDB(t)
	insertProviderRow(t, db, "yt-1", "video", "youtube", "abc123XYZ", 12500, 35000, `{}`)

	if _, updated, err := backfillProviderTimestamps(context.Background(), db, 0); err != nil || updated != 1 {
		t.Fatalf("first run: updated=%d err=%v, want 1/nil", updated, err)
	}

	matched, updated, err := backfillProviderTimestamps(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if matched != 0 || updated != 0 {
		t.Fatalf("second run: matched=%d updated=%d, want 0/0", matched, updated)
	}
}

func TestBackfillProviderTimestamps_NoNullPollution(t *testing.T) {
	db := newProviderTimestampsTestDB(t)
	// Row matching only the source_provider arm: the other three
	// canonical keys must stay ABSENT (never written as JSON null).
	insertProviderRow(t, db, "stock-1", "clip", "stock", "", 0, 0, `{}`)

	if _, updated, err := backfillProviderTimestamps(context.Background(), db, 0); err != nil || updated != 1 {
		t.Fatalf("run: updated=%d err=%v, want 1/nil", updated, err)
	}

	var metaJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = ?`, "stock-1").Scan(&metaJSON); err != nil {
		t.Fatalf("read: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"source_video_id", "start_sec", "end_sec"} {
		if v, present := meta[key]; present {
			t.Fatalf("key %q must stay absent, got %v in %s", key, v, metaJSON)
		}
	}
	if got := providerMetadataKey(t, db, "stock-1", "source_provider"); got != "stock" {
		t.Fatalf("source_provider=%v, want stock", got)
	}
}

func TestBackfillProviderTimestamps_RespectsLimit(t *testing.T) {
	db := newProviderTimestampsTestDB(t)
	insertProviderRow(t, db, "yt-1", "video", "youtube", "a", 1000, 2000, `{}`)
	insertProviderRow(t, db, "yt-2", "video", "youtube", "b", 3000, 4000, `{}`)
	insertProviderRow(t, db, "yt-3", "video", "youtube", "c", 5000, 6000, `{}`)

	matched, updated, err := backfillProviderTimestamps(context.Background(), db, 2)
	if err != nil {
		t.Fatalf("backfill with limit: %v", err)
	}
	if matched != 3 || updated != 2 {
		t.Fatalf("matched=%d updated=%d, want 3 matched / 2 updated with --limit 2", matched, updated)
	}
}
