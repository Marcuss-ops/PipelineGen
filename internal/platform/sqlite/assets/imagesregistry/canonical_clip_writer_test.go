// Package assets — clip_atomic_writer_test.go pins the canonical
// 5-step tx shape (BEGIN → UPSERT → BUILD envelope → INSERT outbox
// ON CONFLICT(event_key) DO NOTHING → COMMIT) of the new
// ClipAtomicWriterAdapter.
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #6): the
// adapter now takes `youtubetypes.ClipAsset` (the canonical, strongly-
// typed internal domain entity) instead of `youtubetypes.ExtractItem`
// (the HTTP response shape). Tests updated to the new signature.
//
// What this test asserts:
//  1. Happy path: CommitClipAndIndexEvent on a fresh ledger inserts
//     exactly ONE media_assets row + ONE outbox_events row in one
//     tx, and the schema_version literal matches
//     outboxevents.ReindexEnvelopeV1Schema. source_version is
//     persisted in the media_assets row (BLOCKER #2 closure).
//  2. Idempotency: a second call with the same clipID + same
//     LegacyFileMD5-derived sourceVersion collapses the outbox half via
//     ON CONFLICT(event_key) DO NOTHING. media_assets row is
//     updated-once via ON CONFLICT(id) DO UPDATE.
//  3. Different LegacyFileMD5 produces a different eventKey → second
//     INSERT goes through with a NEW outbox_events row (the
//     canonical content-hash supersede pattern).
//  4. Tx rollback: outbox half failure propagates the error and
//     leaves media_assets UNTOUCHED (the P0 #3 fail-closed detector
//     in reverse — the silent-success regression the use case
//     pre-fix code carried).
//
// Schema: minimal media_assets + outbox_events matching production
// shape (only the columns the writer owns — see
// clip_atomic_writer.go::upsertClipInTx comment block).
//
// Layering note: the test file imports youtubeports because the
// adapter itself implements the port (same package, same layer).
// This is NOT a layering violation — the adapter file already
// imports youtubeports to satisfy AGENTS.md Pattern 0.
package imagesregistry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// clipAtomicWriterSchema is the minimal test schema (production-faithful
// subset). Column set matches what upsertClipInTx writes and what the
// canonical envelope inserts (via outboxevents.Repository.Enqueue).
const clipAtomicWriterSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    category TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '', tags_norm TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, legacy_file_md5 TEXT, binary_sha256 TEXT NOT NULL DEFAULT '',
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
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    created_at TEXT, updated_at TEXT,
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '');
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
`

// newAtomicWriterDB opens an in-memory SQLite with the minimal
// clip-atomic-writer schema applied. SetMaxOpenConns(1) is required
// for in-memory sqlite (per txmutation/primitives_test.go precedent —
// the connection pool can hand out different per-connection in-memory
// stores otherwise).
func newAtomicWriterDB(t *testing.T) *sql.DB {
	t.Helper()
	db, openErr := sql.Open("sqlite3", ":memory:")
	if openErr != nil {
		t.Fatalf("open :memory: sqlite: %v", openErr)
	}
	db.SetMaxOpenConns(1)
	if _, execErr := db.Exec(clipAtomicWriterSchema); execErr != nil {
		t.Fatalf("apply schema: %v", execErr)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// canonicalEnvelopePayload resets the event metadata to its zero value.
// The writer fills AggregateID from clipID when empty and the canonical
// AssetCommitter owns the event type and envelope body.
func canonicalEnvelopePayload() youtubeports.IndexEventPayload {
	return youtubeports.IndexEventPayload{}
}

// sha256Hex returns a 64-hex string the writer treats as the canonical
// LegacyFileMD5 shape (the production path uses MD5, but the writer does
// not enforce hash length — any non-empty string is valid).
func sha256Hex(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(h[:])
}

// ── Test 1: happy path insert + outbox row ──────────────────────────

// TestClipAtomicWriter_HappyPathInsertAndOutbox pins the canonical
// 5-step tx shape. After CommitClipAndIndexEvent returns nil we
// expect exactly ONE media_assets row + ONE outbox_events row,
// both matching the input.
func TestClipAtomicWriter_HappyPathInsertAndOutbox(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)

	const clipID = "yt_abc123_10_60_v1"
	item := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "abc123",
		LegacyFileMD5: sha256Hex("happy-path"),
		LocalPath:     "/tmp/clips/yt_abc123_10_60_v1.mp4",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder_xyz",
			FolderPath:  "youtube/abc123",
			FileID:      "drive_xyz",
			WebViewLink: "https://drive.google.com/file/d/drive_xyz/view",
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 10,
			EndSec:   60,
			Duration: 50,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Funny Moment",
			Topics:          []string{"humor"},
			SourceURL:       "https://www.youtube.com/watch?v=abc123",
			NormalizedGroup: "general",
		},
		PolicyVersion: "v1",
	}
	if err := adapter.CommitClipAndIndexEvent(context.Background(), clipID, item, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("CommitClipAndIndexEvent happy path: %v", err)
	}

	// Verify media_assets row (BLOCKER #2: source_version now persisted).
	var (
		gotName, gotFileHash, gotLocalPath, gotSourceVersion, gotLifecycle string
		gotDriveFileID, gotDriveLink                                       string
	)
	row := db.QueryRow(`
		SELECT name, legacy_file_md5, local_path, drive_file_id, drive_link, source_version, lifecycle_state
		FROM media_assets WHERE id = ?`, clipID)
	if err := row.Scan(&gotName, &gotFileHash, &gotLocalPath, &gotDriveFileID, &gotDriveLink, &gotSourceVersion, &gotLifecycle); err != nil {
		t.Fatalf("scan media_assets row: %v", err)
	}
	if gotName != item.Metadata.Summary {
		t.Errorf("name: want %q got %q", item.Metadata.Summary, gotName)
	}
	if gotFileHash != item.LegacyFileMD5 {
		t.Errorf("legacy_file_md5: want %q got %q", item.LegacyFileMD5, gotFileHash)
	}
	if gotLocalPath != item.LocalPath {
		t.Errorf("local_path: want %q got %q", item.LocalPath, gotLocalPath)
	}
	if gotDriveFileID != item.Drive.FileID {
		t.Errorf("drive_file_id: want %q got %q", item.Drive.FileID, gotDriveFileID)
	}
	if gotDriveLink != item.Drive.WebViewLink {
		t.Errorf("drive_link: want %q got %q", item.Drive.WebViewLink, gotDriveLink)
	}
	// BLOCKER #2 closure: source_version must be non-empty and must
	// match what deriveSourceVersion computes (asset.LegacyFileMD5 when
	// non-empty). The clipindexer's CAS fence reads this column.
	if gotSourceVersion == "" {
		t.Errorf("BLOCKER #2: source_version must be non-empty after CommitClipAndIndexEvent (CAS fence starves on empty)")
	}
	if gotSourceVersion != item.LegacyFileMD5 {
		t.Errorf("BLOCKER #2: source_version must equal LegacyFileMD5 when LegacyFileMD5 is non-empty; want %q got %q", item.LegacyFileMD5, gotSourceVersion)
	}
	if gotLifecycle != "ACTIVE" {
		t.Errorf("lifecycle_state: want ACTIVE got %q", gotLifecycle)
	}

	// Verify outbox row count and shape.
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if outCount != 1 {
		t.Fatalf("outbox_events rows for %s: want 1 got %d", clipID, outCount)
	}

	var (
		gotEventType, gotAggID, gotAggType, gotPayloadJSON, gotEventKey string
	)
	row = db.QueryRow(`
		SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key
		FROM outbox_events WHERE aggregate_id = ?`, clipID)
	if err := row.Scan(&gotEventType, &gotAggID, &gotAggType, &gotPayloadJSON, &gotEventKey); err != nil {
		t.Fatalf("scan outbox_events row: %v", err)
	}
	if gotEventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("event_type: want %q got %q", outboxevents.EventAssetIndexRequested, gotEventType)
	}
	if gotAggID != clipID {
		t.Errorf("aggregate_id: want %q got %q", clipID, gotAggID)
	}
	if gotAggType != "media_asset" {
		t.Errorf("aggregate_type: want %q got %q", "media_asset", gotAggType)
	}
	if !strings.Contains(gotPayloadJSON, `"schema_version":"asset.index.requested.v1"`) {
		t.Errorf("payload JSON must contain schema_version literal; got %.200s", gotPayloadJSON)
	}
	if !strings.HasPrefix(gotEventKey, "asset.index.requested:youtube:"+clipID+":") {
		t.Errorf("event_key shape: want asset.index.requested:youtube:%s:..., got %q", clipID, gotEventKey)
	}
}

// ── Test 2: idempotent on replay ────────────────────────────────────

// TestClipAtomicWriter_IdempotentOnSameContent pins the canonical
// "replay safe" contract: a second CommitClipAndIndexEvent with
// the SAME clipID + SAME LegacyFileMD5 produces NO new outbox_events row
// (ON CONFLICT(event_key) DO NOTHING) AND no new media_assets row
// (ON CONFLICT(id) DO UPDATE).
func TestClipAtomicWriter_IdempotentOnSameContent(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)

	const clipID = "yt_idem_001_5_30_v1"
	fileHash := sha256Hex("idem-content")
	item := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "idem_001",
		LegacyFileMD5: fileHash,
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Drive:         youtubetypes.ClipAssetDrive{},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 5,
			EndSec:   30,
			Duration: 25,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Idempotent Clip",
			NormalizedGroup: "general",
		},
		PolicyVersion: "v1",
	}
	ctx := context.Background()

	if err := adapter.CommitClipAndIndexEvent(ctx, clipID, item, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("first CommitClipAndIndexEvent: %v", err)
	}
	if err := adapter.CommitClipAndIndexEvent(ctx, clipID, item, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("second CommitClipAndIndexEvent (replay): %v", err)
	}

	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 1 {
		t.Errorf("outbox_events count after idempotent replay: want 1 got %d (ON CONFLICT(event_key) DO NOTHING broken)", outCount)
	}

	var medCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, clipID).Scan(&medCount); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if medCount != 1 {
		t.Errorf("media_assets count after idempotent replay: want 1 got %d", medCount)
	}
}

// ── Test 3: different LegacyFileMD5 → new outbox row ─────────────────────

// TestClipAtomicWriter_DifferentFileHashEmitsSecondRow pins the
// content-hash supersede contract: a different LegacyFileMD5 on the
// SAME clipID produces a different eventKey, so the second INSERT
// goes through with a SECOND outbox_events row. The supersede gate
// downstream (IndexingHandler.source_version check) fires on the
// new row.
func TestClipAtomicWriter_DifferentFileHashEmitsSecondRow(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)

	const clipID = "yt_supersede_001_10_60_v1"
	itemA := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "supersede_001",
		LegacyFileMD5: sha256Hex("content-A"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.CanonicalClipMetadata{Summary: "Supersede A", NormalizedGroup: "general"},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 10, EndSec: 60, Duration: 50},
		PolicyVersion: "v1",
	}
	itemB := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "supersede_001",
		LegacyFileMD5: sha256Hex("content-B"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.CanonicalClipMetadata{Summary: "Supersede B", NormalizedGroup: "general"},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 10, EndSec: 60, Duration: 50},
		PolicyVersion: "v1",
	}
	ctx := context.Background()

	if err := adapter.CommitClipAndIndexEvent(ctx, clipID, itemA, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("first call (content-A): %v", err)
	}
	if err := adapter.CommitClipAndIndexEvent(ctx, clipID, itemB, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("second call (content-B): %v", err)
	}

	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 2 {
		t.Errorf("outbox_events count after different content: want 2 got %d", outCount)
	}

	// Sanity: two distinct event_keys.
	rows, err := db.Query(`SELECT event_key FROM outbox_events WHERE aggregate_id = ? ORDER BY id ASC`, clipID)
	if err != nil {
		t.Fatalf("query event_keys: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan event_key: %v", err)
		}
		keys = append(keys, k)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 distinct keys, got %v", keys)
	}
	if keys[0] == keys[1] {
		t.Errorf("different LegacyFileMD5 MUST produce different event_keys; both rows had %q", keys[0])
	}
}

// ── Test 4: closed writer DB → error, no panic ─────────────────────

// ── Test 5: terminal conflict returns typed error ───────────────────

// TestClipAtomicWriter_TerminalConflictReturnsError verifies the
// audit 2026-07-03 BLOCKER #4 closure: when an existing dead_letter
// or superseded outbox row blocks the INSERT (same event_key),
// the writer must return ErrOutboxTerminalConflict instead of the
// pre-closure silent-success nil.
func TestClipAtomicWriter_TerminalConflictReturnsError(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)

	const clipID = "yt_terminal_001_10_60_v1"
	fileHash := sha256Hex("terminal-conflict-content")
	item := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "terminal_001",
		LegacyFileMD5: fileHash,
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Drive:         youtubetypes.ClipAssetDrive{},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 10,
			EndSec:   60,
			Duration: 50,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Terminal Conflict Probe",
			NormalizedGroup: "general",
		},
		PolicyVersion: "v1",
	}
	ctx := context.Background()

	// First call: normal write succeeds (1 row each).
	if err := adapter.CommitClipAndIndexEvent(ctx, clipID, item, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("first call (normal): %v", err)
	}

	// Manually mark the outbox row as dead_letter to simulate a
	// terminal state from a prior indexing run.
	_, err := db.Exec(`UPDATE outbox_events SET status = 'dead_letter' WHERE aggregate_id = ?`, clipID)
	if err != nil {
		t.Fatalf("seed dead_letter row: %v", err)
	}

	// Second call: same LegacyFileMD5 → same sourceVersion → same eventKey.
	// ON CONFLICT(event_key) DO NOTHING suppresses the INSERT;
	// the existing row is dead_letter (terminal). BLOCKER #4 closure:
	// the writer must return ErrOutboxTerminalConflict.
	err = adapter.CommitClipAndIndexEvent(ctx, clipID, item, canonicalEnvelopePayload())
	if err == nil {
		t.Fatal("BLOCKER #4: second call with terminal row must return error (pre-closure returned nil with 'processed')")
	}
	if !errors.Is(err, youtubeports.ErrOutboxTerminalConflict) {
		t.Errorf("BLOCKER #4: error must wrap ErrOutboxTerminalConflict; got %v", err)
	}

	// Verify the asset row is still there (it was committed in the first call;
	// the second call's UPSERT is ON CONFLICT(id) DO UPDATE — idempotent).
	var medCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, clipID).Scan(&medCount); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if medCount != 1 {
		t.Errorf("media_assets must still have 1 row after terminal conflict; got %d", medCount)
	}

	// Verify no NEW outbox row was created (terminal suppression confirmed).
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND status != 'dead_letter'`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count non-terminal outbox: %v", err)
	}
	if outCount != 0 {
		t.Errorf("BLOCKER #4: no new outbox row must be created on terminal conflict; got %d pending rows", outCount)
	}
}

