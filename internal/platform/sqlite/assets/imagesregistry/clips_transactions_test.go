package imagesregistry

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type recordingCanonicalClipWriter struct {
	commitTxCalled bool
	commitTx       *sql.Tx
	commitRequest  persistence.CommitRequest
	patchTxCalled  bool
	patchTx        *sql.Tx
	patch          persistence.AssetPatch
}

func (w *recordingCanonicalClipWriter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

func (w *recordingCanonicalClipWriter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (w *recordingCanonicalClipWriter) CommitTx(_ context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		return persistence.CommitResult{}, nil
	}
	w.commitTxCalled = true
	w.commitTx = sqlTx
	w.commitRequest = req
	return persistence.CommitResult{}, nil
}

func (w *recordingCanonicalClipWriter) PatchAsset(context.Context, persistence.AssetPatch) error {
	return nil
}

func (w *recordingCanonicalClipWriter) PatchAssetTx(_ context.Context, tx persistence.Transaction, patch persistence.AssetPatch) error {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		return nil
	}
	w.patchTxCalled = true
	w.patchTx = sqlTx
	w.patch = patch
	return nil
}

func (w *recordingCanonicalClipWriter) ReconcileDriveLocations(context.Context, []persistence.DriveLocationPatch) error {
	return nil
}

func (w *recordingCanonicalClipWriter) ReconcileDriveLocationsTx(context.Context, persistence.Transaction, []persistence.DriveLocationPatch) error {
	return nil
}

func (w *recordingCanonicalClipWriter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	req, err := canonicalClipCommitRequest(clip)
	if err != nil {
		return err
	}
	_, err = w.CommitTx(ctx, tx, req)
	return err
}

func (w *recordingCanonicalClipWriter) SetIndexStateTx(ctx context.Context, tx *sql.Tx, assetID string, state asset.IndexState) error {
	value := string(state)
	return w.PatchAssetTx(ctx, tx, persistence.AssetPatch{AssetID: assetID, IndexState: &value})
}

var _ persistence.AssetCommitter = (*recordingCanonicalClipWriter)(nil)
var _ persistence.AssetMutator = (*recordingCanonicalClipWriter)(nil)
var _ persistence.CanonicalAssetWriter = (*recordingCanonicalClipWriter)(nil)

func newTransactionDelegationTestDB(t *testing.T) (*sql.DB, *sql.Tx) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return db, tx
}

func TestClipsRepository_UpsertClipTxDelegatesToCanonicalWriter(t *testing.T) {
	_, tx := newTransactionDelegationTestDB(t)
	writer := &recordingCanonicalClipWriter{}
	repo := NewClipsRepository(nil, nil)
	repo.SetCanonicalWriter(writer)

	clip := &asset.Asset{
		ID:             "clip-canonical-delegation",
		Source:         "youtube",
		Name:           "clip",
		Filename:       "clip.mp4",
		MediaType:      "video",
		LifecycleState: asset.StateActive,
		Metadata: asset.Metadata{
			"legacy_file_md5": "content-v1",
			"source_version":  "source-v1",
		},
	}

	require.NoError(t, repo.UpsertClipTx(context.Background(), tx, clip))
	require.True(t, writer.commitTxCalled)
	require.Same(t, tx, writer.commitTx)
	require.Equal(t, clip.ID, writer.commitRequest.AssetID)
	require.Equal(t, "youtube", writer.commitRequest.Source)
	require.Equal(t, "video", writer.commitRequest.MediaType)
	require.Equal(t, "content-v1", writer.commitRequest.ContentHash)
	require.False(t, writer.commitRequest.EmitIndexEvent, "dispatcher owns the outbox event")
	require.NotEmpty(t, writer.commitRequest.Taxonomy.Namespace)
}

func TestClipsRepository_SetIndexStateTxDelegatesToCanonicalMutator(t *testing.T) {
	_, tx := newTransactionDelegationTestDB(t)
	writer := &recordingCanonicalClipWriter{}
	repo := NewClipsRepository(nil, nil)
	repo.SetCanonicalWriter(writer)

	require.NoError(t, repo.SetIndexStateTx(context.Background(), tx, "clip-state-delegation", asset.StateIndexDeletePending))
	require.True(t, writer.patchTxCalled)
	require.Same(t, tx, writer.patchTx)
	require.Equal(t, "clip-state-delegation", writer.patch.AssetID)
	require.NotNil(t, writer.patch.IndexState)
	require.Equal(t, string(asset.StateIndexDeletePending), *writer.patch.IndexState)
}

func TestClipsRepository_TransactionMutationsFailClosedWithoutCanonicalWriter(t *testing.T) {
	_, tx := newTransactionDelegationTestDB(t)
	repo := NewClipsRepository(nil, nil)
	clip := &asset.Asset{
		ID:             "clip-without-writer",
		Source:         "youtube",
		Filename:       "clip.mp4",
		MediaType:      "video",
		LifecycleState: asset.StateActive,
	}

	err := repo.UpsertClipTx(context.Background(), tx, clip)
	require.Error(t, err)
	require.Contains(t, err.Error(), "canonical AssetCommitter is required")

	err = repo.SetIndexStateTx(context.Background(), tx, clip.ID, asset.StateIndexed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "canonical AssetMutator is required")
}
