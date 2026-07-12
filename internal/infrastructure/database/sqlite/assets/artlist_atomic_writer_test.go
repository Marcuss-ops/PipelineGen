// Package assets — artlist_atomic_writer_test.go (Fase 11 / Commit 1):
// focused unit tests for the artlist publish single-tx wrap.
//
// Coverage matrix (each test pins ONE invariant):
//
//  1. TestCommitArtlistPublishTx_HappyPath_BothRowsPersist
//     → media_assets row updated to PUBLISHED + 1 outbox row enqueued
//  2. TestCommitArtlistPublishTx_RollbackOnMissingAssetRow
//     → zero media_assets changes + zero outbox rows (tx rolled back)
//  3. TestCommitArtlistPublishTx_RollbackOnOutboxFailure
//     → when outbox INSERT fails, media_assets change is reverted
//  4. TestCommitArtlistPublishTx_IdempotencyDedup
//     → calling twice with same event_key collapses to 1 outbox row
//  5. TestCommitArtlistPublishTx_Validation_EmptyFieldsFailClosed
//     → every empty required field surfaces a typed error BEFORE tx
//  6. TestBuildArtlistPublishRequestV1_ShapeContract
//     → pure-data test for the envelope builder (no DB)
//  7. TestNewArtlistPublishTxAdapter_PanicsOnNil
//     → godlike/07 fail-closed: nil db or box is a panic, not a runtime
//     nil-deref at first CommitArtlistPublishTx call
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts the FULL
// state (row counts + field values), not just "no error". A test
// that produces a half-applied write is itself a fake-availability
// regression — the wrap's only reason to exist is the
// "Mai PUBLISHED senza outbox" invariant.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Test helpers ─────────────────────────────────────────────────────

// artlistTestDB creates an in-memory SQLite with the canonical
// media_assets + outbox_events schema used by the wrap. The
// schema is a minimal SUBSET of the production migration
// (migrations/sqlite/092_create_outbox_events.sql + the media_assets
// migration) covering only the columns the wrap reads or writes.
// Tests that need extra columns add them inline.
func artlistTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// :memory: with shared cache so multiple connections see the
	// same DB (the outbox events writer may use a different
	// connection from the BEGIN-tx connection). The canonical
	// pattern is `file::memory:?cache=shared` with a unique
	// DSN per test to avoid cross-test contamination.
	dsn := "file:" + filepath.Join(t.TempDir(), "artlist_atomic_test.db") + "?cache=shared&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Force a single connection so :memory: is consistent across
	// operations (the production code uses a pool, but for the
	// test the single-connection invariant matches the tx-wrap
	// semantics we are pinning).
	db.SetMaxOpenConns(1)

	// media_assets — minimal subset covering the wrap's writes.
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'DISCOVERED',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);`); err != nil {
		t.Fatalf("CREATE TABLE media_assets: %v", err)
	}

	// outbox_events — matches the canonical migration's
	// column subset the wrap reads or writes. The Enqueue
	// method writes to (event_type, aggregate_id,
	// aggregate_type, payload_json, event_key, created_at,
	// updated_at); the other columns have defaults that the
	// Enqueue does NOT touch, so the test schema can omit them.
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL DEFAULT '',
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);`); err != nil {
		t.Fatalf("CREATE TABLE outbox_events: %v", err)
	}

	return db
}

// insertArtlistStagingRow pre-inserts a media_assets row in the
// DISCOVERED lifecycle so the wrap's UPDATE can promote it to
// PUBLISHED. Mirrors the staging-row pattern used by the
// canonical drive publisher.
func insertArtlistStagingRow(t *testing.T, db *sql.DB, assetID string) {
	t.Helper()
	_, err := db.Exec(`
INSERT INTO media_assets (id, source, lifecycle_state, created_at, updated_at)
VALUES (?, 'artlist', 'DISCOVERED', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
`, assetID)
	if err != nil {
		t.Fatalf("insert staging row %q: %v", assetID, err)
	}
}

// newArtlistTestAdapter returns an adapter wired with a fixed clock
// so tests are deterministic. Uses zap.NewNop() (no log spam).
func newArtlistTestAdapter(t *testing.T, db *sql.DB) *artlistPublishTxAdapter {
	t.Helper()
	fixed := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	box := outboxevents.NewRepository(db)
	a := newArtlistPublishTxAdapter(db, box, zap.NewNop())
	a.now = func() time.Time { return fixed }
	return a
}