// TestClipAtomicWriter_ClosedWriterDBReturnsError pins the
// minimum-viable fail-closed posture for the adapter: a closed
// writer DB causes BeginTx to fail; the adapter must surface a
// wrapped error and MUST NOT panic.
//
// A genuine in-tx rollback test (forcing UPSERT-or-Enqueue to fail
// inside a live tx) is deferred to Commit 4 — the metadata atomicity
// pass that will introduce a typed Enqueuer port interface on the
// writer, so the test can inject a stub that returns an error from
// Enqueue without closing any DB. The current concrete
// *outboxevents.Repository receiver precludes a clean stub injection
// at Commit 1 scope (AGENTS.md "Simplicity & Minimalism"). The atomic
// invariant is exercised indirectly by Test 1 (both rows committed
// in one tx) and Test 2 (idempotent ON CONFLICT collapse).
func TestClipAtomicWriter_ClosedWriterDBReturnsError(t *testing.T) {
	dbWriter := newAtomicWriterDB(t)
	// Fresh in-memory outbox DB. The outbox repo is constructed
	// with a valid handle but the writer's tx fails first on the
	// closed writer DB, so the outbox is never touched.
	dbOutbox, openErr := sql.Open("sqlite3", ":memory:")
	if openErr != nil {
		t.Fatalf("open in-memory outbox db: %v", openErr)
	}
	t.Cleanup(func() { _ = dbOutbox.Close() })
	box := outboxevents.NewRepository(dbOutbox)

	// Close the writer DB BEFORE constructing the adapter so any
	// tx attempt (BeginTx or ExecContext on the underlying conn)
	// fails. The test does NOT verify media_assets count: the
	// closed conn can't be re-queried cleanly. The fail-closed
	// posture is "no panic + wrapped error"; both are asserted.
	require.NoError(t, dbWriter.Close(), "precondition: writer DB must close cleanly")

	adapter := NewSQLiteMediaCommitter(dbWriter, box, testRegistryTxWriter{}, nil)

	const clipID = "yt_closed_db_001_10_60_v1"
	item := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "closed_db_001",
		LegacyFileMD5: sha256Hex("closed-db-content"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.CanonicalClipMetadata{Summary: "Closed DB Probe", NormalizedGroup: "general"},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 10, EndSec: 60, Duration: 50},
		PolicyVersion: "v1",
	}

	err := adapter.CommitClipAndIndexEvent(context.Background(), clipID, item, canonicalEnvelopePayload())
	require.Error(t, err, "CommitClipAndIndexEvent must return error when writer DB is closed (P0 #3 fail-closed posture; no silent success)")
	require.NotEmpty(t, err.Error(), "error must carry a message for operator diagnosis")
}

