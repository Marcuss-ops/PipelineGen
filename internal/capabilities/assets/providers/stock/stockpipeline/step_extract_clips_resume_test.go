package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// mapBatchRepo is an in-memory StockBatchRepository with production-like
// artifact transition semantics (PLANNED/RETRY_WAIT claimable, EXTRACTING
// reclaimable only via failed, progress states terminal). Batch/group
// methods are no-ops: resume tests only exercise artifact lifecycle.
type mapBatchRepo struct {
	artifacts map[string]*StockArtifact
}

func newMapBatchRepo(rows ...*StockArtifact) *mapBatchRepo {
	m := &mapBatchRepo{artifacts: map[string]*StockArtifact{}}
	for _, r := range rows {
		cp := *r
		m.artifacts[r.ID] = &cp
	}
	return m
}

func (m *mapBatchRepo) CreateBatch(_ context.Context, _ *StockBatch) error { return nil }
func (m *mapBatchRepo) GetBatch(_ context.Context, _ string) (*StockBatch, error) {
	return nil, nil
}
func (m *mapBatchRepo) UpdateBatchStatus(_ context.Context, _ string, _ BatchState, _ string) error {
	return nil
}
func (m *mapBatchRepo) CreateGroup(_ context.Context, _ *StockBatchGroup) error { return nil }
func (m *mapBatchRepo) GetGroup(_ context.Context, _ string) (*StockBatchGroup, error) {
	return nil, nil
}
func (m *mapBatchRepo) UpdateGroupStatus(_ context.Context, _ string, _ GroupState, _ string) error {
	return nil
}
func (m *mapBatchRepo) ListGroups(_ context.Context, _ string) ([]StockBatchGroup, error) {
	return nil, nil
}
func (m *mapBatchRepo) CreateArtifact(_ context.Context, a *StockArtifact) error {
	if _, ok := m.artifacts[a.ID]; !ok {
		cp := *a
		m.artifacts[a.ID] = &cp
	}
	return nil
}
func (m *mapBatchRepo) GetArtifact(_ context.Context, id string) (*StockArtifact, error) {
	if a, ok := m.artifacts[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, nil
}
func (m *mapBatchRepo) MarkArtifactExtracting(_ context.Context, id string) error {
	a, ok := m.artifacts[id]
	if !ok || (a.Status != ArtifactStatePlanned && a.Status != ArtifactStateRetryWait) {
		return errors.New("stockbatches: state transition failed: no matching row or race condition")
	}
	a.Status = ArtifactStateExtracting
	a.Attempts++
	return nil
}
func (m *mapBatchRepo) MarkArtifactExtracted(_ context.Context, id, localPath, sha256 string, actualDurationMs int) error {
	a, ok := m.artifacts[id]
	if !ok || (a.Status != ArtifactStateExtracting && a.Status != ArtifactStateExtracted) {
		return errors.New("stockbatches: state transition failed: no matching row or race condition")
	}
	a.Status = ArtifactStateExtracted
	a.LocalPath = localPath
	return nil
}
func (m *mapBatchRepo) MarkArtifactPublished(_ context.Context, id, driveFileID, driveFolderID, driveLink string) error {
	a, ok := m.artifacts[id]
	if !ok || (a.Status != ArtifactStateExtracted && a.Status != ArtifactStatePublished) {
		return errors.New("stockbatches: state transition failed: no matching row or race condition")
	}
	a.Status = ArtifactStatePublished
	a.DriveFileID = driveFileID
	return nil
}
func (m *mapBatchRepo) MarkArtifactVerified(_ context.Context, id string) error {
	a, ok := m.artifacts[id]
	if !ok || (a.Status != ArtifactStatePublished && a.Status != ArtifactStateVerified) {
		return errors.New("stockbatches: state transition failed: no matching row or race condition")
	}
	a.Status = ArtifactStateVerified
	return nil
}
func (m *mapBatchRepo) MarkArtifactFailed(_ context.Context, id string, status ArtifactState, lastError string) error {
	a, ok := m.artifacts[id]
	if !ok || a.Status != ArtifactStateExtracting {
		return errors.New("stockbatches: state transition failed: no matching row or race condition")
	}
	a.Status = status
	a.LastError = lastError
	return nil
}
func (m *mapBatchRepo) MarkGroupSucceeded(_ context.Context, _ string, _ int) error {
	return nil
}
func (m *mapBatchRepo) MarkBatchSucceeded(_ context.Context, _ string, _ int) error {
	return nil
}
func (m *mapBatchRepo) FindIncompleteArtifacts(_ context.Context, _ string, _ int) ([]StockArtifact, error) {
	return nil, nil
}

var _ StockBatchRepository = (*mapBatchRepo)(nil)

func resumeRow(id string, status ArtifactState) *StockArtifact {
	return &StockArtifact{ID: id, Status: status, DriveFileID: "drive-" + id, DriveFolderID: "folder-1", DriveLink: "https://drive.google.com/file/d/drive-" + id + "/view"}
}

// TestClaimArtifactForExtract_ReclaimsOrphanedExtracting pins the retry
// poison fix: an EXTRACTING row stranded by a dead attempt is reconciled
// and claimed instead of failing the whole run on its first clip.
func TestClaimArtifactForExtract_ReclaimsOrphanedExtracting(t *testing.T) {
	repo := newMapBatchRepo(resumeRow("a:clip:0", ArtifactStateExtracting))
	require.NoError(t, claimArtifactForExtract(context.Background(), repo, "a:clip:0"))
	got, err := repo.GetArtifact(context.Background(), "a:clip:0")
	require.NoError(t, err)
	require.Equal(t, ArtifactStateExtracting, got.Status)
	require.Equal(t, 1, got.Attempts, "reclaim must bump attempts so maxAttempts still bounds retries")
}

// TestClaimArtifactForExtract_KeepsDurableProgress pins that rows carrying
// completed work are left untouched by the claim: no state churn, no
// attempts bump, no error.
func TestClaimArtifactForExtract_KeepsDurableProgress(t *testing.T) {
	for _, st := range []ArtifactState{ArtifactStateExtracted, ArtifactStatePublished, ArtifactStateVerified} {
		repo := newMapBatchRepo(resumeRow("a:clip:0", st))
		require.NoError(t, claimArtifactForExtract(context.Background(), repo, "a:clip:0"), "status %s", st)
		got, err := repo.GetArtifact(context.Background(), "a:clip:0")
		require.NoError(t, err)
		require.Equal(t, st, got.Status, "durable progress must not churn")
		require.Equal(t, 0, got.Attempts)
	}
}

// TestClaimArtifactForExtract_NormalClaimAndMissingRow pins the unchanged
// contracts: PLANNED/RETRY_WAIT claim, missing row stays fail-closed.
func TestClaimArtifactForExtract_NormalClaimAndMissingRow(t *testing.T) {
	repo := newMapBatchRepo(resumeRow("a:clip:0", ArtifactStatePlanned))
	require.NoError(t, claimArtifactForExtract(context.Background(), repo, "a:clip:0"))
	got, _ := repo.GetArtifact(context.Background(), "a:clip:0")
	require.Equal(t, ArtifactStateExtracting, got.Status)

	require.Error(t, claimArtifactForExtract(context.Background(), repo, "a:clip:missing"),
		"missing row must stay fail-closed (prepareBatchState owns creation)")
}

// TestPublishedRowReusable pins the reuse gate: only completed publications
// with a Drive file ID skip the upload pool.
func TestPublishedRowReusable(t *testing.T) {
	ctx := context.Background()
	_, _, _, ok, err := publishedRowReusable(ctx, nil, "x")
	require.NoError(t, err)
	require.False(t, ok, "nil repo must degrade to normal upload")

	repo := newMapBatchRepo(
		resumeRow("pub", ArtifactStatePublished),
		resumeRow("ver", ArtifactStateVerified),
		&StockArtifact{ID: "pub-nofile", Status: ArtifactStatePublished},
		resumeRow("ext", ArtifactStateExtracted),
		resumeRow("ing", ArtifactStateExtracting),
	)
	fileID, folderID, _, ok, err := publishedRowReusable(ctx, repo, "pub")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "drive-pub", fileID)
	require.Equal(t, "folder-1", folderID)

	_, _, _, ok, err = publishedRowReusable(ctx, repo, "ver")
	require.NoError(t, err)
	require.True(t, ok, "VERIFIED rows reuse too")

	for _, id := range []string{"pub-nofile", "ext", "ing", "missing"} {
		_, _, _, ok, err = publishedRowReusable(ctx, repo, id)
		require.NoError(t, err)
		require.False(t, ok, "%s must upload normally", id)
	}
}

