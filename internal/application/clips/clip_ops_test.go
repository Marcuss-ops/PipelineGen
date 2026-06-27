// Package clips (test) — clip_ops_test.go.
//
// PR 3 (June 2026 — codex/clips-ops-cutover) unit tests for
// ClipOpsService. The six ports the service takes
// (SourceResolver / VoiceoverRepository / ImageRepository /
// ClipDriveUploader / CleanupService / JobsService) are stubbed
// with minimal compilable defaults; the tests pin behavioural
// contracts (deep-mode enqueues system.cleanup job; fallback to
// synchronous CleanupOrphanFiles when jobs=nil; dry_run never
// deletes; invalid source returned as error; voiceover source
// iterates the voiceover repo; verify with missing local path
// flags local_file_missing; etc.).
package clips

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Port stubs (minimal happy-defaults) ──────────────────────────────────────

type testSourceResolver struct{ repos map[string]ClipRepositoryPort }

func (r *testSourceResolver) ResolveRepo(s string) ClipRepositoryPort {
	if r == nil {
		return nil
	}
	return r.repos[s]
}

type testClipsRepo struct {
	clips []*asset.Asset
}

func (r *testClipsRepo) Upsert(_ context.Context, _ *asset.Asset) error       { return nil }
func (r *testClipsRepo) Get(_ context.Context, _ string) (*asset.Asset, error) {
	return nil, nil
}
func (r *testClipsRepo) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	for _, c := range r.clips {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, nil
}
func (r *testClipsRepo) ListFolders(_ context.Context, _ string) ([]*asset.ClipFolder, error) {
	return nil, nil
}
func (r *testClipsRepo) GetFolder(_ context.Context, _ string) (*asset.ClipFolder, error) {
	return nil, nil
}
func (r *testClipsRepo) GetFolderChildren(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *testClipsRepo) ListByFolderID(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *testClipsRepo) ListByFolderPath(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *testClipsRepo) DeleteFolder(_ context.Context, _ string) error         { return nil }
func (r *testClipsRepo) BulkAddTags(_ context.Context, _, _ []string) error    { return nil }
func (r *testClipsRepo) BulkRemoveTags(_ context.Context, _, _ []string) error { return nil }
func (r *testClipsRepo) ListClipsPaged(_ context.Context, _ string, _, _ int, _ string) ([]*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	out := make([]*asset.Asset, 0, len(r.clips))
	return append(out, r.clips...), nil
}
func (r *testClipsRepo) FindClipsByHash(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}

type testVoiceoverRepo struct{ records map[string]*ClipVoiceoverRecordDTO }

func (r *testVoiceoverRepo) GetByID(_ context.Context, id string) (*ClipVoiceoverRecordDTO, error) {
	if r == nil {
		return nil, nil
	}
	return r.records[id], nil
}
func (r *testVoiceoverRepo) ListAll(_ context.Context) ([]*ClipVoiceoverRecordDTO, error) {
	if r == nil {
		return nil, nil
	}
	out := make([]*ClipVoiceoverRecordDTO, 0, len(r.records))
	for _, rec := range r.records {
		out = append(out, rec)
	}
	return out, nil
}
func (r *testVoiceoverRepo) Upsert(_ context.Context, _ *ClipVoiceoverRecordDTO) error {
	return nil
}

type testImagesRepo struct{ images []*asset.ImageAsset }

func (r *testImagesRepo) ListAll(_ context.Context) ([]*asset.ImageAsset, error) {
	if r == nil {
		return nil, nil
	}
	return r.images, nil
}

type testDriveUploader struct{ md5ByFileID map[string]string }

