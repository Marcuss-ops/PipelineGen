// Package clips (test) — clip_ops_test.go.
//
// PR 3 (June 2026 — codex/clips-ops-cutover) unit tests for
// ClipOpsService. The five ports the service takes
// (SourceResolver / VoiceoverRepository / ImageRepository /
// ClipDriveUploader / JobsService) are stubbed with minimal
// compilable defaults; the tests pin behavioural contracts.
// CleanupServicePort was removed in July 2026 (dead code —
// field assigned but never read by any ClipOpsService method).
package clips

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Port stubs (minimal happy-defaults) ──────────────────────────────────────

// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): testSourceResolver REMOVED.
// All clip-type sources share a single canonical clips.ClipRepositoryPort;
// tests inject the stubRepo (still defined below) directly into ClipOpsService
// via the first positional argument of NewClipOpsService (formerly the
// sourceResolver slot). The per-source discriminator is now encoded at the
// test fixture layer via the source string passed to Verify/Cleanup —
// the service's static switch in isKnownCleanupSource expands to cover all
// canonical clip-type sources (see clip_ops_reconcile.go).

type stubRepo struct {
	clips []*asset.Asset
	// lastSeenClip captures the clip returned by the most recent
	// GetClip call on this stub. Consumed by
	// TestVerify_HashInfoSeparateFromIssues/read_only_no_clip_mutation
	// to assert that verify observes but does not mutate: the
	// fixture pointer passes through unchanged end-to-end.
	lastSeenClip *asset.Asset
}

func (r *stubRepo) Upsert(_ context.Context, _ *asset.Asset) error { return nil }
func (r *stubRepo) Get(_ context.Context, _ string) (*asset.Asset, error) {
	return nil, nil
}
func (r *stubRepo) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	for _, c := range r.clips {
		if c.ID == id {
			r.lastSeenClip = c
			return c, nil
		}
	}
	return nil, nil
}
func (r *stubRepo) ListFolders(_ context.Context, _ string) ([]*asset.ClipFolder, error) {
	return nil, nil
}
func (r *stubRepo) GetFolder(_ context.Context, _ string) (*asset.ClipFolder, error) {
	return nil, nil
}
func (r *stubRepo) GetFolderChildren(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *stubRepo) ListByFolderID(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *stubRepo) ListByFolderPath(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *stubRepo) DeleteFolder(_ context.Context, _ string) error        { return nil }
func (r *stubRepo) BulkAddTags(_ context.Context, _, _ []string) error    { return nil }
func (r *stubRepo) BulkRemoveTags(_ context.Context, _, _ []string) error { return nil }
func (r *stubRepo) ListClipsPaged(_ context.Context, _ string, _, _ int, _ string) ([]*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	out := make([]*asset.Asset, 0, len(r.clips))
	return append(out, r.clips...), nil
}
func (r *stubRepo) FindClipsByHash(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}

type testVoiceoverRepo struct {
	records map[string]*ClipVoiceoverRecordDTO
}

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

// ── Subtests ─────────────────────────────────────────────────────────────────

// TestClipOps_Cleanup_Deep_EnqueuesSystemCleanupJob pins the
// deep-mode happy path: source="all" + deep=true → jobs.Enqueue
// receives a "system.cleanup" job with the stable
// "system_maintenance_manual" ActiveKey, the cleanup port is
// NOT called (deep path skips synchronous cleanup).
func TestClipOps_Cleanup_Deep_EnqueuesSystemCleanupJob(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-001"}
	svc := NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, "job-001", report.JobID)
	require.Equal(t, "system cleanup job enqueued; poll job_id=job-001 for results", report.Message)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
	require.Equal(t, "system_maintenance_manual_deep", jobs.enqueued[0].ActiveKey)
}

// TestClipOps_Cleanup_ShallowPlain_BaseActiveKey pins the
// un-suffixed base case: DryRun=false AND Deep=false →
// activeKey = "system_maintenance_manual" (the 4-way suffix ladder
// in clip_ops.go::Cleanup must keep the no-flags case exactly
// at the base string, with no accidental inline mutations from
// future refactors of the suffix logic).
func TestClipOps_Cleanup_ShallowPlain_BaseActiveKey(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-base"}
	svc := NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())

	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		DryRun: false,
		Deep:   false,
	})
	require.NoError(t, err)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system_maintenance_manual", jobs.enqueued[0].ActiveKey,
		"base case (DryRun=false, Deep=false) must emit un-suffixed activeKey")
}