// resumeBoundaryRunner wires a real map-backed repo into the publish
// boundary harness; the upload prep fails if ever invoked, proving the
// resume path never touches the Drive pool.
type resumeBoundaryRunner struct {
	*publishBoundaryRunner
	repo StockBatchRepository
}

func (r *resumeBoundaryRunner) BatchRepository() StockBatchRepository { return r.repo }

// TestPublishCutsSkipsUploadForPublishedRow pins no-duplicate-Drive-files on
// resume: with a PUBLISHED row, publishCuts succeeds WITHOUT invoking the
// upload prep, commits once with the persisted Drive identity, and reports
// the chunk with the row's file ID.
func TestPublishCutsSkipsUploadForPublishedRow(t *testing.T) {
	writer := &boundaryRecordingWriter{}
	base := newPublishBoundaryRunner(writer, failingArtifactPreparation{})
	plans := boundaryPlans()
	sourceID := plans[0].SourceID
	artifactID := StockArtifactID("batch-1", sourceID, 0)
	repo := newMapBatchRepo(resumeRow(artifactID, ArtifactStatePublished))
	runner := &resumeBoundaryRunner{publishBoundaryRunner: base, repo: repo}

	cutPaths, chunks, err := publishCuts(
		context.Background(), runner, sourceID, 0, plans, boundaryCutResult(),
		map[string]int{}, map[string]*timestampGroupBuffer{},
		"root", "folder-root", "group", nil, "batch-1",
	)
	require.NoError(t, err)
	require.Len(t, cutPaths, 1)
	require.Len(t, chunks, 1)
	require.Equal(t, "drive-"+artifactID, chunks[0].RemoteFileID,
		"resumed chunk must carry the persisted Drive file, not a fresh upload")
	require.Equal(t, 1, writer.calls, "resumed clip must still commit once (idempotent upsert)")
	require.NotEmpty(t, writer.clips[0].LocalPath(), "commit must carry a location")
}
