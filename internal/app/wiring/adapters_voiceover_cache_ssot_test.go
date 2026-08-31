// adapters_voiceover_cache_ssot_test.go certifies the cache SSOT
// invariant for the voiceover cross-run cache: a cache HIT must never
// return a canonical asset taken directly from the cache without
// validating it against SQLite (media_assets).
//
// The voiceoverCacheAdapter.Lookup contract requires:
//  1. A voiceovers row with matching fingerprint + reusable status
//  2. A media_assets row with non-empty DriveFileID (SSOT validation)
//  3. When timingRequired, timing_json_link in metadata
//
// This test proves that missing media_assets row OR empty DriveFileID
// causes a cache MISS (nil return) — the cache never returns a hit
// without SQLite SSOT validation.
package wiring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
)

func newVoiceoverCacheTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Minimal media_assets + voiceovers schema (only columns the
	// adapter reads/writes; this is a focused SSOT test, not a full
	// migration test).
	_, err = db.Exec(`
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT ''
);
CREATE TABLE voiceovers (
    id TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    voice TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
	require.NoError(t, err)
	return db
}

func insertVoiceoverRow(t *testing.T, db *sql.DB, id, fingerprint, status, metadata string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO voiceovers (id, fingerprint, request_id, voice, status, duration_seconds, metadata, created_at, updated_at)
VALUES (?, ?, '', 'alloy', ?, 5.0, ?, datetime('now'), datetime('now'))`,
		id, fingerprint, status, metadata)
	require.NoError(t, err)
}

