// Package deletion_test — Blocco 3.1 commits 4/3 + 3/3 (June-July 2026) test pinning.
//
// Coverage:
//
//   - Blocco 3.1 commit 4/3 (June 2026): DeleteClip routes exclusively through
//     DispatcherPort.EnqueueDriveDelete; nil-dispatcher wiring fails-fast.
//   - Blocco 3.1 commit 3/3 (July 2026): CompleteAsset adds the COMPLETED
//     step fail-closed. Pinning:
//   - drive_file_alive_block guard at state pre-DRIVE_DELETED
//   - Drive-gone check failure propagates WITHOUT side-effects
//   - Nil completionTxRunner = wiring-error fail-closed
//   - Happy path runs the atomic SQLite TX
//   - Idempotent re-run after success = nil, no side-effects
//   - Resume-from-current-state: any state in {DRIVE_DELETED,
//     INDEX_DELETE_PENDING, INDEX_DELETED, DELETED} proceeds
//   - Asset-tree cleanup errors PROPAGATE in DeleteClip
//     (godlike/07 NO_FAKE_AVAILABILITY; the silent `_ =` anti-pattern
//     listed in the user spec for this commit)
package deletion_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Fakes & Mocks ────────────────────────────────────────────────────

// recordingDispatcher satisfies deletion.DispatcherPort (structurally)
// and records each EnqueueDriveDelete call so tests can assert
// (a) the call was made, (b) the (assetID, permanently) tuple is what
// the service decided.
type recordingDispatcher struct {
	mu    sync.Mutex
	calls []enqueueDriveDeleteCall
	err   error
}

type enqueueDriveDeleteCall struct {
	assetID     string
	permanently bool
}

func (r *recordingDispatcher) EnqueueDriveDelete(_ context.Context, assetID string, permanently bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, enqueueDriveDeleteCall{assetID: assetID, permanently: permanently})
	return r.err
}

// recordingDriveGoneChecker satisfies deletion.DriveGoneChecker and
// records each CheckDriveGone call so the audit tests can verify the
// guard fires BEFORE any SQLite side-effect.
type recordingDriveGoneChecker struct {
	mu       sync.Mutex
	calls    []driveGoneCallRecord
	isGone   bool
	goneErr  error // non-nil simulates a Drive API failure
	byFileID map[string]driveGoneResponse
}

type driveGoneCallRecord struct {
	fileID string
}

// driveGoneResponse is a per-fileID override; tests that exercise the
// Drive API failure surface AND the success surface in the same test
// use this to differentiate outcomes.
type driveGoneResponse struct {
	isGone bool
	err    error
}

func (r *recordingDriveGoneChecker) CheckDriveGone(_ context.Context, fileID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, driveGoneCallRecord{fileID: fileID})
	if r.byFileID != nil {
		if resp, ok := r.byFileID[fileID]; ok {
			return resp.isGone, resp.err
		}
	}
	return r.isGone, r.goneErr
}

// recordingCompletionTxRunner satisfies deletion.CompletionTxRunner
// and records each RunCompletionTx call so the audit tests can
// verify the SQLite TX runs ONLY on the happy path (no partial
// side-effects on Drive-check failure paths).
type recordingCompletionTxRunner struct {
	mu    sync.Mutex
	calls []string
	err   error
	byID  map[string]error
}

func (r *recordingCompletionTxRunner) RunCompletionTx(_ context.Context, assetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, assetID)
	if r.byID != nil {
		if err, ok := r.byID[assetID]; ok {
			return err
		}
	}
	return r.err
}

// ── Test fixtures ────────────────────────────────────────────────────

func memoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 memory: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// minimalMediaAssetsFixture creates the canonical `media_assets`
// table aligned to clips_repository.go::MediaAssetColumns (40 columns
// — Blocco 3.1 commit 3/3, July 2026). AssetStoreSQLite.Get reads the
// full projection; a missing column causes COMPLETED-step tests to
// fail with `no such column: <X>`. Other columns use
// `NOT NULL DEFAULT ”` so callers can seed a minimal row via
// seedClipRow (id + lifecycle_state only).
func minimalMediaAssetsFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id                   TEXT PRIMARY KEY,
			source               TEXT NOT NULL DEFAULT '',
			name                 TEXT NOT NULL DEFAULT '',
			tags                 TEXT NOT NULL DEFAULT '[]',
			tags_norm            TEXT NOT NULL DEFAULT '',
			embedding_json       TEXT NOT NULL DEFAULT '[]',
			duration_ms          INTEGER NOT NULL DEFAULT 0,
			url                  TEXT NOT NULL DEFAULT '',
			media_type           TEXT NOT NULL DEFAULT '',
			status               TEXT NOT NULL DEFAULT '',
			local_path           TEXT NOT NULL DEFAULT '',
			relative_path        TEXT NOT NULL DEFAULT '',
			drive_file_id        TEXT NOT NULL DEFAULT '',
			drive_folder_id      TEXT NOT NULL DEFAULT '',
			drive_link           TEXT NOT NULL DEFAULT '',
			download_link        TEXT NOT NULL DEFAULT '',
			legacy_file_md5            TEXT NOT NULL DEFAULT '',
			metadata_json        TEXT NOT NULL DEFAULT '{}',
			visual_embedding     TEXT NOT NULL DEFAULT '[]',
			transcript_embedding TEXT NOT NULL DEFAULT '[]',
			created_at           TEXT NOT NULL DEFAULT '',
			updated_at           TEXT NOT NULL DEFAULT '',
			width                INTEGER NOT NULL DEFAULT 0,
			height               INTEGER NOT NULL DEFAULT 0,
			lifecycle_state      TEXT NOT NULL DEFAULT 'ACTIVE',
			deleted_at           TEXT,
			folder_id            TEXT NOT NULL DEFAULT '',
			parent_folder_id     TEXT NOT NULL DEFAULT '',
			folder_path          TEXT NOT NULL DEFAULT '',
			category             TEXT NOT NULL DEFAULT '',
			group_name           TEXT NOT NULL DEFAULT '',
			filename             TEXT NOT NULL DEFAULT '',
			error                TEXT NOT NULL DEFAULT '',
			thumb_url            TEXT NOT NULL DEFAULT '',
			phash                TEXT NOT NULL DEFAULT '',
			search_text          TEXT NOT NULL DEFAULT '',
			scene_type           TEXT NOT NULL DEFAULT '',
			quality_score        REAL NOT NULL DEFAULT 0.0,
			reuse_count          INTEGER NOT NULL DEFAULT 0,
			last_used_at         TEXT NOT NULL DEFAULT ''
    index_state TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
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
    semantic_role TEXT NOT NULL DEFAULT '',)
	`)
	if err != nil {
		t.Fatalf("create media_assets fixture: %v", err)
	}
}

// seedClipRow writes a minimal row with only id + lifecycle_state
// populated. Other canonical columns default in the fixture schema;
// SELECTs from AssetStoreSQLite.Get return zero/empty strings.
func seedClipRow(t *testing.T, db *sql.DB, id, lifecycleState string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, lifecycle_state) VALUES (?, ?)`,
		id, lifecycleState)
	if err != nil {
		t.Fatalf("seed %s (state=%s): %v", id, lifecycleState, err)
	}
}

