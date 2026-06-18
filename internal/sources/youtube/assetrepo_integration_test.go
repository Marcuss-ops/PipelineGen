// PR12b integration test: verifies that youtube.Service.dispatchOrIndex,
// when wired with an assetrepo.Repository via SetAssetRepo, routes through
// the canonical writer AND legacy readers (clips.Repository) observe the
// same row data.
//
// Schema matches the production `media_assets` columns written by
// `internal/infrastructure/database/sqlite/assetrepo.Upsert` (40 columns)
// PLUS the legacy columns that `internal/repository/clips.UpsertClip` reads
// and writes (`tags_norm`, `embedding_json`, `visual_embedding`,
// `transcript_embedding`, `relative_path`, `drive_folder_id`, `width`,
// `height`) plus `outbox_events` so the canonical upsert's outbox emit
// succeeds inside the test transaction.
package youtube

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

// pr12bYoutubeSchema mirrors the production `media_assets` (40 columns)
// written by `internal/infrastructure/database/sqlite/assetrepo.Upsert` plus
// the legacy columns clips.Repository.UpsertClip expects
// (tags_norm, embedding_json, visual_embedding, transcript_embedding,
// relative_path, drive_folder_id, width, height) plus `outbox_events` so
// the upsert's transactional outbox emit succeeds inside the test.
const pr12bYoutubeSchema = `
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
    status              TEXT    NOT NULL DEFAULT '',
    error               TEXT    NOT NULL DEFAULT '',
    drive_file_id       TEXT    NOT NULL DEFAULT '',
    drive_link          TEXT    NOT NULL DEFAULT '',
    download_link       TEXT    NOT NULL DEFAULT '',
    local_path          TEXT    NOT NULL DEFAULT '',
    file_hash           TEXT    NOT NULL DEFAULT '',
    created_at          TEXT    NOT NULL DEFAULT '',
    updated_at          TEXT    NOT NULL DEFAULT '',

    -- Legacy columns read/written by clips.Repository.UpsertClip
    tags_norm           TEXT    NOT NULL DEFAULT '',
    embedding_json      TEXT    NOT NULL DEFAULT '[]',
    visual_embedding    TEXT,
    transcript_embedding TEXT,
    relative_path       TEXT    NOT NULL DEFAULT '',
    drive_folder_id     TEXT    NOT NULL DEFAULT '',
    width               INTEGER NOT NULL DEFAULT 0,
    height              INTEGER NOT NULL DEFAULT 0,
    visual_embedding_json TEXT  NOT NULL DEFAULT ''
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

// setupYoutubePR12b creates a fresh SQLite DB with the full PR12b schema,
// wires clips + assetrepo repos, and registers teardown.
func setupYoutubePR12b(t *testing.T) (db *sql.DB, clipsRepo *clips.Repository, assetRepo *assetrepo.Repository) {
	t.Helper()
	db = storage.NewTestDBWithSchema(t, pr12bYoutubeSchema)
	t.Cleanup(func() { _ = db.Close() })
	log := zap.NewNop()
	clipsRepo = clips.NewRepository(db, log)
	assetRepo = assetrepo.New(db, log)
	return
}

// zeroTime is the canonical zero-time used by DeletedAt fixtures so that
// timeutil.FormatPtrRFC3339 binds a non-NULL string (which the test schema's
// `deleted_at TEXT NOT NULL DEFAULT ''` accepts).
var zeroTime = time.Time{}

func TestYoutubePR12b_DispatchOrIndexRoutesThroughAssetRepo(t *testing.T) {
	db, clipsRepo, assetRepo := setupYoutubePR12b(t)

	now := time.Now().UTC().Truncate(time.Second)
	clip := &models.MediaAsset{
		ID:           "pr12b-youtube-001",
		Name:         "PR12b Canonical Writer Test (YouTube)",
		Source:       "youtube",
		Filename:     "pr12b-youtube-001.mp4",
		Group:        "youtube-fixtures",
		MediaType:    "video",
		Tags:         []string{"pr12b", "canonical-writer", "youtube"},
		ExternalURL:  "https://youtube.com/watch?v=pr12b-youtube-001",
		DownloadLink: "https://youtube.com/download/pr12b-youtube-001.mp4",
		ClipPageURL:  "https://youtube.com/watch?v=pr12b-youtube-001",
		ThumbURL:     "https://i.ytimg.com/vi/pr12b-youtube-001/hqdefault.jpg",
		Duration:     120,
		Status:       "ready",
		LocalPath:    "data/youtube/pr12b-youtube-001.mp4",
		DriveLink:    "https://drive.google.com/file/d/pr12b-youtube-001",
		CreatedAt:    now,
		UpdatedAt:    now,
		DeletedAt:    &zeroTime,
	}

	svc := &Service{
		log:       zap.NewNop(),
		clipsRepo: clipsRepo,
	}
	svc.SetAssetRepo(assetRepo)

	ctx := context.Background()

	if err := svc.dispatchOrIndex(ctx, clip, "test-hash-001"); err != nil {
		t.Fatalf("dispatchOrIndex via assetRepo failed: %v", err)
	}

	// ── Assert 1: canonical reader ──
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

	// ── Assert 2: legacy reader sees the SAME row ──
	legacy, err := clipsRepo.GetClip(ctx, clip.ID)
	if err != nil {
		t.Fatalf("clipsRepo.GetClip(%q) failed: %v", clip.ID, err)
	}
	if legacy == nil {
		t.Fatalf("clipsRepo.GetClip(%q) returned nil; row missing after canonical write", clip.ID)
	}
	if legacy.ID != clip.ID {
		t.Errorf("legacy ID mismatch: want %q, got %q", clip.ID, legacy.ID)
	}
	if legacy.Name != clip.Name {
		t.Errorf("legacy Name mismatch: want %q, got %q", clip.Name, legacy.Name)
	}
	if legacy.DriveLink != clip.DriveLink {
		t.Errorf("legacy DriveLink mismatch: want %q, got %q", clip.DriveLink, legacy.DriveLink)
	}
	if legacy.LocalPath != clip.LocalPath {
		t.Errorf("legacy LocalPath mismatch: want %q, got %q", clip.LocalPath, legacy.LocalPath)
	}
	if legacy.DownloadLink != clip.DownloadLink {
		t.Errorf("legacy DownloadLink mismatch: want %q, got %q", clip.DownloadLink, legacy.DownloadLink)
	}

	// ── Assert 3: outbox row was emitted ──
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

func TestYoutubePR12b_DispatchOrIndexWithoutAssetRepoFallsBack(t *testing.T) {
	// When neither SetAssetRepo nor SetDispatcher is called, the legacy
	// clipsRepo.UpsertClip path must continue to work unchanged.
	_, clipsRepo, _ := setupYoutubePR12b(t)

	svc := &Service{
		log:       zap.NewNop(),
		clipsRepo: clipsRepo,
	}
	// (No SetAssetRepo, no SetDispatcher calls)

	clip := &models.MediaAsset{
		ID:           "pr12b-youtube-fallback-001",
		Name:         "Fallback Test",
		Source:       "youtube",
		ExternalURL:  "https://youtube.com/watch?v=fallback",
		DownloadLink: "https://youtube.com/fallback",
		Status:       "ready",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		DeletedAt:    &zeroTime,
	}

	ctx := context.Background()
	if err := svc.dispatchOrIndex(ctx, clip, ""); err != nil {
		t.Fatalf("legacy dispatchOrIndex fallback failed: %v", err)
	}

	if _, err := clipsRepo.GetClip(ctx, clip.ID); err != nil {
		t.Fatalf("legacy GetClip after fallback dispatch failed: %v", err)
	}
}
