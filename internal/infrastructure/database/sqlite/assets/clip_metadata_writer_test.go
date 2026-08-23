// Package assets — clip_metadata_writer_test.go: TDD lock-in for
// the canonical ClipMetadataWriterAdapter.
//
// Test coverage targets (the verdict's P1 #15 + #16 contract):
//   - UpdateClipMetadataAndRequestIndex creates 1 outbox row per
//     call (commit-success path).
//   - Re-issuing the same (clipID, sourceVersion) collapses via
//     ON CONFLICT DO NOTHING (idempotency).
//   - Different sourceVersion produces a fresh outbox row (re-index).
//   - Empty clipID / mismatched m.ClipID fail-closed.
package assets

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// testDBForMetadataWriter opens a fresh on-disk SQLite DB with
// the canonical media_assets + outbox_events schema. The schema
// is the minimal subset the writer needs.
func testDBForMetadataWriter(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	dsn := dbPath + "?_journal=WAL&_busy_timeout=5000&_fk=1"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
	})
	// Apply the minimal media_assets + outbox_events schema.
	// Note: metadata_json is the canonical column for the
	// 9 metadata keys the writer UPDATE targets.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			updated_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '',);
	`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			event_key TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 10,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
			ON outbox_events(event_key);
	`); err != nil {
		t.Fatalf("create outbox_events: %v", err)
	}
	return db
}

// seedMediaAsset inserts a media_assets row so the writer's
// UPDATE has a row to update.
func seedMediaAsset(t *testing.T, db *sql.DB, clipID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (id, source, name, filename, media_type, lifecycle_state, updated_at, created_at, metadata_json)
		VALUES (?, 'youtube', 'name', 'file.mp4', 'video', 'ACTIVE', '2026-06-30T00:00:00Z', '2026-06-30T00:00:00Z', '{}')
	`, clipID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// countOutboxRows returns the number of outbox_events rows for
// the given aggregate_id.
func countOutboxRows(t *testing.T, db *sql.DB, aggregateID string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, aggregateID).Scan(&n)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// readMetadataJSON returns the metadata_json column for a clip.
func readMetadataJSON(t *testing.T, db *sql.DB, clipID string) string {
	t.Helper()
	var s string
	err := db.QueryRow(`SELECT COALESCE(metadata_json, '{}') FROM media_assets WHERE id = ?`, clipID).Scan(&s)
	if err != nil {
		t.Fatalf("read metadata_json: %v", err)
	}
	return s
}

// ── Tests ─────────────────────────────────────────────────────────────

func TestClipMetadataWriter_UpdatesMediaAndEmitsOutbox(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipMetadataWriterAdapter(db, box, zap.NewNop())
	clipID := "yt_abc_0_60_v1"
	seedMediaAsset(t, db, clipID)

	err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), clipID, youtubetypes.CanonicalClipMetadata{
		ClipID:          clipID,
		Summary:         "Test summary",
		Topics:          []string{"topic1", "topic2"},
		Speakers:        []string{"host"},
		MentionedPeople: []string{"guest"},
		QualityScore:    0.85,
		SponsorSegment:  false,
		TranscriptPath:  "/tmp/transcript.txt",
		SourceURL:       "https://www.youtube.com/watch?v=abc",
		NormalizedGroup: "general",
		SourceVersion:   "abc123",
		JobID:           "job-1",
	})
	if err != nil {
		t.Fatalf("UpdateClipMetadataAndRequestIndex: %v", err)
	}
	// 1 outbox row.
	if got := countOutboxRows(t, db, clipID); got != 1 {
		t.Errorf("expected 1 outbox row; got %d", got)
	}
	// media_assets.metadata_json has the 9 keys.
	// Note: PR-YT-DOD-7 switched from nested json_set (which stored
	// Go bool as SQLite integer 0/1) to json_patch with Go-side
	// json.Marshal (which produces proper JSON false/true).
	md := readMetadataJSON(t, db, clipID)
	for _, want := range []string{`"summary":"Test summary"`, `"quality_score":0.85`, `"sponsor_segment":false`, `"topics"`} {
		if !strings.Contains(md, want) {
			t.Errorf("metadata_json missing %q; got %s", want, md)
		}
	}
}

func TestClipMetadataWriter_IdempotentOnSameSourceVersion(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipMetadataWriterAdapter(db, box, zap.NewNop())
	clipID := "yt_abc_0_60_v1"
	seedMediaAsset(t, db, clipID)
	md := youtubetypes.CanonicalClipMetadata{
		ClipID:        clipID,
		Summary:       "Test",
		QualityScore:  0.75,
		SourceVersion: "same-hash",
	}
	if err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), clipID, md); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), clipID, md); err != nil {
		t.Fatalf("second: %v", err)
	}
	// ON CONFLICT(event_key) DO NOTHING → still 1 outbox row.
	if got := countOutboxRows(t, db, clipID); got != 1 {
		t.Errorf("expected 1 outbox row (idempotent); got %d", got)
	}
}

func TestClipMetadataWriter_DifferentSourceVersionCreatesNewRow(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipMetadataWriterAdapter(db, box, zap.NewNop())
	clipID := "yt_abc_0_60_v1"
	seedMediaAsset(t, db, clipID)
	if err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), clipID, youtubetypes.CanonicalClipMetadata{
		ClipID:        clipID,
		QualityScore:  0.5,
		SourceVersion: "v1",
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), clipID, youtubetypes.CanonicalClipMetadata{
		ClipID:        clipID,
		QualityScore:  0.6,
		SourceVersion: "v2",
	}); err != nil {
		t.Fatalf("second: %v", err)
	}
	// Different sourceVersion → different event_key → 2 outbox rows.
	if got := countOutboxRows(t, db, clipID); got != 2 {
		t.Errorf("expected 2 outbox rows (different sourceVersion); got %d", got)
	}
}

func TestClipMetadataWriter_EmptyClipIDFailsClosed(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipMetadataWriterAdapter(db, box, zap.NewNop())
	err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), "", youtubetypes.CanonicalClipMetadata{
		ClipID: "x",
	})
	if err == nil {
		t.Fatal("expected error for empty clipID; got nil")
	}
}

func TestClipMetadataWriter_MismatchedClipIDFailsClosed(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipMetadataWriterAdapter(db, box, zap.NewNop())
	err := adapter.UpdateClipMetadataAndRequestIndex(context.Background(), "yt_a_0_60_v1", youtubetypes.CanonicalClipMetadata{
		ClipID: "yt_b_0_60_v1",
	})
	if err == nil {
		t.Fatal("expected error for mismatched ClipID; got nil")
	}
}

// ── Test helpers ─────────────────────────────────────────────────────
//
// Note: PR-YT-DOD-7 switched the writer from nested json_set
// (which stored Go bool as SQLite integer 0/1) to Go-side
// json.Marshal + json_patch (which produces proper JSON
// false/true). Tests assert against the correct JSON boolean
// representation.