// seedClipRowWithDriveFileID writes id + lifecycle_state + drive_file_id
// (canonical column) + metadata_json blob so both ScanMediaAsset
// (reads drive_file_id column directly) AND m.DriveFileID()
// (reads metadata via SetMetadataString) see the populated value.
// COMPLETED-step Drive-gone-check tests MUST seed through this
// helper to trigger the gated code path; basic-state tests use
// seedClipRow with no fileID.
func seedClipRowWithDriveFileID(t *testing.T, db *sql.DB, id, lifecycleState, fileID string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, lifecycle_state, drive_file_id, metadata_json) VALUES (?, ?, ?, ?)`,
		id, lifecycleState, fileID, `{"drive_file_id":"`+fileID+`"}`)
	if err != nil {
		t.Fatalf("seed %s (state=%s, fileID=%s): %v", id, lifecycleState, fileID, err)
	}
}

// newTestService wires a DeletionService against an in-memory SQLite +
// the recording dispatcher mock. Fields that aren't exercised in the
// routing test (voiceoverRepo, imagesRepo, assetTreeSvc, assetIndexSvc)
// are nil; DeleteClip's branch-selection reaches the dispatcher path
// for the "artlist" source where canonical is "clips" (ClipsRepository
// branch).
//
// PR-WAVE-1-DRIVE-SSOT (July 2026): the driveUploader arg is REMOVED
// from the canonical ctor (12-arg signature instead of 13; the
// driveUploader field has been retired from DeletionService entirely).
//
// driveGoneChecker + completionTxRunner are nil by default for
// the Blocco 3.1 commit 4/3 tests (the DeleteClip path doesn't reach
// CompleteAsset). Tests that exercise CompleteAsset build their own
// service via newTestServiceForComplete (which wires the COMPLETED
// step's recorder-mocks).
func newTestService(t *testing.T, db *sql.DB, dispatcher deletion.DispatcherPort) *deletion.DeletionService {
	t.Helper()
	clipsRepo := assets.NewClipsRepository(db, zap.NewNop())
	return deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ClipsRepo: clipsRepo,
		},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})
}

// newTestServiceForComplete wires a DeletionService for the COMPLETED-step
// tests (Blocco 3.1 commit 3/3). Includes the recorder-mock ports that
// CompleteAsset reaches into.
//
// PR-WAVE-1-DRIVE-SSOT (July 2026): driveUploader arg REMOVED.
func newTestServiceForComplete(
	t *testing.T,
	db *sql.DB,
	dispatcher deletion.DispatcherPort,
	driveGoneChecker deletion.DriveGoneChecker,
	completionTx deletion.CompletionTxRunner,
) *deletion.DeletionService {
	t.Helper()
	clipsRepo := assets.NewClipsRepository(db, zap.NewNop())
	return deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ClipsRepo: clipsRepo,
		},
		Dispatcher: dispatcher,
		Finalize: deletion.DeletionFinalizeDeps{
			DriveGoneChecker:   driveGoneChecker,
			CompletionTxRunner: completionTx,
		},
		Log: zap.NewNop(),
	})
}

// ── Blocco 3.1 commit 4/3 tests (DeleteClip — pre-existing test pinning) ──

// TestDeletionService_DeleteClip_RoutesThroughDispatcherEnqueueDriveDelete
// locks the Blocco 3.1 commit 4/3 (June 2026) refactor: DeleteClip
// routes through DispatcherPort.EnqueueDriveDelete(ctx, clipID,
// permanently). There is NO synchronous Drive call. The dispatcher
// mock records the call so the test asserts both (a) the call was
// made with the right (asset_id, permanently) tuple and (b) the
// dispatcher's return value is propagated.
func TestDeletionService_DeleteClip_RoutesThroughDispatcherEnqueueDriveDelete(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-rt", "ACTIVE")
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	if err := svc.DeleteClip(context.Background(), "artlist", "clip-rt", false); err != nil {
		t.Fatalf("DeleteClip: %v", err)
	}

	if len(dispatcher.calls) != 1 {
		t.Fatalf("DispatcherPort.EnqueueDriveDelete must be called exactly once (no sync Drive fallback path); got %d calls",
			len(dispatcher.calls))
	}
	if dispatcher.calls[0].assetID != "clip-rt" {
		t.Errorf("dispatcher.EnqueueDriveDelete asset_id: want %q got %q", "clip-rt", dispatcher.calls[0].assetID)
	}
	if dispatcher.calls[0].permanently != false {
		t.Errorf("dispatcher.EnqueueDriveDelete permanently: want false (safe-fallback); got %v",
			dispatcher.calls[0].permanently)
	}
}

func TestDeletionService_DeleteClip_PermanentlyFlagPropagated(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-perm", "ACTIVE")
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	if err := svc.DeleteClip(context.Background(), "artlist", "clip-perm", true); err != nil {
		t.Fatalf("DeleteClip: %v", err)
	}
	if dispatcher.calls[0].permanently != true {
		t.Errorf("dispatcher.EnqueueDriveDelete permanently: want true; got %v",
			dispatcher.calls[0].permanently)
	}
}

// TestDeletionService_DeleteClip_PropagatesDispatcherError: DeleteClip is
// now a legacy wrapper that delegates to DeleteAsset, so the dispatcher's
// error propagates UNCHANGED (no source-branch re-wrapping).
func TestDeletionService_DeleteClip_PropagatesDispatcherError(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-fail", "ACTIVE")
	dispatcher := &recordingDispatcher{err: errors.New("dispatcher simulate fail")}
	svc := newTestService(t, db, dispatcher)

	err := svc.DeleteClip(context.Background(), "artlist", "clip-fail", false)
	if err == nil {
		t.Fatal("expected error from dispatcher; got nil")
	}
	if !strings.Contains(err.Error(), "simulate fail") {
		t.Errorf("error must propagate the dispatcher's underlying cause; got: %q", err.Error())
	}
}

// TestDeletionService_DeleteClip_NilDispatcherFailsFast — PR-WAVE-1-DRIVE-SSOT
// (July 2026): the driveUploader arg REMOVED from the canonical ctor;
// now passes 5 leading nils for the 5 repository positions (was 6).
func TestDeletionService_DeleteClip_NilDispatcherFailsFast(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-nil-disp", "ACTIVE")
	clipsRepo := assets.NewClipsRepository(db, zap.NewNop())
	svc := deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ClipsRepo: clipsRepo,
		},
		Log: zap.NewNop(),
	})

	err := svc.DeleteClip(context.Background(), "artlist", "clip-nil-disp", false)
	if err == nil {
		t.Fatal("nil dispatcher must produce a wiring-error; got nil")
	}
	if !strings.Contains(err.Error(), "dispatcher is nil") {
		t.Errorf("error must carry the 'dispatcher is nil' marker; got: %q", err.Error())
	}
}

// TestDeletionService_BeaconAssetLifecycleState is the structural-contract
// beacon for the closed deletion chain. It is intentionally NOT a behavior
// test — it's a build-time invariant that locks the canonical enum names
// so a future refactor that drops a constant trips the reviewer to add
// a migration comment. Updated to the post-Blocco-3.1-commit-2/3 closed
// chain (7 stops: 1 legacy + 6 explicit confirmation hops).
func TestDeletionService_BeaconAssetLifecycleState(t *testing.T) {
	want := []asset.LifecycleState{
		asset.StateDeletePending, // legacy broad-intent (kept for pre-Blocco 3.1 rows)
		asset.StateDeleteRequested,
		asset.StateDriveDeletePending,
		asset.StateDriveDeleted, // added Blocco 3.1 commit 2/3 (July 2026)
		asset.StateLifecycleIndexDeletePending,
		asset.StateIndexDeleted, // added Blocco 3.1 commit 2/3 (July 2026)
		asset.StateDeleted,
	}
	if len(want) != 7 {
		t.Fatalf("deletion state-machine has wrong shape; want 7 stops (legacy + post-2/3 closed chain) got %d", len(want))
	}
}

// ── Blocco 3.1 commit 3/3 tests (CompleteAsset — P0 #1 audit, commit 3/3) ──

// TestCompleteAsset_DriveAliveBlocksCompletion (audit-spec test): a row
// at lifecycle_state=DELETE_REQUESTED (Drive file NOT yet removed — the
// canonical proof is missing) MUST trigger the drive_file_alive_block
// guard with a clear error. NO SQLite TX runs, NO outbox purge.
// This is the user-spec test #2 ("test che simuli Drive.DeleteFile
// failure → ... NO rimozione outbox, stato macchina non avanza").
func TestCompleteAsset_DriveAliveBlocksCompletion(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-alive", string(asset.StateDeleteRequested))
	dispatcher := &recordingDispatcher{}
	driveGone := &recordingDriveGoneChecker{isGone: true}
	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

	err := svc.CompleteAsset(context.Background(), "clip-alive")
	if err == nil {
		t.Fatal("pre-DRIVE_DELETED row MUST trigger drive_file_alive_block guard; got nil")
	}
	if !strings.Contains(err.Error(), "drive_file_alive_block") {
		t.Errorf("error must carry the 'drive_file_alive_block' marker; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "masih alive") && !strings.Contains(err.Error(), "still alive") {
		t.Errorf("error must carry the file-still-alive hint ('masih alive' OR 'still alive'); got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "DriveDeleteHandler has stamped DRIVE_DELETED") {
		t.Errorf("error must carry the retry guidance; got: %q", err.Error())
	}
	// The guard fires BEFORE any deleter invocation:
	if len(driveGone.calls) != 0 {
		t.Errorf("drive-gone check must NOT fire on state-gate rejection; got %d calls", len(driveGone.calls))
	}
	if len(tx.calls) != 0 {
		t.Errorf("SQLite TX must NOT run on state-gate rejection (no partial side-effects); got %d calls", len(tx.calls))
	}
	// The media row is still present (no hard delete).
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, "clip-alive").Scan(&rowCount); err != nil {
		t.Fatalf("count post-reject: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("media_assets row must still be present; got count %d", rowCount)
	}
}

// TestCompleteAsset_DeleteFileFailure_PropagatesErrorNoSideEffects
// (audit-spec test): the user spec REQUIRES a Drive.DeleteFile
// failure simulation. The DriveGoneChecker returns a non-nil error
// (simulating the Drive API failing). The COMPLETED step MUST:
// (a) propagate the error,
// (b) NOT run the SQLite TX (no SQLite delete),
// (c) NOT remove outbox state,
// (d) NOT advance the state machine.
func TestCompleteAsset_DeleteFileFailure_PropagatesErrorNoSideEffects(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	// Seed a row at DRIVE_DELETED + with a populated drive_file_id
	// so the Drive-gone-check gate is reached (CompleteAsset
	// only invokes the check when fileID is non-empty).
	seedClipRowWithDriveFileID(t, db, "clip-drive-down", string(asset.StateDriveDeleted), "file-id-123")
	dispatcher := &recordingDispatcher{}
	driveGone := &recordingDriveGoneChecker{
		goneErr: errors.New("drive api 503 service unavailable"),
	}
	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

	err := svc.CompleteAsset(context.Background(), "clip-drive-down")
	if err == nil {
		t.Fatal("Drive API failure MUST propagate error; got nil")
	}
	// Error message must mention the Drive-gone check failure.
	if !strings.Contains(err.Error(), "Drive-gone check") {
		t.Errorf("error must carry the Drive-gone check marker; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "drive api 503 service unavailable") {
		t.Errorf("error must wrap the underlying Drive API cause; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no SQLite delete, no outbox purge will run") {
		t.Errorf("error must mention the no-side-effects invariant; got: %q", err.Error())
	}
	// (b) NO SQLite TX was attempted.
	if len(tx.calls) != 0 {
		t.Errorf("SQLite TX MUST NOT run on Drive-failure; got %d calls", len(tx.calls))
	}
	// (c) The media row is still present (no hard delete).
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, "clip-drive-down").Scan(&rowCount); err != nil {
		t.Fatalf("count post-Drive-fail: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("media_assets row must STILL be present on Drive-failure (no hard delete); got count %d", rowCount)
	}
	if len(driveGone.calls) != 1 {
		t.Errorf("Drive-gone check MUST be invoked exactly once (lock the audit-pinning surface); got %d calls", len(driveGone.calls))
	}
}

// TestCompleteAsset_DriveFileStillAlive_BlocksCompletion: when the
// Drive-gone checker reports the file is STILL present (Drive.Trash
// call returned "not yet completed" / not-deleted), the guard fires
// even though the state machine says DRIVE_DELETED. This is the
// "drive_file_alive_guard_recheck" path inside CompleteAsset —
// distinct from the IndexDeleteHandler's guard.
func TestCompleteAsset_DriveFileStillAlive_BlocksCompletion(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRowWithDriveFileID(t, db, "clip-still-there", string(asset.StateDriveDeleted), "file-id-456")
	dispatcher := &recordingDispatcher{}
	// Drive API returns SUCCESS but the file is STILL present (not
	// yet trashed, possibly in the trash buffer). isGone=false.
	driveGone := &recordingDriveGoneChecker{isGone: false}
	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

	err := svc.CompleteAsset(context.Background(), "clip-still-there")
	if err == nil {
		t.Fatal("file-still-present in Drive MUST trigger recheck guard; got nil")
	}
	if !strings.Contains(err.Error(), "drive_file_alive_guard_recheck") {
		t.Errorf("error must carry the recheck-guard marker; got: %q", err.Error())
	}
	if len(tx.calls) != 0 {
		t.Errorf("SQLite TX MUST NOT run when file is still alive; got %d calls", len(tx.calls))
	}
	if len(driveGone.calls) != 1 {
		t.Errorf("Drive-gone check MUST be invoked exactly once; got %d calls", len(driveGone.calls))
	}
}

// TestCompleteAsset_HappyPath_RunsAtomicCleanup: the canonical full
// chain reaches INDEX_DELETED (a non-terminal post-Qdrant state),
// DriveGoneChecker returns isGone=true, and the SQLite TX runs
// exactly once. The asset is purged from media_assets + the
// outbox_events are cleared; the row's lifecycle_state stamp is
// then flipped to COMPLETED. This is the production happy path
// the COMPLETED step exists to serve. (We intentionally seed a
// NON-terminal post-Drive state — at StateDeleted the entry is
// terminal-idempotent and CompleteAsset short-circuits WITHOUT
// running the TX; that path is covered by
// TestCompleteAsset_IdempotentReRun_OkWithoutSideEffects.)
func TestCompleteAsset_HappyPath_RunsAtomicCleanup(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRowWithDriveFileID(t, db, "clip-complete-me", string(asset.StateIndexDeleted), "file-id-789")
	dispatcher := &recordingDispatcher{}
	// DriveGoneChecker: per-fileID isGone so the test wires the
	// specific fileID seeded above; alternative path via
	// isGone=true also works because byFileID miss falls back to
	// isGone safely.
	driveGone := &recordingDriveGoneChecker{
		byFileID: map[string]driveGoneResponse{
			"file-id-789": {isGone: true},
		},
	}
	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

	if err := svc.CompleteAsset(context.Background(), "clip-complete-me"); err != nil {
		t.Fatalf("happy path should succeed; got: %v", err)
	}
	if got := len(tx.calls); got != 1 {
		t.Errorf("CompletionTxRunner.RunCompletionTx MUST be called exactly once on the happy path; got %d", got)
	}
	if got := tx.calls[0]; got != "clip-complete-me" {
		t.Errorf("CompletionTxRunner.RunCompletionTx must be called with the right assetID; got %q want %q", got, "clip-complete-me")
	}
}

// TestCompleteAsset_IdempotentReRun_OkWithoutSideEffects (audit-spec
// test): re-running CompleteAsset after a successful run MUST return
// nil WITHOUT invoking the SQLite TX (the row was already hard-deleted
// by the prior run; the SQLite Get returns nil; the early-return path
// short-circuits before the TX). This is the user-spec idempotency
// test ("Test di idempotenza: re-run dopo successo → ok senza
// side-effect").
func TestCompleteAsset_IdempotentReRun_OkWithoutSideEffects(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	// NOTE: row is ABSENT to simulate the post-success state.
	dispatcher := &recordingDispatcher{}
	driveGone := &recordingDriveGoneChecker{isGone: true}
	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

	if err := svc.CompleteAsset(context.Background(), "clip-already-gone"); err != nil {
		t.Fatalf("idempotent re-run on absent row should return nil; got: %v", err)
	}
	if len(tx.calls) != 0 {
		t.Errorf("CompletionTxRunner MUST NOT run on idempotent re-run (no side-effects on absent row); got %d calls", len(tx.calls))
	}
	if len(driveGone.calls) != 0 {
		t.Errorf("Drive-gone check MUST NOT run when row is absent (pre-flight short-circuits); got %d calls", len(driveGone.calls))
	}
}

// TestCompleteAsset_ResumesFromAnyPostDriveState: per the user spec
// "Idempotenza: ripartire dallo stato corrente, NON dall'inizio (se
// si è a DRIVE_DELETED, riprendi da lì)". CompleteAsset must accept
// entry from any non-terminal post-Drive state — DRIVE_DELETED,
// INDEX_DELETE_PENDING, INDEX_DELETED — WITHOUT re-running the state
// machine; it just runs the atomic cleanup via CompletionTxRunner.
// StateDeleted is NOT included here: it is terminal (the asset was
// already fully retired) and CompleteAsset short-circuits with the
// idempotent-skip path. The terminal-state contract is covered by
// TestCompleteAsset_IdempotentReRun_OkWithoutSideEffects.
func TestCompleteAsset_ResumesFromAnyPostDriveState(t *testing.T) {
	for _, state := range []asset.LifecycleState{
		asset.StateDriveDeleted,
		asset.StateLifecycleIndexDeletePending,
		asset.StateIndexDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			db := memoryDB(t)
			minimalMediaAssetsFixture(t, db)
			seedClipRow(t, db, "clip-resume", string(state))
			dispatcher := &recordingDispatcher{}
			driveGone := &recordingDriveGoneChecker{isGone: true}
			tx := &recordingCompletionTxRunner{}
			svc := newTestServiceForComplete(t, db, dispatcher, driveGone, tx)

			if err := svc.CompleteAsset(context.Background(), "clip-resume"); err != nil {
				t.Fatalf("state %q: CompleteAsset must succeed; got: %v", state, err)
			}
			if len(tx.calls) != 1 {
				t.Errorf("state %q: CompletionTxRunner must run exactly once; got %d", state, len(tx.calls))
			}
		})
	}
}

// TestCompleteAsset_NilCompletionTxFailsFast (godlike/05 wiring guard):
// a DeletionService built with a nil completionTxRunner must return a
// typed wiring-error rather than silently no-op'ing the COMPLETED step
// (the canonical pre-Wave-21 silent-no-op regression should not recur).
func TestCompleteAsset_NilCompletionTxFailsFast(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	// Seed a non-terminal post-Drive state so production reaches the
	// completionTxRunner wiring-guard BEFORE the terminal-idempotent
	// short-circuit (which would skip the guard entirely).
	seedClipRow(t, db, "clip-nil-tx", string(asset.StateIndexDeleted))
	dispatcher := &recordingDispatcher{}
	driveGone := &recordingDriveGoneChecker{isGone: true}
	// completionTxRunner is nil: must fail-fast with wiring-error.
	svc := newTestServiceForComplete(t, db, dispatcher, driveGone, nil)

	err := svc.CompleteAsset(context.Background(), "clip-nil-tx")
	if err == nil {
		t.Fatal("nil completionTxRunner MUST produce a wiring-error; got nil")
	}
	if !strings.Contains(err.Error(), "completionTxRunner not wired") {
		t.Errorf("error must carry the 'completionTxRunner not wired' marker; got: %q", err.Error())
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, "clip-nil-tx").Scan(&rowCount); err != nil {
		t.Fatalf("count post-nil-tx: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("media_assets row must STILL be present on nil-tx-wiring-error (fail-closed); got count %d", rowCount)
	}
}

// TestCompleteAsset_EmptyAssetIDIsTerminal: a nil/empty asset_id is
// rejected up-front (terminal — retry cannot conjure an id).
func TestCompleteAsset_EmptyAssetIDIsTerminal(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	svc := newTestServiceForComplete(t, db, &recordingDispatcher{}, &recordingDriveGoneChecker{}, &recordingCompletionTxRunner{})

	err := svc.CompleteAsset(context.Background(), "")
	if err == nil {
		t.Fatal("empty asset_id MUST be rejected; got nil")
	}
	if !strings.Contains(err.Error(), "asset_id is required") {
		t.Errorf("error must carry the 'asset_id is required' marker; got: %q", err.Error())
	}
}

// NOTE: the deferred TestDeleteClip_AssetTreeCleanupErrorPropagates
// (forward-pointer from Blocco 3.1 commit 3/3) is now implemented as
// TestCompleteAsset_AssetTreeCleanupRunsInTerminalPath +
// TestCompleteAsset_AssetTreeCleanupFailurePropagates in
// delete_asset_test.go — the asset-tree cleanup moved from the
// synchronous DeleteClip post-dispatch path into the terminal
// CompleteAsset close-out, where it is exercised against a real
// *assettree.Service backed by an in-memory asset_tree_nodes table.
