// cmd/admin/backfill_source_url_metadata_test.go — contract tests for the
// source_url convergence backfill (Fase D).
//
// Pins three invariants of backfillSourceURLMetadata:
//   - image rows are excluded (url column = Drive link, metadata key =
//     original source — intentionally different, never merged);
//   - an existing source_url key is never overwritten (additive);
//   - the operation is idempotent: a second run matches zero rows.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newBackfillTestDB builds an in-memory media_assets table with the
// minimal columns the backfill touches.
func newBackfillTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL DEFAULT '',
		media_type TEXT NOT NULL DEFAULT '',
		metadata_json TEXT NOT NULL DEFAULT '{}',
		updated_at TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	return db
}

func insertBackfillRow(t *testing.T, db *sql.DB, id, url, mediaType, metadataJSON string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, url, media_type, metadata_json, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, url, mediaType, metadataJSON, "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func readBackfillRow(t *testing.T, db *sql.DB, id string) (string, string) {
	t.Helper()
	var metadataJSON, updatedAt string
	if err := db.QueryRow(`SELECT metadata_json, updated_at FROM media_assets WHERE id = ?`, id).Scan(&metadataJSON, &updatedAt); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return metadataJSON, updatedAt
}

func TestBackfillSourceURLMetadata_BackfillsNonImage(t *testing.T) {
	db := newBackfillTestDB(t)
	insertBackfillRow(t, db, "clip-1", "https://example.com/a.mp4", "clip", `{"title":"A"}`)
	// Image row: url is the canonicalized Drive link; must NOT be merged.
	insertBackfillRow(t, db, "img-1", "https://drive.google.com/file/d/X/view", "image", `{"title":"Img"}`)
	// Legacy row with NULL media_type behaves like a non-image row.
	insertBackfillRow(t, db, "legacy-1", "https://example.com/legacy.mp4", "", `{}`)

	matched, updated, err := backfillSourceURLMetadata(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if matched != 2 || updated != 2 {
		t.Fatalf("matched=%d updated=%d, want 2/2 (image row excluded)", matched, updated)
	}

	if got := metadataKey(t, db, "clip-1", "source_url"); got != "https://example.com/a.mp4" {
		t.Fatalf("clip-1 source_url=%q, want backfilled url", got)
	}
	if got := metadataKey(t, db, "legacy-1", "source_url"); got != "https://example.com/legacy.mp4" {
		t.Fatalf("legacy-1 source_url=%q, want backfilled url", got)
	}

	// Image row untouched: the source_url key must remain absent.
	meta := readMetadataMap(t, db, "img-1")
	if _, ok := meta["source_url"]; ok {
		t.Fatalf("image row must keep its divergence: source_url key present: %v", meta)
	}
}

func TestBackfillSourceURLMetadata_DoesNotOverwriteExistingKey(t *testing.T) {
	db := newBackfillTestDB(t)
	insertBackfillRow(t, db, "clip-1", "https://example.com/current.mp4", "clip", `{"source_url":"https://example.com/original.mp4"}`)

	matched, updated, err := backfillSourceURLMetadata(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if matched != 0 || updated != 0 {
		t.Fatalf("matched=%d updated=%d, want 0/0 (existing key must be preserved)", matched, updated)
	}
	if got := metadataKey(t, db, "clip-1", "source_url"); got != "https://example.com/original.mp4" {
		t.Fatalf("existing source_url overwritten: %q", got)
	}
}

func TestBackfillSourceURLMetadata_IsIdempotent(t *testing.T) {
	db := newBackfillTestDB(t)
	insertBackfillRow(t, db, "clip-1", "https://example.com/a.mp4", "clip", `{"title":"A"}`)

	if _, updated, err := backfillSourceURLMetadata(context.Background(), db, 0); err != nil || updated != 1 {
		t.Fatalf("first run: updated=%d err=%v, want 1/nil", updated, err)
	}
	before, _ := readBackfillRow(t, db, "clip-1")

	// Second run must be a no-op (key now present).
	matched, updated, err := backfillSourceURLMetadata(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if matched != 0 || updated != 0 {
		t.Fatalf("second run: matched=%d updated=%d, want 0/0", matched, updated)
	}
	after, _ := readBackfillRow(t, db, "clip-1")
	if before != after {
		t.Fatalf("idempotency violated: metadata_json changed on second run\n before=%s\n after=%s", before, after)
	}
}

// readMetadataMap decodes a row's metadata_json into a fresh map (no
// cross-test map reuse, which json.Unmarshal would silently merge).
func readMetadataMap(t *testing.T, db *sql.DB, id string) map[string]any {
	t.Helper()
	metaJSON, _ := readBackfillRow(t, db, id)
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("decode %s metadata: %v", id, err)
	}
	return meta
}

func metadataKey(t *testing.T, db *sql.DB, id, key string) string {
	t.Helper()
	meta := readMetadataMap(t, db, id)
	got, _ := meta[key].(string)
	return got
}

func TestBackfillSourceURLMetadata_RespectsLimit(t *testing.T) {
	db := newBackfillTestDB(t)
	for i := 0; i < 3; i++ {
		id := "clip-" + time.Duration(i).String() + "-" + string(rune('a'+i))
		insertBackfillRow(t, db, id, "https://example.com/"+id+".mp4", "clip", `{}`)
	}
	matched, updated, err := backfillSourceURLMetadata(context.Background(), db, 2)
	if err != nil {
		t.Fatalf("backfill with limit: %v", err)
	}
	if matched != 3 || updated != 2 {
		t.Fatalf("matched=%d updated=%d, want 3 matched / 2 updated with --limit 2", matched, updated)
	}
}
