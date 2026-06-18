// PR12b integration test: verifies that artlist.SearchService.UpsertClip,
// when wired with an assetrepo.Repository via SetAssetRepo, routes through
// the canonical writer AND legacy readers (clips.Repository) observe the
// same row data.
//
// Schema matches the production `media_assets` columns written by
// `internal/infrastructure/database/sqlite/assetrepo.Upsert` (40 columns)
// plus `outbox_events` so the canonical upsert's outbox emit succeeds
// inside the test transaction.
package artlist

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assetrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
)

// pr12bArtlistSchema is the full `media_assets` schema the canonical
// assetrepo.Upsert writes (40 columns) plus the `outbox_events` table the
// same transaction emits to. Mirrors the production tables created by
// migrations up to `062_asset_locations_backfill.sql`.
const pr12bArtlistSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id                  TEXT    PRIMARY KEY,
    source              TEXT    NOT NULL DEFAULT '',
    name                TEXT    NOT NULL DEFAULT '',
    filename            TEXT    NOT NULL DEFAULT '',
    media_type          TEXT    NOT NULL DEFAULT '',
    category            TEXT    NOT NULL DEFAULT '',
    group_name          TEXT    NOT NULL DEFAULT '',
    url                 TEXT    NOT NULL DEFAULT '',
    clip_page_url       TEXT    NOT NULL DEFAULT '',
    thumbnail_url       TEXT    NOT NULL DEFAULT '',
    external_url        TEXT    NOT NULL DEFAULT '',
    duration_ms         INTEGER NOT NULL DEFAULT 0,
    tags                TEXT    NOT NULL DEFAULT '[]',
    tags_norm           TEXT    NOT NULL DEFAULT '',
    search_terms        TEXT    NOT NULL DEFAULT '[]',
    search_text         TEXT    NOT NULL DEFAULT '',
    lifecycle_state     TEXT    NOT NULL DEFAULT '',
    deleted_at          TEXT    NOT NULL DEFAULT '',
    quality_score       REAL    NOT NULL DEFAULT 0.0,
    reuse_count         INTEGER NOT NULL DEFAULT 0,
    last_used_at        TEXT    NOT NULL DEFAULT '',
    scene_type          TEXT    NOT NULL DEFAULT '',
    metadata_json       TEXT    NOT NULL DEFAULT '{}',
    is_folder           INTEGER NOT NULL DEFAULT 0,
    depth               INTEGER NOT NULL DEFAULT 0,
    folder_id           TEXT    NOT NULL DEFAULT '',
    parent_folder_id    TEXT    NOT NULL DEFAULT '',
    folder_path         TEXT    NOT NULL DEFAULT '',
    usable_for          TEXT    NOT NULL DEFAULT '[]',
    avoid_for           TEXT    NOT NULL DEFAULT '[]',
    phash               TEXT    NOT NULL DEFAULT '',
    child_count         INTEGER NOT NULL DEFAULT 0,
    thumb_url           TEXT    NOT NULL DEFAULT '',
    status              TEXT    NOT NULL DEFAULT '',
    error               TEXT    NOT NULL DEFAULT '',
    drive_file_id       TEXT    NOT NULL DEFAULT '',
    drive_link          TEXT    NOT NULL DEFAULT '',
    download_link       TEXT    NOT NULL DEFAULT '',
    local_path          TEXT    NOT NULL DEFAULT '',
    file_hash           TEXT    NOT NULL DEFAULT '',
    created_at          TEXT    NOT NULL DEFAULT '',
    updated_at          TEXT    NOT NULL DEFAULT '',
    visual_embedding_json TEXT  NOT NULL DEFAULT '',
    visual_embedding      TEXT,
    transcript_embedding  TEXT,
    relative_path         TEXT  NOT NULL DEFAULT '',
    drive_folder_id       TEXT  NOT NULL DEFAULT '',
    width                 INTEGER NOT NULL DEFAULT 0,
    height                INTEGER NOT NULL DEFAULT 0,
    embedding_json        TEXT  NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id           TEXT    PRIMARY KEY,
    aggregate_id TEXT    NOT NULL,
    event_type   TEXT    NOT NULL,
    payload_json TEXT    NOT NULL DEFAULT '{}',
    created_at   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_id ON outbox_events(aggregate_id);