// validArtlistCommand returns a fully-populated ArtlistPublishCommand
// the tests can mutate per-case.
func validArtlistCommand(assetID string) ArtlistPublishCommand {
	return ArtlistPublishCommand{
		AssetID:       assetID,
		AssetVersion:  "v1",
		AssetLocation: "/data/artlist/staging/" + assetID + ".mp4",
		Rendition:     "1080p",
		DriveFileID:   "drive-file-" + assetID,
		DriveLink:     "https://drive.google.com/file/d/drive-file-" + assetID + "/view",
		DownloadLink:  "https://drive.google.com/uc?export=download&id=drive-file-" + assetID,
		FileHash:      "sha256:" + assetID + "-hash",
		SourceVersion: "sha256:" + assetID + "-hash",
	}
}

// countOutboxRowsForAsset returns the number of outbox_events
// rows where aggregate_id = assetID. Used to assert the
// "exactly 1 outbox row per commit" invariant.
func countOutboxRowsForAsset(t *testing.T, db *sql.DB, assetID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID).Scan(&n); err != nil {
		t.Fatalf("count outbox rows for asset %q: %v", assetID, err)
	}
	return n
}

// getMediaAssetRow fetches the full media_assets row for
// assetID. Returns the column values; tests assert field-by-field
// for the post-commit state.
type mediaAssetRow struct {
	Source         string
	AssetVersion   string
	AssetLocation  string
	Rendition      string
	DriveFileID    string
	DriveLink      string
	DownloadLink   string
	FileHash       string
	SourceVersion  string
	LifecycleState string
	CreatedAt      string
	UpdatedAt      string
}

func getMediaAssetRow(t *testing.T, db *sql.DB, assetID string) mediaAssetRow {
	t.Helper()
	var r mediaAssetRow
	err := db.QueryRow(`
SELECT source, asset_version, asset_location, rendition,
       drive_file_id, drive_link, download_link,
       file_hash, source_version, lifecycle_state,
       created_at, updated_at
FROM media_assets WHERE id = ?`, assetID).Scan(
		&r.Source, &r.AssetVersion, &r.AssetLocation, &r.Rendition,
		&r.DriveFileID, &r.DriveLink, &r.DownloadLink,
		&r.FileHash, &r.SourceVersion, &r.LifecycleState,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("get media_assets row %q: %v", assetID, err)
	}
	return r
}

// ── Tests ────────────────────────────────────────────────────────────