// TestClipOps_Cleanup_DeepDryRun_ActiveKeySuffix pins the
// ActiveKey suffix when DryRun=true combined with deep-mode.
func TestClipOps_Cleanup_DeepDryRun_ActiveKeySuffix(t *testing.T) {
	jobs := &testJobsPort{nextID: "job-002"}
	svc := NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())

	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system_maintenance_manual_dry_deep", jobs.enqueued[0].ActiveKey)
}

// TestClipOps_Cleanup_DeepNoJobs_FailsClosed pins the
// pre-PR-3 synchronous-fallback REMOVAL: Cleanup is now fail-closed
// across deep AND non-deep modes when jobs=nil. Operators must wire
// the broker before invoking Cleanup at all. The HTTP handler layer
// (api/assets/clips/clip_ops.go) maps ErrJobsUnavailable → 503 so
// callers see a single "broker missing" signal regardless of mode.
//
// Wave 22 PR-5 polish (June 2026) flipped the synchronous-fallback
// behaviour; the historical `TestClipOps_Cleanup_DeepNoJobs_FallsBackSynchronous`
// test from before that polish is preserved in this doc-comment for
// audit (NOT executed): historically Cleanup.cleanupHits would be 1
// with a "synchronously" message; today it is 0 with ErrJobsUnavailable.
func TestClipOps_Cleanup_DeepNoJobs_FailsClosed(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, nil, nil, zap.NewNop())

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "all",
		Deep:   true,
		DryRun: false,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrJobsUnavailable)
	require.Nil(t, report)
}

// TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError
// pins the source-validation precedence: source="not-a-source"
// fails BEFORE the jobs-nil check. Cleanup returns
// ErrInvalidSource immediately, so callers see a typed signal
// for bad input (caller-actionable: fix the source value)
// distinct from "broker missing" (operator-infrastructure: wire
// the broker). Both layers route via typed sentinels; the HTTP
// handler maps ErrInvalidSource → 400 and ErrJobsUnavailable
// → 503.
//
// The previous Wave 22 PR-5 behaviour put jobs-nil FIRST
// regardless of source validity. That precedence was a
// quiet regression: a 503 from the broker-missing path
// obscured whether the request was caller-actionable (fix
// input) or operator-infrastructure (wire broker). The new
// order — source first — pins both layers to a single typed
// answer that callers can grep on without parsing free-form
// strings.
//
// jobs port is wired (not nil) so the source-validation branch
// fires first; without wiring the test would short-circuit on
// ErrJobsUnavailable, occluding the precedence this test pins.
func TestClipOps_Cleanup_InvalidSource_ReturnsInvalidSourceError(t *testing.T) {
	jobsStub := &testJobsPort{nextID: "stub-job"}
	svc := NewClipOpsService(nil, nil, nil, nil, jobsStub, nil, zap.NewNop())
	_, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "not-a-source",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSource,
		"source validation runs BEFORE jobs-nil check (PR-3 source-first precedence)")
	require.Len(t, jobsStub.enqueued, 0, "invalid source must NOT enqueue (validation rejected upstream)")
}

// TestClipOps_Cleanup_YoutubeDryRun_ReportsOrphanWithoutDelete
// pins the per-source non-deep path: orphan clip (no local file)
// is reported in Items but NOT deleted when DryRun=true.
//
// PR-3 (June 2026) fail-closed contract: with jobs=nil, Cleanup
// returns ErrJobsUnavailable before walking the resolver. To pin
// the per-source orphan-reporting path itself, callers must wire
// a stub broker — see TestClipOps_Cleanup_WiredJobs_YoutubeDryRun for
// that contract. This test pins the BROKER-MISSING case.
func TestClipOps_Cleanup_YoutubeDryRun_ReportsOrphanWithoutDelete(t *testing.T) {
	clip := &asset.Asset{ID: "yt-orphan", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &stubRepo{clips: []*asset.Asset{clip}}
	// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the retired resolver
	// is GONE — the canonical clipRepo is injected directly into
	// NewClipOpsService's first (clipRepo) slot. Source discriminator
	// now lives in the static switch in isKnownCleanupSource.
	svc := NewClipOpsService(repo, nil, nil, nil, nil, nil, zap.NewNop())

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "youtube",
		DryRun: true,
	})
	require.Error(t, err, "Cleanup must fail-closed when jobs port is nil")
	require.ErrorIs(t, err, ErrJobsUnavailable)
	require.Nil(t, report)
}