// ── Test 6: PR-YT-DOD-7 metadata_json completeness ───────────────────

// TestClipMetadataWriter_DoD7_MetadataJSONCompleteness pins the
// canonical PR-YT-DOD-7 contract: after UpdateClipMetadataAndRequestIndex,
// the media_assets.metadata_json column MUST contain all 16 required
// fields per the DoD 7 specification:
//
//	source_url, source_provider, video_id, clip_start_sec,
//	clip_end_sec, clip_duration_sec, title, summary, topics,
//	speakers, mentioned_people, hook, normalized_group,
//	policy_version, drive_path, content_hash
//
// [NOTE: The test lives in clip_atomic_writer_test.go (same package)
// because it exercises the ClipMetadataWriterAdapter which shares
// the assets package with ClipAtomicWriterAdapter. The test DB
// schema includes all columns needed by both writers.]
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion reads from the
// real SQLite row AFTER the writer commits — no in-memory stubs.
func TestClipMetadataWriter_DoD7_MetadataJSONCompleteness(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	atomicWriter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)
	metaWriter := NewClipMetadataWriterAdapter(db, box, nil)

	const clipID = "yt_vdC5GXxS-qU_146_155_v1"
	ctx := context.Background()

	// ── Step 1: Write the initial media_assets row via ClipAtomicWriter ──
	fileHash := sha256Hex("broner-pacquiao-146-155")
	asset := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "vdC5GXxS-qU",
		LegacyFileMD5: fileHash,
		LocalPath:     "/tmp/clips/" + clipID + ".mp4",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder_broner",
			FolderPath:  "youtube/vdC5GXxS-qU",
			FileID:      "drive_broner_001",
			WebViewLink: "https://drive.google.com/file/d/drive_broner_001/view",
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 146,
			EndSec:   155,
			Duration: 9,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Broner urla a Pacquiao: Pensa a me, non a Floyd!",
			NormalizedGroup: "boxing",
		},
		PolicyVersion: "v1",
	}
	if err := atomicWriter.CommitClipAndIndexEvent(ctx, clipID, asset, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("Step 1 ClipAtomicWriter: %v", err)
	}

	// ── Step 2: Enrich with full metadata via ClipMetadataWriter ──
	meta := youtubetypes.CanonicalClipMetadata{
		ClipID:          clipID,
		AssetID:         clipID,
		SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		SourceProvider:  "youtube",
		VideoID:         "vdC5GXxS-qU",
		Title:           "Sfuriata di Broner contro Pacquiao",
		Summary:         "Broner urla a Pacquiao: Pensa a me, non a Floyd!",
		Topics:          []string{"boxing", "trash talk", "press conference"},
		Speakers:        []string{"Adrien Broner", "Manny Pacquiao"},
		MentionedPeople: []string{"Floyd Mayweather"},
		Hook:            "Ti sto per spaccare il culo, non preoccuparti di Floyd! Pensa a me!",
		NormalizedGroup: "boxing",
		ClipStartSec:    146,
		ClipEndSec:      155,
		ClipDurationSec: 9,
		PolicyVersion:   "v1",
		DrivePath:       "https://drive.google.com/file/d/drive_broner_001/view",
		ContentHash:     fileHash,
		SourceVersion:   fileHash,
		TranscriptPath:  "/tmp/transcripts/vdC5GXxS-qU.vtt",
		QualityScore:    0.85,
	}
	if err := metaWriter.UpdateClipMetadataAndRequestIndex(ctx, clipID, meta); err != nil {
		t.Fatalf("Step 2 ClipMetadataWriter: %v", err)
	}

	// ── Step 3: Read back metadata_json and verify all 16 DoD 7 fields ──
	var metadataJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = ?`, clipID).Scan(&metadataJSON); err != nil {
		t.Fatalf("read metadata_json: %v", err)
	}

	// Define the 16 required DoD 7 keys and their expected Go values.
	type fieldCheck struct {
		jsonKey string
		// wantJSON is the expected JSON fragment that appears in metadata_json.
		// We use strings.Contains on the raw JSON to avoid json.Unmarshal
		// coupling the test to CanonicalClipMetadata's struct tags.
		wantJSON string
		desc     string
	}
	checks := []fieldCheck{
		{jsonKey: "source_url", wantJSON: `"source_url":"https://www.youtube.com/watch?v=vdC5GXxS-qU"`, desc: "DoD 7.1: source_url"},
		{jsonKey: "source_provider", wantJSON: `"source_provider":"youtube"`, desc: "DoD 7.2: source_provider"},
		{jsonKey: "video_id", wantJSON: `"video_id":"vdC5GXxS-qU"`, desc: "DoD 7.3: video_id"},
		{jsonKey: "clip_start_sec", wantJSON: `"clip_start_sec":146`, desc: "DoD 7.4: clip_start_sec"},
		{jsonKey: "clip_end_sec", wantJSON: `"clip_end_sec":155`, desc: "DoD 7.5: clip_end_sec"},
		{jsonKey: "clip_duration_sec", wantJSON: `"clip_duration_sec":9`, desc: "DoD 7.6: clip_duration_sec"},
		{jsonKey: "title", wantJSON: `"title":"Sfuriata di Broner contro Pacquiao"`, desc: "DoD 7.7: title"},
		{jsonKey: "summary", wantJSON: `"summary":"Broner urla a Pacquiao: Pensa a me, non a Floyd!"`, desc: "DoD 7.8: summary"},
		{jsonKey: "topics", wantJSON: `"topics":["boxing","trash talk","press conference"]`, desc: "DoD 7.9: topics"},
		{jsonKey: "speakers", wantJSON: `"speakers":["Adrien Broner","Manny Pacquiao"]`, desc: "DoD 7.10: speakers"},
		{jsonKey: "mentioned_people", wantJSON: `"mentioned_people":["Floyd Mayweather"]`, desc: "DoD 7.11: mentioned_people"},
		{jsonKey: "hook", wantJSON: `"hook":"Ti sto per spaccare il culo, non preoccuparti di Floyd! Pensa a me!"`, desc: "DoD 7.12: hook"},
		{jsonKey: "normalized_group", wantJSON: `"normalized_group":"boxing"`, desc: "DoD 7.13: normalized_group"},
		{jsonKey: "policy_version", wantJSON: `"policy_version":"v1"`, desc: "DoD 7.14: policy_version"},
		{jsonKey: "drive_path", wantJSON: `"drive_path":"https://drive.google.com/file/d/drive_broner_001/view"`, desc: "DoD 7.15: drive_path"},
		{jsonKey: "content_hash", wantJSON: `"content_hash":"` + fileHash + `"`, desc: "DoD 7.16: content_hash"},
	}

	for _, c := range checks {
		if !strings.Contains(metadataJSON, c.wantJSON) {
			t.Errorf("%s: metadata_json MISSING expected fragment %s\ngot: %s", c.desc, c.wantJSON, metadataJSON)
		}
	}

	// ── Sanity checks: forbidden patterns (must NOT be present) ──
	forbidden := []string{
		`"source_provider":""`,
		`"video_id":""`,
		`"drive_path":""`,
		`"content_hash":""`,
	}
	for _, fb := range forbidden {
		if strings.Contains(metadataJSON, fb) {
			t.Errorf("metadata_json contains forbidden empty-value pattern: %s", fb)
		}
	}
}

// ── Test 7: PR-YT-DOD-7-METADATA-JSON-AUDIT — exactly 14 required fields ──

// TestClipMetadataWriter_MetadataJSON_AllRequiredFields pins the
// PR-YT-DOD-7-METADATA-JSON-AUDIT contract (user-spec 2026-07-08):
// the canonical ClipMetadataWriterAdapter MUST populate the
// metadata_json column with exactly 14 required semantic fields per
// the user-spec field list. Tighter than Test 6 (16 fields) — this
// is the audit shortlist for canonical producer-data compliance.
//
// The 14 required fields (canonical producer-data shortlist):
//
//	source_url, source_provider, video_id, clip_start_sec,
//	clip_end_sec, clip_duration_sec, title, summary, topics,
//	speakers, mentioned_people, hook, normalized_group,
//	policy_version
//
// Notable exclusions (NOT asserted here — see Test 6 for 16-field
// coverage): drive_path + content_hash (best-effort enrichments).
func TestClipMetadataWriter_MetadataJSON_AllRequiredFields(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	atomicWriter := NewSQLiteMediaCommitter(db, box, testRegistryTxWriter{}, nil)
	metaWriter := NewClipMetadataWriterAdapter(db, box, nil)

	const clipID = "yt_audit_shortlist_146_155_v1"
	ctx := context.Background()

	// Step 1: write initial media_assets row via ClipAtomicWriter.
	fileHash := sha256Hex("audit-shortlist-broner-pacquiao")
	asset := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "vdC5GXxS-qU",
		LegacyFileMD5: fileHash,
		LocalPath:     "/tmp/clips/" + clipID + ".mp4",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder_audit",
			FolderPath:  "youtube/vdC5GXxS-qU",
			FileID:      "drive_audit_001",
			WebViewLink: "https://drive.google.com/file/d/drive_audit_001/view",
		},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 146, EndSec: 155, Duration: 9},
		Metadata:      youtubetypes.CanonicalClipMetadata{Summary: "Broner insult Pacquiao", NormalizedGroup: "boxing"},
		PolicyVersion: "v1",
	}
	if err := atomicWriter.CommitClipAndIndexEvent(ctx, clipID, asset, canonicalEnvelopePayload()); err != nil {
		t.Fatalf("Step 1 ClipAtomicWriter: %v", err)
	}

	// Step 2: enrich with full metadata. DrivePath + ContentHash
	// intentionally left zero so this test does NOT cover the
	// broader 16-field DoD-7 surface (Test 6 owns that scope).
	meta := youtubetypes.CanonicalClipMetadata{
		ClipID:          clipID,
		AssetID:         clipID,
		SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		SourceProvider:  "youtube",
		VideoID:         "vdC5GXxS-qU",
		Title:           "Sfuriata di Broner contro Pacquiao",
		Summary:         "Broner insult Pacquiao, then lands leather on him.",
		Topics:          []string{"boxing", "trash talk", "press conference"},
		Speakers:        []string{"Adrien Broner", "Manny Pacquiao"},
		MentionedPeople: []string{"Floyd Mayweather"},
		Hook:            "Ti sto per spaccare il culo, non preoccuparti di Floyd! Pensa a me!",
		NormalizedGroup: "boxing",
		ClipStartSec:    146,
		ClipEndSec:      155,
		ClipDurationSec: 9,
		PolicyVersion:   "v1",
	}
	if err := metaWriter.UpdateClipMetadataAndRequestIndex(ctx, clipID, meta); err != nil {
		t.Fatalf("Step 2 ClipMetadataWriter: %v", err)
	}

	// Step 3: read back metadata_json + parse.
	var metadataJSON string
	if err := db.QueryRow(`SELECT metadata_json FROM media_assets WHERE id = ?`, clipID).Scan(&metadataJSON); err != nil {
		t.Fatalf("read metadata_json: %v", err)
	}

	// Decode the JSON envelope into a generic map so the test is
	// robust against Map-iteration-order, whitespace, and quoting
	// variations in the underlying JSON encoder output.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &parsed); err != nil {
		t.Fatalf("metadata_json is not valid JSON: %v\nraw: %s", err, metadataJSON)
	}

	// required14: the canonical user-spec shortlist. Assert each field
	// is present AND has a non-empty value (godlike/07 NO-FAKE-AVAILABILITY).
	type fieldCheck struct {
		key  string
		desc string
	}
	required14 := []fieldCheck{
		{"source_url", "Audit 1: source_url (canonical traceability)"},
		{"source_provider", "Audit 2: source_provider (canonical producer label)"},
		{"video_id", "Audit 3: video_id (canonical identifier)"},
		{"clip_start_sec", "Audit 4: clip_start_sec (canonical second-precise start)"},
		{"clip_end_sec", "Audit 5: clip_end_sec (canonical second-precise end)"},
		{"clip_duration_sec", "Audit 6: clip_duration_sec (canonical duration)"},
		{"title", "Audit 7: title (canonical narrative headline)"},
		{"summary", "Audit 8: summary (canonical content description)"},
		{"topics", "Audit 9: topics (canonical BM25 channel)"},
		{"speakers", "Audit 10: speakers (canonical attribution list)"},
		{"mentioned_people", "Audit 11: mentioned_people (canonical person refs)"},
		{"hook", "Audit 12: hook (canonical attention-grabber)"},
		{"normalized_group", "Audit 13: normalized_group (canonical routing tag)"},
		{"policy_version", "Audit 14: policy_version (canonical event-version seal)"},
	}

	for _, c := range required14 {
		val, ok := parsed[c.key]
		if !ok {
			t.Errorf("%s: metadata_json MISSING key %q (not present in JSON map)\nraw: %s", c.desc, c.key, metadataJSON)
			continue
		}
		if isEmpty(val) {
			t.Errorf("%s: metadata_json has empty value for %q — godlike/07 NO-FAKE-AVAILABILITY forbids empty canonical fields\nraw: %s", c.desc, c.key, metadataJSON)
		}
	}

	// Forbidden empties: assert NONE of the 14 required text keys
	// serialized as empty strings (defense-in-depth on top of the
	// isEmpty check above).
	for _, key := range []string{"source_url", "source_provider", "video_id", "normalized_group", "policy_version"} {
		if s, ok := parsed[key].(string); ok && s == "" {
			t.Errorf("metadata_json has forbidden empty string for canonical key %q", key)
		}
	}
}

// isEmpty reports whether a generic JSON-decoded value is morallyempty
// (nil, empty string, empty slice, zero number). The metadata_json
// payload is expected to be a non-empty primitive-or-slice for the
// 14 required canonical producer-data fields.
func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case float64: // JSON numbers decode as float64
		return x == 0
	}
	return false
}