// TestCommitArtlistPublishTx_HappyPath_BothRowsPersist pins the
// canonical "all green" commit. Asserts:
//
//   - media_assets row updated to lifecycle_state='PUBLISHED' with
//     the user-spec field set populated.
//   - exactly 1 outbox_events row with event_type='asset.index.requested.v1',
//     event_key deterministic, payload_json well-formed.
//   - created_at is preserved (staging-row timestamp survives).
//   - updated_at advances to the adapter's clock.
func TestCommitArtlistPublishTx_HappyPath_BothRowsPersist(t *testing.T) {
	db := artlistTestDB(t)
	insertArtlistStagingRow(t, db, "ast-001")
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-001")

	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("CommitArtlistPublishTx: unexpected error: %v", err)
	}

	// media_assets row state
	row := getMediaAssetRow(t, db, "ast-001")
	if row.LifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED", row.LifecycleState)
	}
	if row.AssetVersion != cmd.AssetVersion {
		t.Errorf("asset_version = %q, want %q", row.AssetVersion, cmd.AssetVersion)
	}
	if row.AssetLocation != cmd.AssetLocation {
		t.Errorf("asset_location = %q, want %q", row.AssetLocation, cmd.AssetLocation)
	}
	if row.Rendition != cmd.Rendition {
		t.Errorf("rendition = %q, want %q", row.Rendition, cmd.Rendition)
	}
	if row.DriveFileID != cmd.DriveFileID {
		t.Errorf("drive_file_id = %q, want %q", row.DriveFileID, cmd.DriveFileID)
	}
	if row.DriveLink != cmd.DriveLink {
		t.Errorf("drive_link = %q, want %q", row.DriveLink, cmd.DriveLink)
	}
	if row.DownloadLink != cmd.DownloadLink {
		t.Errorf("download_link = %q, want %q", row.DownloadLink, cmd.DownloadLink)
	}
	if row.FileHash != cmd.FileHash {
		t.Errorf("file_hash = %q, want %q", row.FileHash, cmd.FileHash)
	}
	if row.SourceVersion != cmd.SourceVersion {
		t.Errorf("source_version = %q, want %q", row.SourceVersion, cmd.SourceVersion)
	}
	if row.Source != "artlist" {
		t.Errorf("source = %q, want %q", row.Source, "artlist")
	}
	// created_at is preserved (staging row had 2026-01-01...)
	if !strings.HasPrefix(row.CreatedAt, "2026-01-01") {
		t.Errorf("created_at = %q, want preserved staging timestamp (2026-01-01...)", row.CreatedAt)
	}
	// updated_at advances to the adapter's fixed clock
	if !strings.HasPrefix(row.UpdatedAt, "2026-07-12") {
		t.Errorf("updated_at = %q, want adapter's clock (2026-07-12...)", row.UpdatedAt)
	}

	// outbox row state
	if got := countOutboxRowsForAsset(t, db, "ast-001"); got != 1 {
		t.Errorf("outbox rows for ast-001 = %d, want 1", got)
	}
	var eventType, eventKey, payloadStr, aggregateID string
	if err := db.QueryRow(`SELECT event_type, event_key, payload_json, aggregate_id FROM outbox_events WHERE aggregate_id = ?`, "ast-001").Scan(
		&eventType, &eventKey, &payloadStr, &aggregateID,
	); err != nil {
		t.Fatalf("query outbox row: %v", err)
	}
	if eventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("event_type = %q, want %q", eventType, outboxevents.EventAssetIndexRequested)
	}
	if aggregateID != "ast-001" {
		t.Errorf("aggregate_id = %q, want ast-001", aggregateID)
	}
	// event_key is deterministic — idempotency.OutboxKey shape:
	//   "asset.index.requested.v1:artlist:<assetID>:<sourceVersion>"
	wantPrefix := outboxevents.EventAssetIndexRequested + ":artlist:ast-001:"
	if !strings.HasPrefix(eventKey, wantPrefix) {
		t.Errorf("event_key = %q, want prefix %q", eventKey, wantPrefix)
	}
	if !strings.HasSuffix(eventKey, ":"+cmd.SourceVersion) {
		t.Errorf("event_key = %q, want suffix :%q", eventKey, cmd.SourceVersion)
	}
	// payload shape — user spec requires: source=artlist, media_type=video,
	// lifecycle=PUBLISHED, asset_id, source_version, file_hash, idempotency_key
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("unmarshal payload_json: %v", err)
	}
	if payload["source"] != "artlist" {
		t.Errorf("payload.source = %v, want artlist", payload["source"])
	}
	if payload["media_type"] != "video" {
		t.Errorf("payload.media_type = %v, want video", payload["media_type"])
	}
	if payload["lifecycle_state"] != "PUBLISHED" {
		t.Errorf("payload.lifecycle_state = %v, want PUBLISHED", payload["lifecycle_state"])
	}
	if payload["asset_id"] != "ast-001" {
		t.Errorf("payload.asset_id = %v, want ast-001", payload["asset_id"])
	}
	if payload["source_version"] != cmd.SourceVersion {
		t.Errorf("payload.source_version = %v, want %q", payload["source_version"], cmd.SourceVersion)
	}
	if payload["file_hash"] != cmd.FileHash {
		t.Errorf("payload.file_hash = %v, want %q", payload["file_hash"], cmd.FileHash)
	}
	if payload["idempotency_key"] != eventKey {
		t.Errorf("payload.idempotency_key = %v, want %q", payload["idempotency_key"], eventKey)
	}
}

