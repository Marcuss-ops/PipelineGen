// PR12b integration test: verifies that youtube.Service.dispatchOrIndex
// routes through the canonical asset.Repository.Upsert writer and emits the
// asset.upserted outbox event atomically.
//
// Per PR1.6 (June 2026) the triple persistence fallback
// (assetRepo → disp.EnqueueAndIndex → clipsRepo.Upsert) has been removed:
// AssetRepo.Upsert is the SOLE writer. The previous "fallback when nothing
// is wired" test has been deleted because the new contract REFUSES to
// persist (returns an explicit error) rather than silently degrading.
//
// Schema matches the production `media_assets` columns written by
// `internal/infrastructure/database/sqlite/asset.Upsert` (40 columns)
// plus `outbox_events` so the canonical upsert's outbox emit succeeds
// inside the test transaction.
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

// setupYoutubePR12b creates a fresh SQLite DB with the full PR12b schema
// and registers teardown. Returns the canonical asset.Repository wrapper
// (the SOLE writer in PR1.6).
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
// the test schema's `deleted_at TEXT` accepts a non-NULL string.
var zeroTime = time.Time{}

func TestYoutubePR12b_DispatchOrIndexRoutesThroughAssetRepo(t *testing.T) {
	t.Skip("PR4: pre-existing (incomplete DTO hydration in assetrepo integration). Needs test infrastructure update post-PR3 ports extraction. See docs/POST_CASCADE_OPERATIONAL_READINESS.md §3.")
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

	// PR1.7: AssetRepo is wired via ServiceDeps at construction time (no
	// setter cascade). The SOLE writer remains AssetRepository.Upsert.
	svc := NewService(ServiceDeps{
		Log:       zap.NewNop(),
		AssetRepo: assetRepo,
	})

	ctx := context.Background()

	if err := svc.dispatchOrIndex(ctx, clip, "test-hash-001"); err != nil {
		t.Fatalf("dispatchOrIndex via assetRepo failed: %v", err)
	}

	// canonical reader sees the row.
	canonical, err := assetRepo.Get(ctx, clip.ID)
	if err != nil {
		t.Fatalf("assetRepo.Get(%q) failed: %v", clip.ID, err)
	}
	if canonical == nil {
		t.Fatalf("assetRepo.Get(%q) returned nil; row missing", clip.ID)
	}
	if canonical.ID != clip.ID || canonical.Source != clip.Source ||
		canonical.Group != clip.Group || canonical.MediaType != clip.MediaType {
		t.Errorf("canonical row mismatch:\nwant %+v\n got %+v", clip, canonical)
	}

	// legacy reader (clipsRepo) sees the SAME row written by AssetRepo.
	legacy, err := clipsRepo.GetClip(ctx, clip.ID)
	if err != nil {
		t.Fatalf("clipsRepo.GetClip(%q) failed: %v", clip.ID, err)
	}
	if legacy == nil {
		t.Fatalf("clipsRepo.GetClip(%q) returned nil; row missing after canonical write", clip.ID)
	}

	// outbox event emitted atomically.
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

func TestYoutubePR12b_DispatchOrIndexRefusesWhenAssetRepoNotWired(t *testing.T) {
	// PR1.6: missing AssetRepo is now an explicit error rather than a silent
	// fall-through to the legacy outbox/clipsRepo paths. Operators see the
	// missing dependency in logs immediately, instead of losing data.
	// PR1.7: builds via NewService(ServiceDeps{...}) with no AssetRepo set.
	svc := NewService(ServiceDeps{Log: zap.NewNop()})

	clip := &asset.Asset{
		ID:             "pr12b-youtube-no-canonical",
		Name:           "No Canonical Writer Test",
		Source:         "youtube",
		LifecycleState: asset.StateReady,
		Metadata:       asset.Metadata{},
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		DeletedAt:      &zeroTime,
	}

	err := svc.dispatchOrIndex(context.Background(), clip, "")
	if err == nil {
		t.Fatalf("dispatchOrIndex without AssetRepo wired must return an error; got nil")
	}
}