func insertMediaAssetRow(t *testing.T, db *sql.DB, id, driveFileID, name string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, name, drive_file_id, drive_link, download_link, local_path)
VALUES (?, ?, ?, '', '', '')`, id, name, driveFileID)
	require.NoError(t, err)
}

// TestVoiceoverCacheSSOT_MissingMediaAssetRowIsMiss proves the core
// invariant: a fingerprint match in voiceovers is a cache MISS when the
// media_assets row does not exist (no SSOT validation possible).
func TestVoiceoverCacheSSOT_MissingMediaAssetRowIsMiss(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	insertVoiceoverRow(t, db, "vo-1", "fp-1", "completed", "{}")
	// NO media_assets row — SSOT validation must fail → cache MISS

	hit, err := cache.Lookup(context.Background(), "fp-1", false)
	require.NoError(t, err)
	require.Nil(t, hit, "cache must MISS when media_assets row is absent — no SSOT validation possible")
}

// TestVoiceoverCacheSSOT_EmptyDriveFileIDIsMiss proves that a
// media_assets row with an empty DriveFileID is also a cache MISS.
// The canonical source of truth (media_assets) must confirm the asset
// was uploaded before the cache hit is trusted.
func TestVoiceoverCacheSSOT_EmptyDriveFileIDIsMiss(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	insertVoiceoverRow(t, db, "vo-2", "fp-2", "completed", "{}")
	insertMediaAssetRow(t, db, "vo-2", "", "name") // DriveFileID empty

	hit, err := cache.Lookup(context.Background(), "fp-2", false)
	require.NoError(t, err)
	require.Nil(t, hit, "cache must MISS when media_assets.DriveFileID is empty — asset was not uploaded")
}

// TestVoiceoverCacheSSOT_HealthyRowReturnsHitWithSSOTData proves that
// when media_assets validates the asset (non-empty DriveFileID), the
// cache hit returns data sourced from SQLite (not from the cache row
// alone). The DriveFileID in the hit must come from media_assets, not
// from the voiceovers row.
func TestVoiceoverCacheSSOT_HealthyRowReturnsHitWithSSOTData(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	insertVoiceoverRow(t, db, "vo-3", "fp-3", "completed", `{"timing_json_link":"https://drive/timing.json"}`)
	insertMediaAssetRow(t, db, "vo-3", "drive-file-123", "canonical-name")

	hit, err := cache.Lookup(context.Background(), "fp-3", true)
	require.NoError(t, err)
	require.NotNil(t, hit, "cache must HIT when media_assets validates with non-empty DriveFileID")
	require.Equal(t, "drive-file-123", hit.DriveFileID,
		"DriveFileID must come from media_assets (SSOT), not from the voiceovers cache row")
	require.Equal(t, "canonical-name", hit.Filename,
		"Filename must come from media_assets.name (SSOT), not from the voiceovers row")
}

// TestVoiceoverCacheSSOT_NonReusableStatusIsMiss proves that a
// fingerprint match with a non-reusable status (e.g. "failed") is a
// cache MISS — the SSOT validation includes lifecycle state.
func TestVoiceoverCacheSSOT_NonReusableStatusIsMiss(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	insertVoiceoverRow(t, db, "vo-4", "fp-4", "failed", "{}")
	insertMediaAssetRow(t, db, "vo-4", "drive-file-456", "name")

	hit, err := cache.Lookup(context.Background(), "fp-4", false)
	require.NoError(t, err)
	require.Nil(t, hit, "cache must MISS when voiceover status is not reusable")
}

// TestVoiceoverCacheSSOT_TimingRequiredButMissingIsMiss proves that
// when timingRequired=true and the metadata lacks timing_json_link,
// the cache MISSes — the SSOT validation extends to timing evidence.
func TestVoiceoverCacheSSOT_TimingRequiredButMissingIsMiss(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	insertVoiceoverRow(t, db, "vo-5", "fp-5", "completed", "{}") // no timing_json_link
	insertMediaAssetRow(t, db, "vo-5", "drive-file-789", "name")

	hit, err := cache.Lookup(context.Background(), "fp-5", true)
	require.NoError(t, err)
	require.Nil(t, hit, "cache must MISS when timingRequired but metadata lacks timing_json_link")

	// Without timing requirement, the same row is a valid hit.
	hit, err = cache.Lookup(context.Background(), "fp-5", false)
	require.NoError(t, err)
	require.NotNil(t, hit, "cache must HIT when timing is not required and SSOT validates")
}

// TestVoiceoverCacheSSOT_NilAdapterReturnsNil proves the fail-safe
// path: a nil or unwired adapter returns nil (cache MISS), never a
// stale hit.
func TestVoiceoverCacheSSOT_NilAdapterReturnsNil(t *testing.T) {
	cache := newVoiceoverCacheAdapter(nil, zap.NewNop())
	hit, err := cache.Lookup(context.Background(), "fp-nil", false)
	require.NoError(t, err)
	require.Nil(t, hit, "nil adapter must return nil (MISS), never a stale hit")
}

// TestVoiceoverCacheSSOT_MetadataFromMediaAssetsNotCacheRow is a
// focused regression: the hit's MetaJSON must carry the voiceovers
// metadata, but the location data (DriveFileID, LocalPath) must come
// from media_assets — proving the hit is a hybrid of SSOT-validated
// data, not a raw cache dump.
func TestVoiceoverCacheSSOT_MetadataFromMediaAssetsNotCacheRow(t *testing.T) {
	db := newVoiceoverCacheTestDB(t)
	repo := sqassets.NewVoiceoversRepository(db)
	adapter := newUseCaseRepoAdapter(repo, db)
	cache := newVoiceoverCacheAdapter(adapter, zap.NewNop())

	metaJSON := `{"timing_json_link":"https://drive/timing.json","cleaned_path":"/tmp/cleaned.wav"}`
	insertVoiceoverRow(t, db, "vo-6", "fp-6", "uploaded", metaJSON)
	insertMediaAssetRow(t, db, "vo-6", "drive-ssot-id", "ssot-name")

	hit, err := cache.Lookup(context.Background(), "fp-6", true)
	require.NoError(t, err)
	require.NotNil(t, hit)

	// Location data from media_assets (SSOT)
	require.Equal(t, "drive-ssot-id", hit.DriveFileID)
	require.Equal(t, "ssot-name", hit.Filename)

	// Metadata from voiceovers (provenance, not location)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(hit.MetaJSON, &meta))
	require.Equal(t, "https://drive/timing.json", meta["timing_json_link"])
	require.Equal(t, "/tmp/cleaned.wav", meta["cleaned_path"], "cleaned_path from voiceovers metadata")
}

// Ensure the package compiles with the unused fmt import if test
// helpers evolve.
var _ = fmt.Sprintf