func (d *testDriveUploader) GetOrCreateFolder(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (d *testDriveUploader) GetFolderName(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (d *testDriveUploader) TrashFolder(_ context.Context, _ string) error  { return nil }
func (d *testDriveUploader) DeleteFolder(_ context.Context, _ string) error { return nil }
func (d *testDriveUploader) UploadFile(_ context.Context, _, _, _ string) (*ClipUploadResultDTO, error) {
	return &ClipUploadResultDTO{}, nil
}
func (d *testDriveUploader) UploadFileWithDescription(_ context.Context, _, _, _, _ string) (*ClipUploadResultDTO, error) {
	return &ClipUploadResultDTO{}, nil
}
func (d *testDriveUploader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}
func (d *testDriveUploader) GetFileMD5(_ context.Context, fileID string) (string, error) {
	if d == nil || d.md5ByFileID == nil {
		return "", nil
	}
	return d.md5ByFileID[fileID], nil
}
func (d *testDriveUploader) GetFileMeta(_ context.Context, _ string) (*ClipDriveFileMetaDTO, error) {
	return &ClipDriveFileMetaDTO{}, nil
}
func (d *testDriveUploader) TrashFile(_ context.Context, _ string) error              { return nil }
func (d *testDriveUploader) ListFiles(_ context.Context, _ string) ([]ClipDriveFileDTO, error) {
	return nil, nil
}

type testCleanupPort struct {
	deleteCalls []string
	cleanupHits int
}

func (c *testCleanupPort) CleanupOrphanFiles(_ context.Context, _ string, _ bool) (int, error) {
	if c == nil {
		return 0, nil
	}
	c.cleanupHits++
	return 0, nil
}
func (c *testCleanupPort) DeleteClip(_ context.Context, source, clipID string, _ bool) error {
	if c == nil {
		return nil
	}
	c.deleteCalls = append(c.deleteCalls, source+":"+clipID)
	return nil
}

type testJobsPort struct {
	enqueued []JobsEnqueueRequest
	nextID   string
	err      error
}

// Enqueue returns (nextID, err) verbatim — no auto-fill. Tests that
// need a sentinel "empty ID" return value (TestClipOps_Reconcile_
// EmptyJobID_ReturnsError) construct a testJobsPort{nextID: ""} and
// expect Enqueue to return JobsEnqueueResponse{ID: ""}, which the
// service then translates into "empty job id" wrapped error.
func (j *testJobsPort) Enqueue(_ context.Context, req JobsEnqueueRequest) (*JobsEnqueueResponse, error) {
	if j == nil {
		return nil, nil
	}
	if j.err != nil {
		return nil, j.err
	}
	j.enqueued = append(j.enqueued, req)
	return &JobsEnqueueResponse{ID: j.nextID}, nil
}

// ── Subtests ─────────────────────────────────────────────────────────────────

// TestClipOps_Cleanup_Deep_EnqueuesSystemCleanupJob pins the
// deep-mode happy path: source="all" + deep=true → jobs.Enqueue
// receives a "system.cleanup" job with the stable
// "system_maintenance_manual" ActiveKey, the cleanup port is
// NOT called (deep path skips synchronous cleanup).
func TestClipOps_Cleanup_Deep_EnqueuesSystemCleanupJob(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-001"}
	cleanup := &testCleanupPort{}
	svc := NewClipOpsService(nil, nil, nil, nil, cleanup, jobs, nil)

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, "job-001", report.JobID)
	require.Equal(t, "system cleanup job enqueued", report.Message)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
	require.Equal(t, "system_maintenance_manual", jobs.enqueued[0].ActiveKey)
	require.Equal(t, 0, cleanup.cleanupHits, "deep-mode must NOT fall through to CleanupOrphanFiles")
}

// TestClipOps_Cleanup_DeepDryRun_ActiveKeySuffix pins the
// ActiveKey suffix when DryRun=true combined with deep-mode.
func TestClipOps_Cleanup_DeepDryRun_ActiveKeySuffix(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-002"}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)

	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system_maintenance_manual_dry", jobs.enqueued[0].ActiveKey)
}

// TestClipOps_Cleanup_DeepNoJobs_FallsBackSynchronous pins the
// fallback path: when jobs=nil but cleanup port is wired, the
// service runs synchronous CleanupOrphanFiles.
func TestClipOps_Cleanup_DeepNoJobs_FallsBackSynchronous(t *testing.T) {
	cleanup := &testCleanupPort{}
	svc := NewClipOpsService(nil, nil, nil, nil, cleanup, nil, nil)

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
		DryRun: false,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, 1, cleanup.cleanupHits)
	require.Contains(t, report.Message, "synchronously")
}

// TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError
// pins the early-return on unrecognised source.
func TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "not-a-source",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid source")
}