`

// setupArtlistPR12b creates a fresh SQLite DB with the full PR12b schema,
// wires clips + assetrepo repos, and registers teardown. Returns the DB
// handle so tests can also query outbox_events directly.
func setupArtlistPR12b(t *testing.T) (db *sql.DB, clipsRepo *clips.Repository, assetRepo *assetrepo.Repository) {
	t.Helper()
	db = storage.NewTestDBWithSchema(t, pr12bArtlistSchema)
	t.Cleanup(func() { _ = db.Close() })
	log := zap.NewNop()
	clipsRepo = clips.NewRepository(db, log)
	assetRepo = assetrepo.New(db, log)
	return
}

// zeroTime is the canonical zero-time used by DeletedAt fixtures so that
// timeutil.FormatPtrRFC3339 binds a non-NULL string (which the test schema's
// `deleted_at TEXT NOT NULL DEFAULT ''` accepts). Without this, nil pointer
// formatting binds SQL NULL and trips the NOT NULL constraint.
var zeroTime = time.Time{}

func TestArtlistPR12b_UpsertClipRoutesThroughAssetRepo(t *testing.T) {
	db, clipsRepo, assetRepo := setupArtlistPR12b(t)

	now := time.Now().UTC().Truncate(time.Second)
	clip := &models.MediaAsset{
		ID:           "pr12b-artlist-001",
		Name:         "PR12b Canonical Writer Test",
		Source:       "artlist",
		Filename:     "pr12b-artlist-001.mp4",
		Group:        "artlist-fixtures",
		MediaType:    "video",
		Tags:         []string{"pr12b", "canonical-writer"},
		ExternalURL:  "https://artlist.io/clip/pr12b-artlist-001",
		DownloadLink: "https://artlist.io/hls/pr12b-artlist-001.m3u8",
		ClipPageURL:  "https://artlist.io/clip/pr12b-artlist-001",
		ThumbURL:     "https://artlist.io/thumb/pr12b-artlist-001.jpg",
		Duration:     30,
		Status:       "ready",
		LocalPath:    "data/artlist/pr12b-artlist-001.mp4",
		DriveLink:    "https://drive.google.com/file/d/pr12b-artlist-001",
		CreatedAt:    now,
		UpdatedAt:    now,
		DeletedAt:    &zeroTime, // non-nil pointer → non-NULL binding
	}

	svc := &Service{log: zap.NewNop(), artlistRepo: clipsRepo}
	ss := NewSearchService(svc)
	ss.SetAssetRepo(assetRepo)

	ctx := context.Background()

	// ── Act: write via the wired service path ──
	if err := ss.UpsertClip(ctx, clip); err != nil {
		t.Fatalf("UpsertClip via assetRepo failed: %v", err)
	}

	// ── Assert 1: canonical reader sees the row via asset.MediaAsset ──
	canonical, err := assetRepo.Get(ctx, clip.ID)
	if err != nil {
		t.Fatalf("assetRepo.Get(%q) failed: %v", clip.ID, err)
	}
	if canonical == nil {
		t.Fatalf("assetRepo.Get(%q) returned nil; row missing", clip.ID)
	}
	if canonical.ID != clip.ID {
		t.Errorf("canonical ID mismatch: want %q, got %q", clip.ID, canonical.ID)
	}
	if canonical.Name != clip.Name {
		t.Errorf("canonical Name mismatch: want %q, got %q", clip.Name, canonical.Name)
	}
	if canonical.Source != clip.Source {
		t.Errorf("canonical Source mismatch: want %q, got %q", clip.Source, canonical.Source)
	}
	if canonical.Group != clip.Group {
		t.Errorf("canonical Group mismatch: want %q, got %q", clip.Group, canonical.Group)
	}
	if canonical.MediaType != clip.MediaType {
		t.Errorf("canonical MediaType mismatch: want %q, got %q", clip.MediaType, canonical.MediaType)
	}
	if canonical.DurationMs != int64(clip.Duration) {
		t.Errorf("canonical DurationMs mismatch: want %d, got %d", clip.Duration, canonical.DurationMs)
	}
	if string(canonical.LifecycleState) != clip.Status {
		t.Errorf("canonical LifecycleState mismatch: want %q, got %q", clip.Status, canonical.LifecycleState)
	}

	// ── Assert 2: legacy reader sees the SAME row via models.MediaAsset ──
	// This is the critical PR12b promise: the canonical writer must persist
	// the legacy physical-location columns too so clips.Repository stays
	// unchanged.
	legacy, err := clipsRepo.Get(ctx, clip.ID)
	if err != nil {
		t.Fatalf("clipsRepo.Get(%q) failed: %v", clip.ID, err)
	}
	if legacy == nil {
		t.Fatalf("clipsRepo.Get(%q) returned nil; row missing after canonical write", clip.ID)
	}
	if legacy.ID != clip.ID {
		t.Errorf("legacy ID mismatch: want %q, got %q", clip.ID, legacy.ID)
	}
	if legacy.Name != clip.Name {
		t.Errorf("legacy Name mismatch: want %q, got %q", clip.Name, legacy.Name)
	}
	if legacy.DriveLink != clip.DriveLink {
		t.Errorf("legacy DriveLink mismatch: want %q, got %q (assetrepo must persist legacy columns)", clip.DriveLink, legacy.DriveLink)
	}
	if legacy.LocalPath != clip.LocalPath {
		t.Errorf("legacy LocalPath mismatch: want %q, got %q", clip.LocalPath, legacy.LocalPath)
	}
	if legacy.DownloadLink != clip.DownloadLink {
		t.Errorf("legacy DownloadLink mismatch: want %q, got %q", clip.DownloadLink, legacy.DownloadLink)
	}
	if legacy.ExternalURL != clip.ExternalURL {
		t.Errorf("legacy ExternalURL mismatch: want %q, got %q", clip.ExternalURL, legacy.ExternalURL)
	}

	// ── Assert 3: outbox_events row was emitted by the canonical upsert ──
	var outboxCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND event_type = 'asset.upserted'",
		clip.ID,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("outbox_events query failed: %v", err)
	}
	if outboxCount != 1 {
		t.Errorf("expected exactly 1 asset.upserted outbox row, got %d", outboxCount)
	}
}

func TestArtlistPR12b_UpsertClipWithoutAssetRepoFallsBack(t *testing.T) {
	// When SetAssetRepo is NOT called, behavior must match the pre-PR12b
	// path so legacy test fixtures and callers continue to work unchanged.
	_, clipsRepo, _ := setupArtlistPR12b(t)

	svc := &Service{log: zap.NewNop(), artlistRepo: clipsRepo}
	ss := NewSearchService(svc)
	// (No SetAssetRepo call)

	clip := &models.MediaAsset{
		ID:           "pr12b-artlist-fallback-001",
		Name:         "Fallback Test",
		Source:       "artlist",
		DownloadLink: "https://artlist.io/hls/fallback.m3u8",
		ExternalURL:  "https://artlist.io/clip/fallback",
		Status:       "ready",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		DeletedAt:    &zeroTime,
	}

	ctx := context.Background()
	if err := ss.UpsertClip(ctx, clip); err != nil {
		t.Fatalf("legacy UpsertClip fallback failed: %v", err)
	}

	if _, err := clipsRepo.Get(ctx, clip.ID); err != nil {
		t.Fatalf("legacy Get after fallback upsert failed: %v", err)
	}
}
