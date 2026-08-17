package deletion_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// seedAssetRowWithSource writes a minimal media_assets row with id +
// source + lifecycle_state populated. DeleteAsset must accept the row
// regardless of source — the source column is provenance, not an
// authorization gate.
func seedAssetRowWithSource(t *testing.T, db *sql.DB, id, source string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, lifecycle_state) VALUES (?, ?, 'ACTIVE')`,
		id, source)
	if err != nil {
		t.Fatalf("seed asset %s (source=%s): %v", id, source, err)
	}
}

// TestDeleteAsset_AcceptsAnyCanonicalAsset pins the core contract:
// DeleteAsset accepts clip, script, document and final_audio assets
// alike. The source/type/provider column is NOT consulted — every row
// resolves through the canonical media_assets catalog (clipsRepo.Get)
// and dispatches EnqueueDriveDelete(assetID, permanently).
func TestDeleteAsset_AcceptsAnyCanonicalAsset(t *testing.T) {
	cases := []struct {
		name        string
		assetID     string
		source      string
		permanently bool
	}{
		{name: "clip", assetID: "asset-clip", source: "clip", permanently: false},
		{name: "script", assetID: "asset-script", source: "script", permanently: true},
		{name: "document", assetID: "asset-document", source: "document", permanently: true},
		{name: "final_audio", assetID: "asset-final-audio", source: "final_audio", permanently: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := memoryDB(t)
			minimalMediaAssetsFixture(t, db)
			seedAssetRowWithSource(t, db, tc.assetID, tc.source)
			dispatcher := &recordingDispatcher{}
			svc := newTestService(t, db, dispatcher)

			if err := svc.DeleteAsset(context.Background(), tc.assetID, tc.permanently); err != nil {
				t.Fatalf("DeleteAsset(%s): %v", tc.assetID, err)
			}

			if len(dispatcher.calls) != 1 {
				t.Fatalf("EnqueueDriveDelete must be called exactly once; got %d", len(dispatcher.calls))
			}
			if dispatcher.calls[0].assetID != tc.assetID {
				t.Errorf("EnqueueDriveDelete asset_id: want %q got %q", tc.assetID, dispatcher.calls[0].assetID)
			}
			if dispatcher.calls[0].permanently != tc.permanently {
				t.Errorf("EnqueueDriveDelete permanently: want %v got %v", tc.permanently, dispatcher.calls[0].permanently)
			}
		})
	}
}

// TestDeleteAsset_DoesNotRequireSourceWhitelist pins that an unknown /
// arbitrary source still deletes: DeleteAsset never consults the source
// column or CanonicalSource to decide whether an asset may be deleted.
func TestDeleteAsset_DoesNotRequireSourceWhitelist(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedAssetRowWithSource(t, db, "asset-unknown", "some-future-source-type")
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	if err := svc.DeleteAsset(context.Background(), "asset-unknown", false); err != nil {
		t.Fatalf("DeleteAsset(unknown source): %v", err)
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("EnqueueDriveDelete must be called exactly once; got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].assetID != "asset-unknown" {
		t.Errorf("EnqueueDriveDelete asset_id: want %q got %q", "asset-unknown", dispatcher.calls[0].assetID)
	}
}

// TestDeleteAsset_UnknownAssetReturnsNotFound pins that a missing row
// returns ErrAssetNotFound BEFORE the dispatcher is touched (no outbox
// event for a non-existent id).
func TestDeleteAsset_UnknownAssetReturnsNotFound(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	err := svc.DeleteAsset(context.Background(), "asset-missing", false)
	if !errors.Is(err, deletion.ErrAssetNotFound) {
		t.Fatalf("want ErrAssetNotFound; got %v", err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("dispatcher must not be called for a missing asset; got %d calls", len(dispatcher.calls))
	}
}

// TestDeleteAsset_MissingDispatcherFailsBeforeMutation pins the
// fail-closed wiring contract: a nil dispatcher returns
// ErrDeletionDispatcherUnavailable and never reaches the emit path.
func TestDeleteAsset_MissingDispatcherFailsBeforeMutation(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedAssetRowWithSource(t, db, "asset-nil-disp", "clip")
	clipsRepo := assets.NewClipsRepository(db, zap.NewNop())
	svc := deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ClipsRepo: clipsRepo,
		},
		Log: zap.NewNop(),
	})

	err := svc.DeleteAsset(context.Background(), "asset-nil-disp", false)
	if !errors.Is(err, deletion.ErrDeletionDispatcherUnavailable) {
		t.Fatalf("want ErrDeletionDispatcherUnavailable; got %v", err)
	}
}

// TestDeleteAsset_EmptyAssetID pins the required-id guard.
func TestDeleteAsset_EmptyAssetID(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	err := svc.DeleteAsset(context.Background(), "", false)
	if !errors.Is(err, deletion.ErrAssetIDRequired) {
		t.Fatalf("want ErrAssetIDRequired; got %v", err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("dispatcher must not be called for an empty id; got %d calls", len(dispatcher.calls))
	}
}

// TestDeleteAsset_MissingRepository pins the repository wiring guard.
func TestDeleteAsset_MissingRepository(t *testing.T) {
	svc := deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Dispatcher: &recordingDispatcher{},
		Log:        zap.NewNop(),
	})

	err := svc.DeleteAsset(context.Background(), "asset-no-repo", false)
	if !errors.Is(err, deletion.ErrAssetRepositoryUnavailable) {
		t.Fatalf("want ErrAssetRepositoryUnavailable; got %v", err)
	}
}

// TestDeleteClip_LegacyWrapperDelegatesToDeleteAsset pins the wrapper
// contract: DeleteClip(source, id, permanently) delegates to
// DeleteAsset(id, permanently). The source argument is NOT consulted —
// an unknown/future source still deletes, and the dispatcher receives
// the exact assetID + permanently tuple.
func TestDeleteClip_LegacyWrapperDelegatesToDeleteAsset(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedAssetRowWithSource(t, db, "asset-wrapper", "artlist")
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	if err := svc.DeleteClip(context.Background(), "not-a-real-source", "asset-wrapper", true); err != nil {
		t.Fatalf("DeleteClip wrapper: %v", err)
	}

	if len(dispatcher.calls) != 1 {
		t.Fatalf("EnqueueDriveDelete must be called exactly once; got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].assetID != "asset-wrapper" {
		t.Errorf("EnqueueDriveDelete asset_id: want %q got %q", "asset-wrapper", dispatcher.calls[0].assetID)
	}
	if dispatcher.calls[0].permanently != true {
		t.Errorf("EnqueueDriveDelete permanently: want true got %v", dispatcher.calls[0].permanently)
	}
}

// ── Asset-tree cleanup in CompleteAsset ────────────────────────────

// createAssetTreeNodesFixture creates the minimal asset_tree_nodes table
// the AssetTreeRepository DELETE paths touch (id, source, asset_id).
func createAssetTreeNodesFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS asset_tree_nodes (
			id       TEXT PRIMARY KEY,
			source   TEXT NOT NULL DEFAULT '',
			asset_id TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		t.Fatalf("create asset_tree_nodes fixture: %v", err)
	}
}

// seedAssetTreeNode writes a minimal asset_tree_nodes row.
func seedAssetTreeNode(t *testing.T, db *sql.DB, id, source, assetID string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO asset_tree_nodes (id, source, asset_id) VALUES (?, ?, ?)`,
		id, source, assetID)
	if err != nil {
		t.Fatalf("seed node %s: %v", id, err)
	}
}