// TestClipOps_Cleanup_YoutubeDryRun_ReportsOrphanWithoutDelete
// pins the per-source non-deep path: orphan clip (no local file)
// is reported in Items but NOT deleted when DryRun=true.
func TestClipOps_Cleanup_YoutubeDryRun_ReportsOrphanWithoutDelete(t *testing.T) {
	clip := &asset.Asset{ID: "yt-orphan", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &testClipsRepo{clips: []*asset.Asset{clip}}
	resolver := &testSourceResolver{repos: map[string]ClipRepositoryPort{"youtube": repo}}
	cleanup := &testCleanupPort{}
	svc := NewClipOpsService(resolver, nil, nil, nil, cleanup, nil, nil)

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "youtube",
		DryRun: true,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Len(t, report.Items, 1, "should identify the orphan")
	require.Equal(t, "yt-orphan", report.Items[0].ID)
	require.Empty(t, cleanup.deleteCalls, "dry_run must NOT delete")
}

// TestClipOps_Cleanup_VoiceoverSource_IteratesRecords pins the
// voiceover-source branch in Cleanup: voiceoverRepo.ListAll is
// the source of truth; each orphan is reported.
func TestClipOps_Cleanup_VoiceoverSource_IteratesRecords(t *testing.T) {
	rec := &ClipVoiceoverRecordDTO{
		ID:        "vo-1",
		Filename:  "foo.wav",
		LocalPath: "/nonexistent/foo.wav",
	}
	voiceover := &testVoiceoverRepo{records: map[string]*ClipVoiceoverRecordDTO{"vo-1": rec}}
	svc := NewClipOpsService(nil, voiceover, nil, nil, &testCleanupPort{}, nil, nil)

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "voiceover",
		DryRun: true,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Len(t, report.Items, 1)
	require.Equal(t, "vo-1", report.Items[0].ID)
}

// TestClipOps_Verify_EmptyClipID_ReturnsFalseOK pins the early
// return when clipID="" — report.OK is set to false.
func TestClipOps_Verify_EmptyClipID_ReturnsFalseOK(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, nil, nil, nil)
	report := svc.Verify(context.Background(), "youtube", "")
	require.NotNil(t, report)
	require.False(t, report.OK)
}

// TestClipOps_Verify_InvalidSource_AddsInvalidSourceIssue pins
// the unknown-source marker.
func TestClipOps_Verify_InvalidSource_AddsInvalidSourceIssue(t *testing.T) {
	resolver := &testSourceResolver{repos: map[string]ClipRepositoryPort{}}
	svc := NewClipOpsService(resolver, nil, nil, nil, nil, nil, nil)
	report := svc.Verify(context.Background(), "not-a-source", "x-1")
	require.NotNil(t, report)
	require.False(t, report.OK)
	require.Contains(t, report.Issues, "invalid_source")
}

// TestClipOps_Verify_VoiceoverSource_UsesVoiceoverRepo pins the
// voiceover-source Verify branch — voiceoverRepo.GetByID is the
// source of truth; DB = true on hit.
func TestClipOps_Verify_VoiceoverSource_UsesVoiceoverRepo(t *testing.T) {
	rec := &ClipVoiceoverRecordDTO{
		ID:       "vo-2",
		Filename: "bar.wav",
	}
	voiceover := &testVoiceoverRepo{records: map[string]*ClipVoiceoverRecordDTO{"vo-2": rec}}
	svc := NewClipOpsService(nil, voiceover, nil, nil, &testCleanupPort{}, nil, nil)

	report := svc.Verify(context.Background(), "voiceover", "vo-2")
	require.NotNil(t, report)
	require.Equal(t, "voiceover", report.Source)
	require.Equal(t, "vo-2", report.ClipID)
}

// TestClipOps_Verify_DriveUnavailable_HashMissingIssue pins the
// hash-recovery failure path — when Drive doesn't return a
// usable MD5, "hash_missing" appears in Issues.
func TestClipOps_Verify_DriveUnavailable_HashMissingIssue(t *testing.T) {
	rec := &ClipVoiceoverRecordDTO{
		ID:          "vo-3",
		Filename:    "baz.wav",
		LocalPath:   "/this/path/does/not/exist/baz.wav",
		DriveLink:   "https://drive.google.com/file/d/missing",
		DriveFileID: "missing",
	}
	voiceover := &testVoiceoverRepo{records: map[string]*ClipVoiceoverRecordDTO{"vo-3": rec}}
	svc := NewClipOpsService(nil, voiceover, nil, nil, &testCleanupPort{}, nil, nil)

	report := svc.Verify(context.Background(), "voiceover", "vo-3")
	require.NotNil(t, report)
	require.Contains(t, report.Issues, "local_file_missing", "path doesn't exist on disk")
	require.Contains(t, report.Issues, "hash_missing", "Drive uploaded empty MD5")
}

// ── Reconcile subtests (PR 4, June 2026 — codex/clips-reconcile-real) ────

