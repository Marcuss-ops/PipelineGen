package assets

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mediacommit"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

const mediaCommitterSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    category TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, file_hash TEXT, binary_sha256 TEXT NOT NULL DEFAULT '',
    folder_id TEXT, folder_path TEXT,
    source_version TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    index_state TEXT NOT NULL DEFAULT '',
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
    content_sha256 TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    created_at TEXT, updated_at TEXT
);
CREATE TABLE IF NOT EXISTS asset_locations (
    asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    web_view_link TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (asset_id, location_kind)
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT,
    worker_id TEXT,
    lease_id TEXT,
    lease_expiry TEXT,
    completed_at TEXT,
    next_attempt_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key ON outbox_events(event_key);
CREATE TABLE IF NOT EXISTS media_asset_sources (
    source_id      TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL,
    source_uri     TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    discovered_at  TEXT NOT NULL,
    is_primary     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS registry_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    asset_id TEXT,
    event_type TEXT NOT NULL,
    run_id TEXT,
    actor TEXT NOT NULL DEFAULT '',
    before_hash TEXT NOT NULL DEFAULT '',
    after_hash TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    git_sha TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    language_code TEXT NOT NULL,
    text_kind TEXT NOT NULL,
    text_content TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    model_version TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    translation_key TEXT NOT NULL DEFAULT '',
    is_current INTEGER NOT NULL DEFAULT 1,
    source_track_id INTEGER,
    source_text_hash TEXT NOT NULL DEFAULT '',
    confidence REAL,
    status TEXT NOT NULL DEFAULT 'READY',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;
`

func newMediaCommitter(t *testing.T) (*SQLiteMediaCommitter, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(mediaCommitterSchema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	box := outboxevents.NewRepository(db)
	ledger, err := sqlitemediaregistry.NewLedger(db)
	if err != nil {
		t.Fatalf("new ledger: %v", err)
	}
	committer := NewSQLiteMediaCommitter(db, box, ledger, nil)
	return committer, db
}

func fullCommitRequest() mediacommit.CommitMediaAssetRequest {
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID:        "yt_abc123_10_60_v1",
			Source:         "youtube",
			Name:           "Funny Moment",
			Filename:       "clip.mp4",
			MediaType:      "video",
			ContentHash:    "sha256:content",
			Description:    "A funny moment",
			SearchText:     "funny moment",
			LifecycleState: "ACTIVE",
			IndexState:     "DISCOVERED",
		},
		Source: mediacommit.AssetSourceDraft{
			SourceType:    "youtube",
			SourceURI:     "https://www.youtube.com/watch?v=abc123",
			SourceVersion: "sha256:content",
			IsPrimary:     true,
		},
		Taxonomy: capregistry.AssetTaxonomy{
			Namespace:  "stock",
			MediaType:  capregistry.MediaVideo,
			AssetKind:  capregistry.AssetClip,
			SourceType: "youtube",
		},
		Content: &mediacommit.ContentIdentity{ContentSHA256: "sha256:bytes"},
		TextTracks: []mediacommit.TextTrack{
			{LanguageCode: "en", TextKind: "transcript", TextContent: "hello world"},
		},
		IndexPolicy: mediacommit.IndexPolicy{Indexable: true},
		Actor:       "test",
	}
}

func TestMediaCommitter_HappyPath_AllEightSteps(t *testing.T) {
	c, db := newMediaCommitter(t)

	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("CommitMediaAsset: %v", err)
	}
	if !res.Created {
		t.Fatal("expected Created=true for first commit")
	}
	if res.SourceID == "" || res.AssetID != "yt_abc123_10_60_v1" {
		t.Fatalf("unexpected result identity: %+v", res)
	}
	if res.ContentSHA256 != "sha256:bytes" {
		t.Fatalf("ContentSHA256 = %q, want sha256:bytes", res.ContentSHA256)
	}
	if res.RegistrySeq <= 0 {
		t.Fatalf("RegistrySeq = %d, want > 0", res.RegistrySeq)
	}

	// Asset row + taxonomy dimensions + content link.
	var namespace, assetKind, sourceType, contentSHA string
	if err := db.QueryRow(`SELECT namespace, asset_kind, source_type, content_sha256 FROM media_assets WHERE id = ?`, res.AssetID).
		Scan(&namespace, &assetKind, &sourceType, &contentSHA); err != nil {
		t.Fatalf("scan media_assets: %v", err)
	}
	if namespace != "stock" || assetKind != "clip" || sourceType != "youtube" {
		t.Fatalf("taxonomy mismatch: ns=%q kind=%q source_type=%q", namespace, assetKind, sourceType)
	}
	if contentSHA != "sha256:bytes" {
		t.Fatalf("content_sha256 = %q, want sha256:bytes", contentSHA)
	}

	// Source row.
	var srcCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_asset_sources WHERE asset_id = ?`, res.AssetID).Scan(&srcCount); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if srcCount != 1 {
		t.Fatalf("source rows = %d, want 1", srcCount)
	}

	// Registry event.
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM registry_events WHERE asset_id = ?`, res.AssetID).Scan(&eventCount); err != nil {
		t.Fatalf("count registry events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("registry events = %d, want 1", eventCount)
	}

	// Text track.
	var trackCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id = ? AND is_current = 1`, res.AssetID).Scan(&trackCount); err != nil {
		t.Fatalf("count text tracks: %v", err)
	}
	if trackCount != 1 {
		t.Fatalf("current text tracks = %d, want 1", trackCount)
	}

	// Outbox index request.
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, res.AssetID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 1 {
		t.Fatalf("outbox events = %d, want 1", outCount)
	}
}

func TestMediaCommitter_SecondCommit_NotCreated_NoDuplicateSource(t *testing.T) {
	c, db := newMediaCommitter(t)

	if _, err := c.CommitMediaAsset(context.Background(), fullCommitRequest()); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if res.Created {
		t.Fatal("expected Created=false for second commit")
	}
	var srcCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_asset_sources WHERE asset_id = ?`, res.AssetID).Scan(&srcCount); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if srcCount != 1 {
		t.Fatalf("source rows after re-commit = %d, want 1 (idempotent upsert)", srcCount)
	}
}

func TestMediaCommitter_NotIndexable_SkipsOutbox(t *testing.T) {
	c, db := newMediaCommitter(t)

	req := fullCommitRequest()
	req.IndexPolicy = mediacommit.IndexPolicy{Indexable: false}
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, req.Asset.AssetID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 0 {
		t.Fatalf("outbox events = %d, want 0 for non-indexable asset", outCount)
	}
}

func TestMediaCommitter_Validation(t *testing.T) {
	c, _ := newMediaCommitter(t)

	req := fullCommitRequest()
	req.Asset.AssetID = ""
	if _, err := c.CommitMediaAsset(context.Background(), req); err == nil {
		t.Fatal("expected validation error for empty asset id")
	}
}

func TestMediaCommitter_CommitLegacy(t *testing.T) {
	c, db := newMediaCommitter(t)

	res, err := c.CommitLegacy(context.Background(), persistence.CommitRequest{
		AssetID:        "legacy_asset_1",
		Source:         "youtube",
		Filename:       "legacy.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:legacy",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
	})
	if err != nil {
		t.Fatalf("CommitLegacy: %v", err)
	}
	if res.AssetID != "legacy_asset_1" {
		t.Fatalf("asset id = %q", res.AssetID)
	}
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, res.AssetID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 1 {
		t.Fatalf("outbox events = %d, want 1", outCount)
	}
}