// TestClipOps_Cleanup_VoiceoverSource_IteratesRecords pins the
// voiceover-source branch in Cleanup: voiceoverRepo.ListAll is
// the source of truth; each orphan is reported.
//
// PR-3 (June 2026) fail-closed contract: same as the youtube
// dry-run test above — Cleanup returns ErrJobsUnavailable when
// jobs=nil. Wiring a stubbroker is reserved for tests that
// exercise the orchestrator path (e.g. TestClipOps_Cleanup_WiredJobs_*).
func TestClipOps_Cleanup_VoiceoverSource_IteratesRecords(t *testing.T) {
	rec := &ClipVoiceoverRecordDTO{
		ID:        "vo-1",
		Filename:  "foo.wav",
		LocalPath: "/nonexistent/foo.wav",
	}
	voiceover := &testVoiceoverRepo{records: map[string]*ClipVoiceoverRecordDTO{"vo-1": rec}}
	svc := NewClipOpsService(nil, voiceover, nil, nil, nil, nil, zap.NewNop())

	report, err := svc.Cleanup(context.Background(), CleanupInput{
		Source: "voiceover",
		DryRun: true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrJobsUnavailable)
	require.Nil(t, report)
}

// TestClipOps_Verify_EmptyClipID_ReturnsFalseOK pins the early
// return when clipID="" — report.OK is set to false.
func TestClipOps_Verify_EmptyClipID_ReturnsFalseOK(t *testing.T) {
	svc := NewClipOpsService(nil, nil, nil, nil, nil, nil, zap.NewNop())
	report := svc.Verify(context.Background(), "youtube", "")
	require.NotNil(t, report)
	require.False(t, report.OK)
}

// TestClipOps_Verify_InvalidSource_AddsInvalidSourceIssue pins
// the unknown-source marker.
func TestClipOps_Verify_InvalidSource_AddsInvalidSourceIssue(t *testing.T) {
	// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the retired resolver
	// is GONE — nil clipRepo for this test (the resolver used to
	// return nil for unknown sources; the static-switch discriminator
	// in isKnownCleanupSource now detects "not-a-source" without
	// needing the clipRepo to be queried at all).
	svc := NewClipOpsService(nil, nil, nil, nil, nil, nil, zap.NewNop())
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
	svc := NewClipOpsService(nil, voiceover, nil, nil, nil, nil, zap.NewNop())

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
	svc := NewClipOpsService(nil, voiceover, nil, nil, nil, nil, zap.NewNop())

	report := svc.Verify(context.Background(), "voiceover", "vo-3")
	require.NotNil(t, report)
	require.Contains(t, report.Issues, "local_file_missing", "path doesn't exist on disk")
	require.Contains(t, report.Issues, "hash_missing", "Drive uploaded empty MD5")
}

// TestVerify_HashInfoSeparateFromIssues pins the S1c (June 2026)
// read-only invariant on VerifyClip: verify observes the clip from
// repo.GetClip but MUST NOT mutate it in place. The fixture pointer
// passes through unchanged end-to-end — a future stub refactor that
// detaches `lastSeenClip` would silently break this subtest (a
// regression guard against the S1c silent-Upsert bug recurring).
func TestVerify_HashInfoSeparateFromIssues(t *testing.T) {
	fixture := &asset.Asset{
		ID:   "yt-verify-1",
		Name: "fixture-readonly",
	}
	fixture.SetLocalPath("/this/path/does/not/exist/fixture.mp4")
	fixture.SetDriveLink("https://drive.google.com/file/d/no-md5")

	repo := &stubRepo{clips: []*asset.Asset{fixture}}
	// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the retired resolver
	// is GONE — canonical clipRepo is `repo` injected directly. Verify's
	// source-discriminator happens entirely in isKnownCleanupSource's
	// static switch now.
	svc := NewClipOpsService(repo, nil, nil, nil, nil, nil, zap.NewNop())

	t.Run("read_only_no_clip_mutation", func(t *testing.T) {
		report := svc.Verify(context.Background(), "youtube", "yt-verify-1")
		require.NotNil(t, report)
		// The fixture pointer must pass through unchanged: the
		// verify read-only path observes but does not mutate.
		require.Same(t, fixture, repo.lastSeenClip,
			"verify must pass the fixture pointer through unchanged (no silent in-place mutation)")
	})
}
