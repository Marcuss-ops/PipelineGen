package imagesregistry_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

const mediaCommitterSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    category TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '', tags_norm TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, legacy_file_md5 TEXT NOT NULL DEFAULT '', binary_sha256 TEXT NOT NULL DEFAULT '',
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
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    reuse_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    relative_path TEXT NOT NULL DEFAULT '',
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
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
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

func newMediaCommitter(t *testing.T) (*imagesregistry.SQLiteMediaCommitter, *sql.DB) {
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
	committer := imagesregistry.NewSQLiteMediaCommitter(db, box, ledger, nil)
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
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM registry_events WHERE asset_id = ?`, res.AssetID).Scan(&eventCount); err != nil {
		t.Fatalf("count registry events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("registry events after replay = %d, want 1 (deterministic event id)", eventCount)
	}
}

func TestMediaCommitter_CompatibilityPortPreservesCallerTransaction(t *testing.T) {
	c, db := newMediaCommitter(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	req := fullCommitRequest()
	commitResult, err := c.CommitTx(context.Background(), tx, persistence.CommitRequest{
		AssetID: req.Asset.AssetID, Source: req.Asset.Source, Filename: req.Asset.Filename,
		MediaType: req.Asset.MediaType, ContentHash: req.Asset.ContentHash,
		LifecycleState: req.Asset.LifecycleState, SearchText: req.Asset.SearchText,
		SourceVideoID: "abc123", SourceURL: req.Asset.SourceURL, EmitIndexEvent: true,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("CommitTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit caller tx: %v", err)
	}
	if commitResult.AssetRowsAffected != 1 || commitResult.OutboxEventKey == "" {
		t.Fatalf("unexpected compatibility result: %+v", commitResult)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, req.Asset.AssetID).Scan(&count); err != nil {
		t.Fatalf("count committed asset: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed asset rows = %d, want 1", count)
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

func TestMediaCommitter_UnknownContentAllowedForNonIndexableAsset(t *testing.T) {
	c, db := newMediaCommitter(t)
	req := fullCommitRequest()
	req.Asset.ContentHash = ""
	req.Content = nil
	req.IndexPolicy = mediacommit.IndexPolicy{Indexable: false}
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("Drive-only commit with unknown content: %v", err)
	}
	var contentSHA string
	if err := db.QueryRow(`SELECT content_sha256 FROM media_assets WHERE id = ?`, req.Asset.AssetID).Scan(&contentSHA); err != nil {
		t.Fatalf("read content hash: %v", err)
	}
	if contentSHA != "" {
		t.Fatalf("content_sha256 = %q, want unknown empty value", contentSHA)
	}
}

func TestMediaCommitter_RollsBackAllStepsOnLateFailure(t *testing.T) {
	c, db := newMediaCommitter(t)
	req := fullCommitRequest()
	req.TextTracks = []mediacommit.TextTrack{{TextKind: "transcript"}}
	if _, err := c.CommitMediaAsset(context.Background(), req); err == nil {
		t.Fatal("expected invalid text track error")
	}
	for _, table := range []string{"media_assets", "media_asset_sources", "registry_events", "outbox_events"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+map[string]string{"media_assets": "id", "media_asset_sources": "asset_id", "registry_events": "asset_id", "outbox_events": "aggregate_id"}[table]+` = ?`, req.Asset.AssetID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d after rollback, want 0", table, count)
		}
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

func TestUpdateMediaAssetUsage_IncrementsReuseCounter(t *testing.T) {
	c, db := newMediaCommitter(t)
	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	usedAt := "2026-08-31T10:00:00Z"
	if err := imagesregistry.UpdateMediaAssetUsage(context.Background(), db, res.AssetID, usedAt); err != nil {
		t.Fatalf("UpdateMediaAssetUsage: %v", err)
	}
	if err := imagesregistry.UpdateMediaAssetUsage(context.Background(), db, res.AssetID, usedAt); err != nil {
		t.Fatalf("UpdateMediaAssetUsage (second): %v", err)
	}

	var reuseCount int
	var lastUsedAt string
	if err := db.QueryRow(`SELECT reuse_count, last_used_at FROM media_assets WHERE id = ?`, res.AssetID).Scan(&reuseCount, &lastUsedAt); err != nil {
		t.Fatalf("scan usage: %v", err)
	}
	if reuseCount != 2 {
		t.Fatalf("reuse_count = %d, want 2", reuseCount)
	}
	if lastUsedAt != usedAt {
		t.Fatalf("last_used_at = %q, want %q", lastUsedAt, usedAt)
	}
}

func TestUpdateMediaAssetUsage_MissingAssetFailsClosed(t *testing.T) {
	_, db := newMediaCommitter(t)
	err := imagesregistry.UpdateMediaAssetUsage(context.Background(), db, "does-not-exist", "2026-08-31T10:00:00Z")
	if err == nil {
		t.Fatal("expected error for unknown asset")
	}
}

func TestUpdateMediaAssetImageFields_PersistsProjection(t *testing.T) {
	c, db := newMediaCommitter(t)
	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	draft := &mediacommit.ImageDraft{
		URL:          "https://example.com/image.jpg",
		TagsJSON:     `["sea","coast"]`,
		TagsNorm:     "sea coast",
		Width:        1920,
		Height:       1080,
		RelativePath: "images/image.jpg",
		Origin:       "images",
		Provider:     "stock",
	}
	if err := imagesregistry.UpdateMediaAssetImageFields(context.Background(), db, res.AssetID, draft); err != nil {
		t.Fatalf("UpdateMediaAssetImageFields: %v", err)
	}

	var url, tags, tagsNorm, relativePath, origin, provider string
	var width, height int
	if err := db.QueryRow(`SELECT url, tags, tags_norm, width, height, relative_path, origin, provider FROM media_assets WHERE id = ?`, res.AssetID).
		Scan(&url, &tags, &tagsNorm, &width, &height, &relativePath, &origin, &provider); err != nil {
		t.Fatalf("scan image fields: %v", err)
	}
	if url != draft.URL || tags != draft.TagsJSON || tagsNorm != draft.TagsNorm ||
		width != draft.Width || height != draft.Height || relativePath != draft.RelativePath ||
		origin != draft.Origin || provider != draft.Provider {
		t.Fatalf("image fields mismatch: url=%q tags=%q tags_norm=%q w=%d h=%d rel=%q origin=%q provider=%q",
			url, tags, tagsNorm, width, height, relativePath, origin, provider)
	}
}

func TestUpdateMediaAssetLifecycleCAS_GuardedTransition(t *testing.T) {
	c, db := newMediaCommitter(t)
	res, err := c.CommitMediaAsset(context.Background(), fullCommitRequest())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	updatedAt := "2026-08-31T10:00:00Z"
	affected, err := imagesregistry.UpdateMediaAssetLifecycleCAS(context.Background(), tx, res.AssetID, "ACTIVE", "DELETE_REQUESTED", updatedAt)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("UpdateMediaAssetLifecycleCAS: %v", err)
	}
	if affected != 1 {
		_ = tx.Rollback()
		t.Fatalf("affected = %d, want 1", affected)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var lifecycle string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = ?`, res.AssetID).Scan(&lifecycle); err != nil {
		t.Fatalf("scan lifecycle: %v", err)
	}
	if lifecycle != "DELETE_REQUESTED" {
		t.Fatalf("lifecycle_state = %q, want DELETE_REQUESTED", lifecycle)
	}

	// A CAS from a stale expected state must be a zero-row idempotent no-op.
	tx2, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	affected, err = imagesregistry.UpdateMediaAssetLifecycleCAS(context.Background(), tx2, res.AssetID, "ACTIVE", "DELETE_PENDING", updatedAt)
	if err != nil {
		_ = tx2.Rollback()
		t.Fatalf("stale CAS: %v", err)
	}
	if affected != 0 {
		_ = tx2.Rollback()
		t.Fatalf("stale CAS affected = %d, want 0", affected)
	}
	_ = tx2.Rollback()
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
