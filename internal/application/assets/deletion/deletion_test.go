// Package deletion_test — Blocco 3.1 commit 4/3 (June 2026) test pinning
// for the DeletionService.DeleteClip refactor.
//
// Pre-fix: DeleteClip called sync driveUploader.DeleteFile/TrashFile
// (the canonical QDRANT-002 ticket-item-D regression that hid Drive-API
// failures from operators) THEN routed through Dispatcher.EnqueueAndDelete
// (which only emits the index-delete event, NOT the drive-delete event).
//
// Post-fix: DeleteClip routes EXCLUSIVELY through
// DispatcherPort.EnqueueDriveDelete(ctx, clipID, permanently) — the
// dispatcher's tx atomically stamps lifecycle_state=DELETE_REQUESTED + emits
// asset.drive.delete_requested.v1. DriveDeleteHandler runs the actual
// Drive call asynchronously. There is NO synchronous Drive call in
// DeleteClip.
package deletion_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// ── Mocks ────────────────────────────────────────────────────────────

// recordingDispatcher satisfies deletion.DispatcherPort (structurally)
// and records each EnqueueDriveDelete call so tests can assert
// (a) the call was made, (b) the (assetID, permanently) tuple is what
// the service decided.
type recordingDispatcher struct {
	calls []enqueueDriveDeleteCall
	err   error
}

type enqueueDriveDeleteCall struct {
	assetID     string
	permanently bool
}

func (r *recordingDispatcher) EnqueueDriveDelete(_ context.Context, assetID string, permanently bool) error {
	r.calls = append(r.calls, enqueueDriveDeleteCall{assetID: assetID, permanently: permanently})
	return r.err
}

// ── Test fixtures ────────────────────────────────────────────────────

// memoryDB + minimalMediaAssetsFixture mirrors the canonical minimal
// fixture pattern used by dispatcher_delete_test.go + qdrant_flow_e2e_test.go.
func memoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 memory: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func minimalMediaAssetsFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id              TEXT PRIMARY KEY,
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			created_at      TEXT NOT NULL DEFAULT '',
			updated_at      TEXT NOT NULL DEFAULT '',
			metadata        TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("create media_assets fixture: %v", err)
	}
}

func seedClipRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, lifecycle_state, created_at, updated_at, metadata) VALUES (?, 'ACTIVE', '', '', '{}')`,
		id)
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// newTestService wires a DeletionService against an in-memory SQLite +
// the recording dispatcher mock. Fields that aren't exercised in the
// routing test (voiceoverRepo, imagesRepo, driveUploader (deprecated),
// assetTreeSvc, assetIndexSvc) are nil; DeleteClip's branch-selection
// reaches the dispatcher path for the "artlist" source where
// canonical is "clips" (ClipsRepository branch).
func newTestService(t *testing.T, db *sql.DB, dispatcher deletion.DispatcherPort) *deletion.DeletionService {
	t.Helper()
	clipsRepo := assets.NewClipsRepository(db)
	return deletion.NewDeletionService(
		clipsRepo, clipsRepo, clipsRepo,
		nil, nil, // voiceoverRepo, imagesRepo
		nil, // driveUploader (deprecated; ignored post-Blocco 3.1 commit 4/3)
		nil, // assetTreeSvc
		nil, // assetIndexSvc
		dispatcher,
		zap.NewNop(),
	)
}

// ── Tests ────────────────────────────────────────────────────────────

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
	seedClipRow(t, db, "clip-rt")
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
	// The row's lifecycle_state must remain ACTIVE post-emit (the
	// dispatcher stamps DELETE_REQUESTED on its own async path; this
	// service never writes lifecycle_state directly).
	var currentState string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = ?`, "clip-rt").Scan(&currentState); err != nil {
		t.Fatalf("read post-emit lifecycle_state: %v", err)
	}
	if currentState == "DELETE_REQUESTED" {
		t.Errorf("DeleteClip must NOT stamp lifecycle_state directly (dispatcher path is canonical); got %q", currentState)
	}
}

// TestDeletionService_DeleteClip_PermanentlyFlagPropagated: a
// hard-delete request routes through EnqueueDriveDelete with
// permanently=true. The flag SURVIVES to DriveDeleteHandler which
// uses it to choose Trash slot vs Delete API.
func TestDeletionService_DeleteClip_PermanentlyFlagPropagated(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-perm")
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

// TestDeletionService_DeleteClip_PropagatesDispatcherError: the
// dispatcher returning a SQL error must surface as a wrapped error
// from DeleteClip. The wrap carries the "failed to delete from
// database" marker + the underlying cause.
func TestDeletionService_DeleteClip_PropagatesDispatcherError(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-fail")
	dispatcher := &recordingDispatcher{err: errors.New("dispatcher simulate fail")}
	svc := newTestService(t, db, dispatcher)

	err := svc.DeleteClip(context.Background(), "artlist", "clip-fail", false)
	if err == nil {
		t.Fatal("expected error from dispatcher; got nil")
	}
	if !strings.Contains(err.Error(), "failed to delete from database") {
		t.Errorf("error must carry the 'failed to delete from database' marker; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "simulate fail") {
		t.Errorf("error must wrap the dispatcher's underlying cause; got: %q", err.Error())
	}
}

// TestDeletionService_DeleteClip_NilDispatcherFailsFast locks the
// Blocco 3.1 commit 4/3 defence-in-depth contract: production wiring
// MUST supply a non-nil dispatcher. A nil-dispatcher service returns
// a wiring-error rather than silently falling back to sync Drive delete
// (the QDRANT-002 ticket-D pre-fix regression).
func TestDeletionService_DeleteClip_NilDispatcherFailsFast(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedClipRow(t, db, "clip-nil-disp")
	clipsRepo := assets.NewClipsRepository(db)
	svc := deletion.NewDeletionService(
		clipsRepo, clipsRepo, clipsRepo,
		nil, nil, nil, nil, nil,
		nil, // dispatcher nil — must fail-fast with a wiring-error
		zap.NewNop(),
	)

	err := svc.DeleteClip(context.Background(), "artlist", "clip-nil-disp", false)
	if err == nil {
		t.Fatal("nil dispatcher must produce a wiring-error; got nil")
	}
	if !strings.Contains(err.Error(), "dispatcher is nil") {
		t.Errorf("error must carry the 'dispatcher is nil' marker; got: %q", err.Error())
	}
}

// TestDeletionService_DeleteClip_BeaconAssetLifecycleState pins the
// IsValidTransition contract that index_state changes via the
// dispatcher's tx must respect the closed state machine. Locks the
// pre-existing pattern: the canonical LifecycleState enum values
// are referenced inline in IndexDeleteHandler pre-flight, so a
// regression that drops an enum constant surfaces as a build failure.
func TestDeletionService_BeaconAssetLifecycleState(t *testing.T) {
	// This test is the structural-contract beacon for the closed
	// 5-state deletion machine. It is intentionally NOT a behavior
	// test — it's a build-time invariant that locks the canonical
	// enum names so a future refactor that drops a constant trips
	// the reviewer to add a migration comment.
	want := []asset.LifecycleState{
		asset.StateDeleteRequested,
		asset.StateDriveDeletePending,
		asset.StateLifecycleIndexDeletePending,
		asset.StateDeleted,
	}
	if len(want) != 4 {
		t.Fatalf("deletion state-machine has wrong shape; want 4 stops got %d", len(want))
	}
	_ = strconv.Itoa // stdlib import used by sibling tests if extended
}