// seedAssetRowState writes a media_assets row with id + source +
// lifecycle_state populated.
func seedAssetRowState(t *testing.T, db *sql.DB, id, source, state string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, lifecycle_state) VALUES (?, ?, ?)`,
		id, source, state)
	if err != nil {
		t.Fatalf("seed asset %s (source=%s, state=%s): %v", id, source, state, err)
	}
}

// newTestServiceForAssetTree wires a DeletionService with a real
// *assettree.Service so the COMPLETED-step asset-tree cleanup can be
// exercised against an in-memory asset_tree_nodes table.
func newTestServiceForAssetTree(
	t *testing.T,
	db *sql.DB,
	treeSvc *assettree.Service,
	tx *recordingCompletionTxRunner,
) *deletion.DeletionService {
	t.Helper()
	clipsRepo := assets.NewClipsRepository(db, zap.NewNop())
	return deletion.NewDeletionService(deletion.DeletionServiceDeps{
		Repos:      deletion.DeletionRepoDeps{ClipsRepo: clipsRepo},
		Index:      deletion.DeletionIndexDeps{AssetTreeSvc: treeSvc},
		Dispatcher: &recordingDispatcher{},
		Finalize: deletion.DeletionFinalizeDeps{
			DriveGoneChecker:   &recordingDriveGoneChecker{isGone: true},
			CompletionTxRunner: tx,
		},
		Log: zap.NewNop(),
	})
}

// TestCompleteAsset_AssetTreeCleanupRunsInTerminalPath pins the
// relocated asset-tree cleanup: after the canonical chain reached DELETED,
// CompleteAsset removes the asset-tree nodes by source+asset_id and by
// node id BEFORE the atomic media_assets/outbox purge.
func TestCompleteAsset_AssetTreeCleanupRunsInTerminalPath(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	createAssetTreeNodesFixture(t, db)
	seedAssetRowState(t, db, "asset-tree", "artlist", string(asset.StateIndexDeleted))
	seedAssetTreeNode(t, db, "node-1", "artlist", "asset-tree")

	treeRepo, err := assets.NewAssetTreeRepository(db, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTreeRepository: %v", err)
	}
	treeSvc := assettree.NewService(treeRepo, zap.NewNop())

	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForAssetTree(t, db, treeSvc, tx)

	if err := svc.CompleteAsset(context.Background(), "asset-tree"); err != nil {
		t.Fatalf("CompleteAsset: %v", err)
	}

	if len(tx.calls) != 1 {
		t.Errorf("CompletionTxRunner must run exactly once after tree cleanup; got %d", len(tx.calls))
	}

	var nodeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_tree_nodes WHERE asset_id = ? OR id = ?`, "asset-tree", "asset-tree").Scan(&nodeCount); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if nodeCount != 0 {
		t.Errorf("asset-tree nodes must be cleaned up; got %d remaining", nodeCount)
	}
}

