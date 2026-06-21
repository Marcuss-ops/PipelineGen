// PR12b integration test: verifies that youtube.Service.dispatchOrIndex,
// when wired with an asset.Repository via SetAssetRepo, routes through
// the canonical writer AND legacy readers (assets.ClipsRepository) observe the
// same row data.
//
// Schema matches the production `media_assets` columns written by
// `internal/infrastructure/database/sqlite/asset.Upsert` (40 columns)
// PLUS the legacy columns that `internal/infrastructure/database/assets.ClipsRepository.UpsertClip`
// reads and writes (`tags_norm`, `embedding_json`, `visual_embedding`,
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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// testClipStoreAdapter wraps *assets.ClipsRepository for tests.
type testClipStoreAdapter struct {
	inner *assets.ClipsRepository
}

func (a *testClipStoreAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}
func (a *testClipStoreAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}
func (a *testClipStoreAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}
func (a *testClipStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.inner.DeleteClip(ctx, id)
}
func (a *testClipStoreAdapter) UpdateSearchTerms(ctx context.Context, id, source, title string, tags []string, searchText string) error {
	return a.inner.UpdateSearchTerms(ctx, id, source, title, tags, searchText)
}
func (a *testClipStoreAdapter) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}
func (a *testClipStoreAdapter) ListYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error) {
	return nil, nil
}
func (a *testClipStoreAdapter) ListEnrichedYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error) {
	return nil, nil
}

// pr12bYoutubeSchema mirrors the full production table definitions for testing.
const pr12bYoutubeSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	name TEXT NOT NULL,
	filename TEXT NOT NULL,
	media_type TEXT NOT NULL,
	category TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL DEFAULT '',
	clip_page_url TEXT NOT NULL DEFAULT '',
	thumbnail_url TEXT NOT NULL DEFAULT '',
	external_url TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	tags TEXT NOT NULL DEFAULT '[]',
	search_terms TEXT NOT NULL DEFAULT '[]',
	search_text TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT 'ready',
	deleted_at TEXT,
	created_at TEXT NOT NULL,    updated_at TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    folder_id TEXT NOT NULL DEFAULT '',
	parent_folder_id TEXT NOT NULL DEFAULT '',
	folder_path TEXT NOT NULL DEFAULT '',
	depth INTEGER NOT NULL DEFAULT 0,
	is_folder INTEGER NOT NULL DEFAULT 0,
	child_count INTEGER NOT NULL DEFAULT 0,
	scene_type TEXT NOT NULL DEFAULT '',
	usable_for TEXT NOT NULL DEFAULT '[]',
	avoid_for TEXT NOT NULL DEFAULT '[]',
	phash TEXT NOT NULL DEFAULT '',
	quality_score REAL NOT NULL DEFAULT 0.0,
	reuse_count INTEGER NOT NULL DEFAULT 0,
	last_used_at TEXT NOT NULL DEFAULT '',
	drive_file_id TEXT NOT NULL DEFAULT '',
	drive_link TEXT NOT NULL DEFAULT '',
	download_link TEXT NOT NULL DEFAULT '',
	local_path TEXT NOT NULL DEFAULT '',
	relative_path TEXT NOT NULL DEFAULT '',
	file_hash TEXT NOT NULL DEFAULT '',
	embedding_json TEXT NOT NULL DEFAULT '[]',
	visual_embedding TEXT NOT NULL DEFAULT '[]',
	transcript_embedding TEXT NOT NULL DEFAULT '[]',
	visual_embedding_json TEXT NOT NULL DEFAULT '[]',
	tags_norm TEXT NOT NULL DEFAULT '',
	drive_folder_id TEXT NOT NULL DEFAULT '',
	thumb_url TEXT NOT NULL DEFAULT '',
	width INTEGER NOT NULL DEFAULT 0,
	height INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS asset_locations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_id TEXT NOT NULL,
	location_kind TEXT NOT NULL,
	uri TEXT NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	web_view_link TEXT NOT NULL DEFAULT '',
	download_url TEXT NOT NULL DEFAULT '',
	mime_type TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	file_hash TEXT NOT NULL DEFAULT '',
	is_primary INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(asset_id, location_kind)
);

