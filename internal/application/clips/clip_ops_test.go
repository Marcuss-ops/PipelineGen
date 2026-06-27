// Package clips (test) - clip_ops_test.go.
//
// PR 3 (June 2026 - codex/clips-ops-cutover) unit tests for
// ClipOpsService. The six ports the service takes
// (SourceResolver / VoiceoverRepository / ImageRepository /
// ClipDriveUploader / CleanupService / JobsService) are stubbed
// with minimal compilable defaults.
//
// PR 5 (June 2026 - codex/clips-cleanup-job) updated assertions:
// the synchronous Cleanup_* tests are replaced with the
// async-enqueue shape. The service now returns *CleanupStarted
// (job_id + active_key + batch_size) - it never returns the
// pre-PR5 *CleanupReport (Items/Checked/Deleted/Summary) shape.
// Behavioural invariants pinned at the service level:
//
//   1. jobs.Enqueue receives an "assets.cleanup" job with a
//      deterministic ActiveKey derived from the CleanupInput
//      tuple.
//   2. The synchronous CleanupOrphanFiles fallback is REMOVED
//      per spec; jobs=nil surfaces ErrQueueUnavailable.
//   3. Invalid source -> ErrInvalidSource (still surfaced via
//      HTTP 400 via mapClipOpsError).
//
// The 6 spec-matrix tests (resume-from-checkpoint / cancel /
// dry_run / retry-idempotent / asset-added-mid-scan /
// partial-Drive-error) live in a followup PR's
// internal/application/assets/cleanup/cleaner_test.go against
// the HandlerJob function - this file stays focused on the
// service-level enqueue/dedupe contract.
package clips

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Port stubs (minimal happy-defaults).

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

func (r *testClipsRepo) Upsert(_ context.Context, _ *asset.Asset) error { return nil }
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
func (r *testClipsRepo) DeleteFolder(_ context.Context, _ string) error { return nil }
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
func (d *testDriveUploader) TrashFile(_ context.Context, _ string) error { return nil }
func (d *testDriveUploader) ListFiles(_ context.Context, _ string) ([]ClipDriveFileDTO, error) {
	return nil, nil
}
func (d *testDriveUploader) FileIsNotTrashed(_ context.Context, _ string) (bool, error) {
	return true, nil
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

func (j *testJobsPort) Enqueue(_ context.Context, req JobsEnqueueRequest) (*JobsEnqueueResponse, error) {
	if j == nil {
		return nil, nil
	}
	if j.err != nil {
		return nil, j.err
	}
	j.enqueued = append(j.enqueued, req)
	if j.nextID == "" {
		j.nextID = "stub-job-id"
	}
	return &JobsEnqueueResponse{ID: j.nextID}, nil
}

// Subtests.

// TestClipOps_Cleanup_EnqueuesAssetsCleanupJob pins the
// happy-path: any source + non-deep -> jobs.Enqueue receives an
// "assets.cleanup" job with the canonical ActiveKey
// "cleanup_<source>_<dry>_<checkLocal>_<checkDrive>_<repair>_<delete>_<batchSize>".
// Returns *CleanupStarted with JobID + ActiveKey + BatchSize
// (not the pre-PR5 Items/Checked shape).
func TestClipOps_Cleanup_EnqueuesAssetsCleanupJob(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-001"}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)

	started, err := svc.Cleanup(context.Background(), CleanupInput{Source: "youtube"})
	require.NoError(t, err)
	require.NotNil(t, started)
	require.Equal(t, "job-001", started.JobID)
	require.Equal(t, "cleanup_youtube_false_false_false_false_false_0", started.ActiveKey)
	require.Equal(t, 0, started.BatchSize, "BatchSize 0 means handler will default to 250")
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "assets.cleanup", jobs.enqueued[0].Type)
	require.Equal(t, "cleanup_youtube_false_false_false_false_false_0", jobs.enqueued[0].ActiveKey)
}

