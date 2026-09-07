package jobs

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"testing"
)

// testCombinedSchema creates media_assets + outbox_events + asset_versions
// + asset_locations tables in a single in-memory SQLite DB. This mirrors
// the real production schema where both the finalizer and the outbox
// handler share the same database.
const testCombinedSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	filename TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	legacy_file_md5 TEXT NOT NULL DEFAULT '',
	drive_file_id TEXT NOT NULL DEFAULT '',
	drive_link TEXT NOT NULL DEFAULT '',
	download_link TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT '',
	folder_path TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
	index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	width INTEGER NOT NULL DEFAULT 0,
	height INTEGER NOT NULL DEFAULT 0,
	local_path TEXT NOT NULL DEFAULT '',
	source_provider TEXT NOT NULL DEFAULT '',
	source_version TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
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
    tags TEXT NOT NULL DEFAULT '',
    tags_norm TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS asset_versions (
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
);
CREATE TABLE IF NOT EXISTS asset_locations (
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
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT    NOT NULL,
    aggregate_id    TEXT    NOT NULL,
    aggregate_type  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL,
    event_key       TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    priority        INTEGER NOT NULL DEFAULT 5,
    last_error      TEXT    NOT NULL DEFAULT '',
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    next_attempt_at DATETIME,
    completed_at    DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key) WHERE event_key != '';
CREATE INDEX IF NOT EXISTS IX_outbox_events_status_next
    ON outbox_events(status, next_attempt_at);
`

func openInMemDB_Integration(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(testCombinedSchema); err != nil {
		t.Fatalf("apply combined schema: %v", err)
	}
	return db
}