// TestCommitArtlistPublishTx_RollbackOnMissingAssetRow pins the
// "Mai PUBLISHED senza outbox" invariant under the
// staging-row-missing case. When the UPDATE returns
// RowsAffected=0, the defer Rollback reverts any partial
// state. Asserts:
//
//   - commit returns a non-nil error wrapping errArtlistAssetRowNotFound.
//   - no outbox row is created (the tx rolled back before the
//     enqueue step).
//   - the staging row, if it existed, is unchanged.
func TestCommitArtlistPublishTx_RollbackOnMissingAssetRow(t *testing.T) {
	db := artlistTestDB(t)
	// NOTE: do NOT insert a staging row — the wrap must fail closed
	// when the row is missing (the caller's job to pre-stage).
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-missing")

	err := a.CommitArtlistPublishTx(context.Background(), cmd)
	if err == nil {
		t.Fatal("CommitArtlistPublishTx: expected error for missing staging row, got nil")
	}
	if !errors.Is(err, errArtlistAssetRowNotFound) {
		t.Errorf("expected errArtlistAssetRowNotFound in chain, got: %v", err)
	}
	// No outbox row should exist (tx rolled back before enqueue)
	if got := countOutboxRowsForAsset(t, db, "ast-missing"); got != 0 {
		t.Errorf("outbox rows for ast-missing = %d, want 0 (tx rolled back)", got)
	}
	// No media_assets row was created (the wrap does INSERT, only UPDATE)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, "ast-missing").Scan(&n); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if n != 0 {
		t.Errorf("media_assets row count for ast-missing = %d, want 0", n)
	}
}

// TestCommitArtlistPublishTx_RollbackOnOutboxFailure pins the
// "Mai PUBLISHED senza outbox" invariant under the
// outbox-INSERT-fails case. Asserts:
//
//   - commit returns a non-nil error (the tx rolled back).
//   - the media_assets row is back to its pre-call state
//     (lifecycle_state NOT 'PUBLISHED') — the UPDATE was reverted.
//   - no outbox row exists.
//
// Implementation: we simulate the outbox failure by pointing the
// adapter at a *sql.Tx that fails on ExecContext (via a wrapped
// DB whose outbox_events table has been dropped mid-test).
// Simpler: we drop the outbox_events table after the wrap
// starts, but the wrap opens the tx first. The cleanest
// simulation is to swap the *outboxevents.Repository for a
// wrapper that returns an error on Enqueue. We do this by
// dropping the outbox_events table BEFORE the call — the
// outbox INSERT then fails with "no such table", and the defer
// Rollback reverts the UPDATE.
func TestCommitArtlistPublishTx_RollbackOnOutboxFailure(t *testing.T) {
	db := artlistTestDB(t)
	insertArtlistStagingRow(t, db, "ast-002")
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-002")

	// Simulate outbox failure by dropping the outbox_events
	// table BEFORE the call. The orchestrator's BeginTx succeeds
	// (media_assets is fine), the UPDATE succeeds, then the
	// outbox INSERT fails with "no such table: outbox_events"
	// and the defer Rollback reverts the UPDATE.
	if _, err := db.Exec(`DROP TABLE outbox_events`); err != nil {
		t.Fatalf("DROP TABLE outbox_events: %v", err)
	}

	err := a.CommitArtlistPublishTx(context.Background(), cmd)
	if err == nil {
		t.Fatal("CommitArtlistPublishTx: expected error when outbox_events table missing, got nil")
	}
	// media_assets row should still be at the pre-call state —
	// the defer Rollback reverted the UPDATE.
	row := getMediaAssetRow(t, db, "ast-002")
	if row.LifecycleState == "PUBLISHED" {
		t.Errorf("lifecycle_state = PUBLISHED after rollback; want pre-call state (DISCOVERED or '')")
	}
	// The outbox_events table is gone (intentionally dropped above
	// to simulate the INSERT failure), so the
	// countOutboxRowsForAsset helper would itself fail. The
	// "no outbox row exists" invariant is implicit when the
	// table is gone — the absence of the table is a strictly
	// stronger condition than the absence of rows.
}