// TestCompleteAsset_AssetTreeCleanupFailurePropagates pins fail-closed
// semantics: when the asset-tree cleanup fails, CompleteAsset propagates
// the error and does NOT run the canonical purge (CompletionTxRunner),
// leaving the canonical row in place for a safe retry.
func TestCompleteAsset_AssetTreeCleanupFailurePropagates(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	// Deliberately do NOT create asset_tree_nodes: DeleteByAssetID fails.
	seedAssetRowState(t, db, "asset-tree-fail", "artlist", string(asset.StateIndexDeleted))

	treeRepo, err := assets.NewAssetTreeRepository(db, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTreeRepository: %v", err)
	}
	treeSvc := assettree.NewService(treeRepo, zap.NewNop())

	tx := &recordingCompletionTxRunner{}
	svc := newTestServiceForAssetTree(t, db, treeSvc, tx)

	err = svc.CompleteAsset(context.Background(), "asset-tree-fail")
	if err == nil {
		t.Fatal("asset-tree cleanup failure must propagate from CompleteAsset; got nil")
	}
	if !strings.Contains(err.Error(), "asset-tree cleanup DeleteByAssetID") {
		t.Errorf("error must carry the asset-tree cleanup marker; got: %q", err.Error())
	}
	if len(tx.calls) != 0 {
		t.Errorf("CompletionTxRunner must NOT run when tree cleanup fails (retry-safe); got %d calls", len(tx.calls))
	}
}
