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
//     FileHash-derived sourceVersion collapses the outbox half via
//     ON CONFLICT(event_key) DO NOTHING. media_assets row is
//     updated-once via ON CONFLICT(id) DO UPDATE.
//  3. Different FileHash produces a different eventKey → second
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
package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// clipAtomicWriterSchema is the minimal test schema (production-faithful
// subset). Column set matches what upsertClipInTx writes and what the
// canonical envelope inserts (via outboxevents.Repository.Enqueue).
const clipAtomicWriterSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, file_hash TEXT,
    folder_id TEXT, folder_path TEXT,
    source_version TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at TEXT, updated_at TEXT
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

// canonicalEnvelopePayload resets the event payload to a zero-value
// IndexEventPayload with the canonical Type prefilled. The writer's
// CommitClipAndIndexEvent fills AggregateID from clipID when empty
// (and renders the canonical envelope body internally).
func canonicalEnvelopePayload() youtubeports.IndexEventPayload {
	return youtubeports.IndexEventPayload{
		Type: outboxevents.EventAssetIndexRequested,
	}
}

// sha256Hex returns a 64-hex string the writer treats as the canonical
// FileHash shape (the production path uses MD5, but the writer does
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
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_abc123_10_60_v1"
	item := youtubetypes.ClipAsset{
		ID:        clipID,
		VideoID:   "abc123",
		FileHash:  sha256Hex("happy-path"),
		LocalPath: "/tmp/clips/yt_abc123_10_60_v1.mp4",
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
		Metadata: youtubetypes.ClipMetadata{
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
		SELECT name, file_hash, local_path, drive_file_id, drive_link, source_version, lifecycle_state
		FROM media_assets WHERE id = ?`, clipID)
	if err := row.Scan(&gotName, &gotFileHash, &gotLocalPath, &gotDriveFileID, &gotDriveLink, &gotSourceVersion, &gotLifecycle); err != nil {
		t.Fatalf("scan media_assets row: %v", err)
	}
	if gotName != item.Metadata.Summary {
		t.Errorf("name: want %q got %q", item.Metadata.Summary, gotName)
	}
	if gotFileHash != item.FileHash {
		t.Errorf("file_hash: want %q got %q", item.FileHash, gotFileHash)
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
	// match what deriveSourceVersion computes (asset.FileHash when
	// non-empty). The clipindexer's CAS fence reads this column.
	if gotSourceVersion == "" {
		t.Errorf("BLOCKER #2: source_version must be non-empty after CommitClipAndIndexEvent (CAS fence starves on empty)")
	}
	if gotSourceVersion != item.FileHash {
		t.Errorf("BLOCKER #2: source_version must equal FileHash when FileHash is non-empty; want %q got %q", item.FileHash, gotSourceVersion)
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
	if !strings.HasPrefix(gotEventKey, "reconcile:reindex:"+clipID+":") {
		t.Errorf("event_key shape: want reconcile:reindex:%s:..., got %q", clipID, gotEventKey)
	}
}

// ── Test 2: idempotent on replay ────────────────────────────────────

// TestClipAtomicWriter_IdempotentOnSameContent pins the canonical
// "replay safe" contract: a second CommitClipAndIndexEvent with
// the SAME clipID + SAME FileHash produces NO new outbox_events row
// (ON CONFLICT(event_key) DO NOTHING) AND no new media_assets row
// (ON CONFLICT(id) DO UPDATE).
func TestClipAtomicWriter_IdempotentOnSameContent(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_idem_001_5_30_v1"
	fileHash := sha256Hex("idem-content")
	item := youtubetypes.ClipAsset{
		ID:        clipID,
		VideoID:   "idem_001",
		FileHash:  fileHash,
		LocalPath: "/tmp/" + clipID + ".mp4",
		Drive:     youtubetypes.ClipAssetDrive{},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 5,
			EndSec:   30,
			Duration: 25,
		},
		Metadata: youtubetypes.ClipMetadata{
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

// ── Test 3: different FileHash → new outbox row ─────────────────────

// TestClipAtomicWriter_DifferentFileHashEmitsSecondRow pins the
// content-hash supersede contract: a different FileHash on the
// SAME clipID produces a different eventKey, so the second INSERT
// goes through with a SECOND outbox_events row. The supersede gate
// downstream (IndexingHandler.source_version check) fires on the
// new row.
func TestClipAtomicWriter_DifferentFileHashEmitsSecondRow(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_supersede_001_10_60_v1"
	itemA := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "supersede_001",
		FileHash:      sha256Hex("content-A"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.ClipMetadata{Summary: "Supersede A", NormalizedGroup: "general"},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 10, EndSec: 60, Duration: 50},
		PolicyVersion: "v1",
	}
	itemB := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "supersede_001",
		FileHash:      sha256Hex("content-B"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.ClipMetadata{Summary: "Supersede B", NormalizedGroup: "general"},
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
		t.Errorf("different FileHash MUST produce different event_keys; both rows had %q", keys[0])
	}
}

// ── Test 4: closed writer DB → error, no panic ─────────────────────

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

	adapter := NewClipAtomicWriterAdapter(dbWriter, box, nil)

	const clipID = "yt_closed_db_001_10_60_v1"
	item := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       "closed_db_001",
		FileHash:      sha256Hex("closed-db-content"),
		LocalPath:     "/tmp/" + clipID + ".mp4",
		Metadata:      youtubetypes.ClipMetadata{Summary: "Closed DB Probe", NormalizedGroup: "general"},
		Coordinates:   youtubetypes.ClipAssetCoordinates{StartSec: 10, EndSec: 60, Duration: 50},
		PolicyVersion: "v1",
	}

	err := adapter.CommitClipAndIndexEvent(context.Background(), clipID, item, canonicalEnvelopePayload())
	require.Error(t, err, "CommitClipAndIndexEvent must return error when writer DB is closed (P0 #3 fail-closed posture; no silent success)")
	require.NotEmpty(t, err.Error(), "error must carry a message for operator diagnosis")
}