// TestCommitArtlistPublishTx_IdempotencyDedup pins the
// replay-safety invariant. Calling the wrap twice with the
// SAME (assetID, sourceVersion) tuple must produce exactly ONE
// outbox row (the second INSERT is squelched by ON CONFLICT
// DO NOTHING on event_key). The media_assets row is UPDATEd
// twice but converges to the same PUBLISHED state — the second
// UPDATE is a no-op for the user-visible state.
//
// The first call uses a fresh staging row; the second call
// re-uses the same staging row. The wrap does not require
// the row to be in DISCOVERED — it can be called on an
// already-PUBLISHED row (idempotent re-publish).
func TestCommitArtlistPublishTx_IdempotencyDedup(t *testing.T) {
	db := artlistTestDB(t)
	insertArtlistStagingRow(t, db, "ast-003")
	a := newArtlistTestAdapter(t, db)
	cmd := validArtlistCommand("ast-003")

	// First call
	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("first CommitArtlistPublishTx: %v", err)
	}
	if got := countOutboxRowsForAsset(t, db, "ast-003"); got != 1 {
		t.Fatalf("outbox rows after first commit = %d, want 1", got)
	}

	// Second call with the SAME (assetID, sourceVersion) — the
	// wrap should be idempotent: same event_key, the outbox
	// INSERT collides on UNIQUE(event_key) and the conflict
	// path in the canonical outbox enqueue returns "not
	// inserted" (the row already exists, status=pending or
	// later).
	if err := a.CommitArtlistPublishTx(context.Background(), cmd); err != nil {
		t.Fatalf("second CommitArtlistPublishTx: %v", err)
	}
	if got := countOutboxRowsForAsset(t, db, "ast-003"); got != 1 {
		t.Errorf("outbox rows after second commit = %d, want 1 (idempotent — same event_key)", got)
	}

	// media_assets is still PUBLISHED (converged state).
	row := getMediaAssetRow(t, db, "ast-003")
	if row.LifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED (converged after replay)", row.LifecycleState)
	}
}

// TestCommitArtlistPublishTx_Validation_EmptyFieldsFailClosed
// pins the godlike/07 no-fake-availability contract: every
// empty required field surfaces a typed error BEFORE the tx
// opens (validateArtlistPublishCommand). The wrap never
// half-applies a write because of a missing field.
func TestCommitArtlistPublishTx_Validation_EmptyFieldsFailClosed(t *testing.T) {
	db := artlistTestDB(t)
	a := newArtlistTestAdapter(t, db)
	base := validArtlistCommand("ast-validate")

	cases := []struct {
		name   string
		mutate func(*ArtlistPublishCommand)
		want   error
	}{
		{"empty AssetID", func(c *ArtlistPublishCommand) { c.AssetID = "" }, errArtlistEmptyAssetID},
		{"empty AssetVersion", func(c *ArtlistPublishCommand) { c.AssetVersion = "" }, errArtlistEmptyAssetVersion},
		{"empty AssetLocation", func(c *ArtlistPublishCommand) { c.AssetLocation = "" }, errArtlistEmptyAssetLocation},
		{"empty Rendition", func(c *ArtlistPublishCommand) { c.Rendition = "" }, errArtlistEmptyRendition},
		{"empty DriveFileID", func(c *ArtlistPublishCommand) { c.DriveFileID = "" }, errArtlistEmptyDriveFileID},
		{"empty DriveLink", func(c *ArtlistPublishCommand) { c.DriveLink = "" }, errArtlistEmptyDriveLink},
		{"empty DownloadLink", func(c *ArtlistPublishCommand) { c.DownloadLink = "" }, errArtlistEmptyDownloadLink},
		{"empty FileHash", func(c *ArtlistPublishCommand) { c.FileHash = "" }, errArtlistEmptyFileHash},
		{"empty SourceVersion", func(c *ArtlistPublishCommand) { c.SourceVersion = "" }, errArtlistEmptySourceVersion},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := base
			tc.mutate(&cmd)
			err := a.CommitArtlistPublishTx(context.Background(), cmd)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v in chain, got: %v", tc.want, err)
			}
			// godlike/07: validation runs BEFORE the tx — no
			// outbox row should exist.
			if got := countOutboxRowsForAsset(t, db, cmd.AssetID); got != 0 {
				t.Errorf("outbox rows for %q = %d, want 0 (validation runs pre-tx)", cmd.AssetID, got)
			}
		})
	}
}

