package finalizer

import (
	"database/sql"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	testsupport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry/testsupport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"testing"
)

// setupTestDB creates an in-memory SQLite DB with the canonical tables.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	// Create tables matching the canonical schemas (055, 105, plus media_assets).
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			-- PR-009 (July 2026): E2E wiring finale — index_state column
			-- added to mirror migration 094. The finalizer's spine write
			-- sets it to 'DISCOVERED' literally (matching the wire
			-- shape from PR-008); the IndexingHandler downstream
			-- overwrites to 'INDEXED' after Qdrant upsert.
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			local_path TEXT NOT NULL DEFAULT '',
			source_provider TEXT NOT NULL DEFAULT '',
			source_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			tags_norm TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    search_text TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS asset_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			version_number INTEGER NOT NULL,
			source_uri TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, version_number)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
			uri TEXT NOT NULL,
			external_id TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_renditions (
			id TEXT PRIMARY KEY,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_id INTEGER NOT NULL REFERENCES asset_locations(id),
			kind TEXT NOT NULL,
			container TEXT NOT NULL DEFAULT '',
			codec TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			fps REAL NOT NULL DEFAULT 0,
			bitrate INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, kind)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'QUEUED',
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL DEFAULT '',
			aggregate_type TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			event_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		// Partial UNIQUE INDEX on event_key — required by
		// AssetTxFinalizer.insertOutboxEvent's
		// `ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`.
		// Mirrors the codebase's partial-index pattern
		// (idx_jobs_active_key, idx_artifacts_sha256): the
		// uniqueness triggers only when event_key is non-empty,
		// so one-shot inserts with event_key='' are NOT
		// uniqueness-constrained. This is the fail-closed
		// idempotency contract for re-finalization.
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
			ON outbox_events(event_key) WHERE event_key != ''`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	return db
}

func newTestFinalizer(t *testing.T, db *sql.DB) *AssetTxFinalizer {
	t.Helper()
	return NewAssetTxFinalizer(nil, testsupport.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))
}

func publishedArtifact(assetID, sha256, fileID string) finalization.PublishedArtifact {
	return finalization.PublishedArtifact{
		ArtifactID:     assetID,
		Kind:           finalization.KindVideo,
		Filename:       "test-video.mp4",
		MIMEType:       "video/mp4",
		SizeBytes:      1024,
		SHA256:         sha256,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: fmt.Sprintf("idem-%s", assetID),
		Description:    "Pacquiao lands a clean left hand while Broner backs up.",
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       fileID,
			WebViewLink:  fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
			DownloadLink: fmt.Sprintf("https://drive.google.com/uc?id=%s", fileID),
			FolderID:     "folder-abc",
			FolderPath:   "/test",
			Action:       finalization.PublishCreated,
		},
	}
}