// TestClipOps_Reconcile_EnqueuesCatalogSyncJob pins the happy path:
// JobsServicePort wired → service emits a "catalog.sync" job with the
// payload {source, folder_id, fix, dry_run} mirroring the request and an
// ActiveKey deterministic on the same tuple. Returns *ReconcileStarted
// with JobID + ActiveKey.
func TestClipOps_Reconcile_EnqueuesCatalogSyncJob(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-recon-001"}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)

	started, err := svc.Reconcile(context.Background(), ReconcileCommand{
		Source:   "youtube",
		FolderID: "drive-folder-abc",
		Fix:      true,
		DryRun:   false,
	})
	require.NoError(t, err)
	require.NotNil(t, started)
	require.Equal(t, "job-recon-001", started.JobID)
	require.Equal(t, "reconcile_youtube_drive-folder-abc_true_false", started.ActiveKey)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, job.TypeCatalogSync, jobs.enqueued[0].Type)
	require.Equal(t, "youtube", jobs.enqueued[0].Payload["source"])
	require.Equal(t, "drive-folder-abc", jobs.enqueued[0].Payload["folder_id"])
	require.Equal(t, true, jobs.enqueued[0].Payload["fix"])
	require.Equal(t, false, jobs.enqueued[0].Payload["dry_run"])
}

// TestClipOps_Reconcile_QueueUnavailable_NoJobs pins the 503 path:
// JobsServicePort=nil composition bug → sentinel ErrQueueUnavailable.
func TestClipOps_Reconcile_QueueUnavailable_NoJobs(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, nil, nil)
	started, err := svc.Reconcile(context.Background(), ReconcileCommand{Source: "youtube"})
	require.Error(t, err)
	require.Nil(t, started)
	require.ErrorIs(t, err, ErrQueueUnavailable)
}

// TestClipOps_Reconcile_EnqueueFailure_ReturnsWrappedError pins the
// broker-rejection path: jobs.Enqueue returns err → service wraps and
// returns it (no sentinel match).
func TestClipOps_Reconcile_EnqueueFailure_ReturnsWrappedError(t *testing.T) {
	jobs := &testJobsPort{err: errors.New("broker down")}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)
	started, err := svc.Reconcile(context.Background(), ReconcileCommand{Source: "youtube"})
	require.Error(t, err)
	require.Nil(t, started)
	require.Contains(t, err.Error(), "enqueue reconcile job")
	require.Contains(t, err.Error(), "broker down")
	require.NotErrorIs(t, err, ErrQueueUnavailable, "broker-rejection is NOT a queue-unavailable")
}

// TestClipOps_Reconcile_EmptyJobID_ReturnsError pins the empty-id
// safety net: Enqueue returns JobID="" → service returns a wrapped error.
func TestClipOps_Reconcile_EmptyJobID_ReturnsError(t *testing.T) {
	jobs := &testJobsPort{nextID: ""}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)
	started, err := svc.Reconcile(context.Background(), ReconcileCommand{Source: "youtube"})
	require.Error(t, err)
	require.Nil(t, started)
	require.Contains(t, err.Error(), "empty job id")
}

// TestClipOps_Reconcile_ActiveKey_Deterministic pins the
// cross-process-deduplication invariant: the same ReconcileCommand
// produces the same ActiveKey; distinct commands produce distinct keys.
func TestClipOps_Reconcile_ActiveKey_Deterministic(t *testing.T) {
	a := reconcileActiveKey(ReconcileCommand{Source: "youtube", FolderID: "F", Fix: true, DryRun: false})
	b := reconcileActiveKey(ReconcileCommand{Source: "youtube", FolderID: "F", Fix: true, DryRun: false})
	c := reconcileActiveKey(ReconcileCommand{Source: "youtube", FolderID: "F", Fix: false, DryRun: false})
	d := reconcileActiveKey(ReconcileCommand{Source: "artlist", FolderID: "F", Fix: true, DryRun: false})
	e := reconcileActiveKey(ReconcileCommand{Source: "youtube", FolderID: "G", Fix: true, DryRun: false})
	require.Equal(t, a, b, "tuple equality → key equality")
	require.NotEqual(t, a, c, "Fix flips the key")
	require.NotEqual(t, a, d, "Source change flips the key")
	require.NotEqual(t, a, e, "FolderID change flips the key")
}

// TestClipOps_Reconcile_NoFolderID_FolderIDEmptyPayload pins the
// shape contract: FolderID="" → payload.folder_id is the empty
// string (NOT omitted) so the catalogsync dispatcher can route to
// SyncSource/SyncAll correctly.
func TestClipOps_Reconcile_NoFolderID_FolderIDEmptyPayload(t *testing.T) {
	jobs := &testJobsPort{nextID: "j-2"}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)
	_, err := svc.Reconcile(context.Background(), ReconcileCommand{Source: "youtube"})
	require.NoError(t, err)
	require.Len(t, jobs.enqueued, 1)
	folderID, ok := jobs.enqueued[0].Payload["folder_id"].(string)
	require.True(t, ok, "folder_id should be a string even when empty")
	require.Equal(t, "", folderID)
	require.Equal(t, "reconcile_youtube__false_false", jobs.enqueued[0].ActiveKey)
}