// TestBuildArtlistPublishRequestV1_ShapeContract pins the
// envelope builder's pure-data transformation. No DB, no IO.
// Asserts the JSON shape matches the user spec: asset_id,
// source=artlist, media_type=video, file_hash, lifecycle=PUBLISHED,
// source_version, idempotency_key, schema_version,
// event_id, operation, requested_at.
func TestBuildArtlistPublishRequestV1_ShapeContract(t *testing.T) {
	cmd := validArtlistCommand("ast-shape")
	eventKey := "asset.index.requested.v1:artlist:ast-shape:sha256:ast-shape-hash"
	nowStr := "2026-07-12T10:00:00Z"

	p, err := buildArtlistPublishRequestV1(cmd, eventKey, nowStr)
	if err != nil {
		t.Fatalf("buildArtlistPublishRequestV1: %v", err)
	}
	if p.SchemaVersion != "asset.index.requested.v1" {
		t.Errorf("schema_version = %q, want asset.index.requested.v1", p.SchemaVersion)
	}
	if p.AssetID != "ast-shape" {
		t.Errorf("asset_id = %q, want ast-shape", p.AssetID)
	}
	if p.Source != "artlist" {
		t.Errorf("source = %q, want artlist", p.Source)
	}
	if p.MediaType != "video" {
		t.Errorf("media_type = %q, want video", p.MediaType)
	}
	if p.FileHash != cmd.FileHash {
		t.Errorf("file_hash = %q, want %q", p.FileHash, cmd.FileHash)
	}
	if p.LifecycleState != "PUBLISHED" {
		t.Errorf("lifecycle_state = %q, want PUBLISHED", p.LifecycleState)
	}
	if p.IdempotencyKey != eventKey {
		t.Errorf("idempotency_key = %q, want %q", p.IdempotencyKey, eventKey)
	}
	if p.RequestedAt != nowStr {
		t.Errorf("requested_at = %q, want %q", p.RequestedAt, nowStr)
	}
	if p.Operation != "publish" {
		t.Errorf("operation = %q, want publish", p.Operation)
	}
	if p.EventID == "" {
		t.Errorf("event_id is empty (uuid.NewString should produce non-empty)")
	}
	// Round-trip via JSON to ensure the struct tags are
	// well-formed.
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, k := range []string{
		"schema_version", "event_id", "asset_id", "operation",
		"source_version", "source", "media_type", "file_hash",
		"lifecycle_state", "idempotency_key", "requested_at",
	} {
		if _, ok := back[k]; !ok {
			t.Errorf("payload missing key %q (got: %v)", k, back)
		}
	}

	// Empty eventKey / nowStr → typed error
	if _, err := buildArtlistPublishRequestV1(cmd, "", nowStr); !errors.Is(err, errArtlistEmptyEventKey) {
		t.Errorf("empty eventKey: want errArtlistEmptyEventKey, got %v", err)
	}
	if _, err := buildArtlistPublishRequestV1(cmd, eventKey, ""); !errors.Is(err, errArtlistEmptyRequestedAt) {
		t.Errorf("empty nowStr: want errArtlistEmptyRequestedAt, got %v", err)
	}
}

// TestNewArtlistPublishTxAdapter_PanicsOnNil pins the
// godlike/07 fail-closed contract for the composition root:
// a nil db or nil outbox.Repository surfaces as a panic
// at construction time, not as a runtime nil-deref at the
// first CommitArtlistPublishTx call.
func TestNewArtlistPublishTxAdapter_PanicsOnNil(t *testing.T) {
	box := outboxevents.NewRepository(artlistTestDB(t))

	t.Run("nil db panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected panic on nil db, got none")
			}
		}()
		_ = NewArtlistPublishTxAdapter(nil, box, zap.NewNop())
	})
	t.Run("nil box panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected panic on nil box, got none")
			}
		}()
		_ = NewArtlistPublishTxAdapter(artlistTestDB(t), nil, zap.NewNop())
	})
}

// TestCommitArtlistPublishTx_AdapterNilSafety pins the
// godlike/07 contract: a nil receiver returns a typed error
// rather than panicking (the panic contract is reserved for
// the composition-root-time constructor; the runtime surface
// is nil-safe).
func TestCommitArtlistPublishTx_AdapterNilSafety(t *testing.T) {
	var a *artlistPublishTxAdapter // nil
	cmd := validArtlistCommand("ast-nil")
	err := a.CommitArtlistPublishTx(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error from nil adapter, got nil")
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Errorf("expected 'not wired' in error, got: %v", err)
	}
}