CREATE TABLE IF NOT EXISTS outbox_events (
	id TEXT PRIMARY KEY,
	aggregate_id TEXT NOT NULL DEFAULT '',
	aggregate_type TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	event_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 10,
	last_error TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT,
	worker_id TEXT NOT NULL DEFAULT '',
	lease_id TEXT NOT NULL DEFAULT '',
	lease_expiry TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_id ON outbox_events(aggregate_id);
`

// setupYoutubePR12b creates a fresh SQLite DB with the full PR12b schema,
// wires clips + assetrepo repos, and registers teardown.
func setupYoutubePR12b(t *testing.T) (db *sql.DB, clipsRepo *assets.ClipsRepository, assetRepo asset.Repository) {
	t.Helper()
	db = drive.NewTestDBWithSchema(t, pr12bYoutubeSchema)
	t.Cleanup(func() { _ = db.Close() })
	log := zap.NewNop()
	clipsRepo = assets.NewClipsRepository(db, log)
	assetStore := asset.NewAssetStoreSQLite(db, log)
	assetRepo = assetStore.AssetRepository()
	return
}

// zeroTime is the canonical zero-time used by DeletedAt fixtures so that
// timeutil.FormatPtrRFC3339 binds a non-NULL string (which the test schema's
// `deleted_at TEXT NOT NULL DEFAULT ”` accepts).
var zeroTime = time.Time{}

func TestYoutubePR12b_DispatchOrIndexRoutesThroughAssetRepo(t *testing.T) {
	db, clipsRepo, assetRepo := setupYoutubePR12b(t)

	now := time.Now().UTC().Truncate(time.Second)
	clip := &asset.Asset{
		ID:             "pr12b-youtube-001",
		Name:           "PR12b Canonical Writer Test (YouTube)",
		Source:         "youtube",
		Filename:       "pr12b-youtube-001.mp4",
		Group:          "youtube-fixtures",
		MediaType:      "video",
		Tags:           []string{"pr12b", "canonical-writer", "youtube"},
		SourceURL:      "https://youtube.com/watch?v=pr12b-youtube-001",
		ClipPageURL:    "https://youtube.com/watch?v=pr12b-youtube-001",
		ThumbnailURL:   "https://i.ytimg.com/vi/pr12b-youtube-001/hqdefault.jpg",
		Duration:       120 * time.Millisecond,
		LifecycleState: asset.StateReady,
		Metadata: asset.Metadata{
			"download_link": "https://youtube.com/download/pr12b-youtube-001.mp4",
			"local_path":    "data/youtube/pr12b-youtube-001.mp4",
			"drive_link":    "https://drive.google.com/file/d/pr12b-youtube-001",
		},
		CreatedAt: now,
		UpdatedAt: now,
		DeletedAt: &zeroTime,
	}

	svc := &Service{
		log: zap.NewNop(),
	}
	svc.SetClipStore(&testClipStoreAdapter{inner: clipsRepo})
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
	if canonical.Duration != clip.Duration {
		t.Errorf("canonical Duration mismatch: want %v, got %v", clip.Duration, canonical.Duration)
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
	if legacy.DriveLink() != clip.DriveLink() {
		t.Errorf("legacy DriveLink mismatch: want %q, got %q", clip.DriveLink(), legacy.DriveLink())
	}
	if legacy.LocalPath() != clip.LocalPath() {
		t.Errorf("legacy LocalPath mismatch: want %q, got %q", clip.LocalPath(), legacy.LocalPath())
	}
	if legacy.DownloadLink() != clip.DownloadLink() {
		t.Errorf("legacy DownloadLink mismatch: want %q, got %q", clip.DownloadLink(), legacy.DownloadLink())
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
		log: zap.NewNop(),
	}
	svc.SetClipStore(&testClipStoreAdapter{inner: clipsRepo})
	// (No SetAssetRepo, no SetDispatcher calls)

	clip := &asset.Asset{
		ID:             "pr12b-youtube-fallback-001",
		Name:           "Fallback Test",
		Source:         "youtube",
		SourceURL:      "https://youtube.com/watch?v=fallback",
		LifecycleState: asset.StateReady,
		Metadata: asset.Metadata{
			"download_link": "https://youtube.com/fallback",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		DeletedAt: &zeroTime,
	}

	ctx := context.Background()
	if err := svc.dispatchOrIndex(ctx, clip, ""); err != nil {
		t.Fatalf("legacy dispatchOrIndex fallback failed: %v", err)
	}

	if _, err := clipsRepo.GetClip(ctx, clip.ID); err != nil {
		t.Fatalf("legacy GetClip after fallback dispatch failed: %v", err)
	}
}