// TestClipOps_Cleanup_DryRun_ActiveKeyIncludesFlag pins the
// ActiveKey suffix change when DryRun=true is combined with
// CheckLocal+CheckDrive+Repair+Delete=true and a non-zero batch.
func TestClipOps_Cleanup_DryRun_ActiveKeyIncludesFlag(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-002"}
	svc := NewClipOpsService(nil, nil, nil, nil, &testCleanupPort{}, jobs, nil)

	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source:     "youtube",
		DryRun:     true,
		CheckLocal: true,
		CheckDrive: true,
		Repair:     true,
		Delete:     true,
		BatchSize:  100,
	})
	require.NoError(t, err)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "cleanup_youtube_true_true_true_true_true_100", jobs.enqueued[0].ActiveKey)
	require.Equal(t, 100, jobs.enqueued[0].Payload["batch_size"])
	require.Equal(t, true, jobs.enqueued[0].Payload["dry_run"])
	require.Equal(t, true, jobs.enqueued[0].Payload["check_local"])
	require.Equal(t, true, jobs.enqueued[0].Payload["check_drive"])
	require.Equal(t, true, jobs.enqueued[0].Payload["repair"])
	require.Equal(t, true, jobs.enqueued[0].Payload["delete"])
}

// TestClipOps_Cleanup_NoJobs_ReturnsErrQueueUnavailable pins the
// pre-flight guard: jobs=nil surfaces ErrQueueUnavailable
// (HTTP 503 + code RECONCILE_QUEUE_UNAVAILABLE in the handler).
// The pre-PR5 synchronous CleanupOrphanFiles fallback is REMOVED
// per spec - there is no fallback path.
func TestClipOps_Cleanup_NoJobs_ReturnsErrQueueUnavailable(t *testing.T) {
	cleanup := &testCleanupPort{}
	svc := NewClipOpsService(nil, nil, nil, nil, cleanup, nil, nil)

	started, err := svc.Cleanup(context.Background(), CleanupInput{Source: "youtube"})
	require.Error(t, err)
	require.Nil(t, started)
	require.ErrorIs(t, err, ErrQueueUnavailable)
	require.Equal(t, 0, cleanup.cleanupHits, "pre-PR5 synchronous fallback is REMOVED")
}

// TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError
// pins the early-return on unrecognised source (still surfaced).
func TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, nil, &testJobsPort{nextID: "job-x"}, nil)
	started, err := svc.Cleanup(context.Background(), CleanupInput{Source: "not-a-source"})
	require.Error(t, err)
	require.Nil(t, started)
	require.ErrorIs(t, err, ErrInvalidSource)
}

// TestClipOps_Cleanup_VoiceoverSource_EnqueuesDoesNotPreflight
// pins the voiceover-source branch path: voiceover source is
// accepted (no preflight repo lookup is performed in the new
// async-enqueue shape); jobs.Enqueue receives the payload
// regardless of which side's repo is wired.
func TestClipOps_Cleanup_VoiceoverSource_EnqueuesDoesNotPreflight(t *testing.T) {
	voiceover := &testVoiceoverRepo{records: map[string]*ClipVoiceoverRecordDTO{}}
	svc := NewClipOpsService(nil, voiceover, nil, nil, &testCleanupPort{}, &testJobsPort{nextID: "job-vo"}, nil)

	started, err := svc.Cleanup(context.Background(), CleanupInput{Source: "voiceover", DryRun: true})
	require.NoError(t, err)
	require.NotNil(t, started)
	require.Equal(t, "job-vo", started.JobID)
	require.Equal(t, "cleanup_voiceover_true_false_false_false_false_0", started.ActiveKey)
}

// TestClipOps_Verify_EmptyClipID_ReturnsFalseOK pins the early
// return when clipID="" - report.OK is set to false.
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
// voiceover-source Verify branch - voiceoverRepo.GetByID is the
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
// hash-recovery failure path - when Drive doesn't return a
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
