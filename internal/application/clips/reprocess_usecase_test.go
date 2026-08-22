// Package clips (reprocess_usecase_test.go) — reprocess HTTP contract audit pins.
//
// The POST /api/media/clips/:source/clips/:id/reprocess endpoint exposes
// three flags in the JSON body:
//
//	force         — false = reuse the existing derived rendition when the
//	                clip already has a valid one on disk (no download,
//	                no re-encode, no upload); true = always re-run.
//	upload_drive  — false = skip the canonical Drive publish (SkipPublish);
//	                the local rendition + hash still stand.
//	normalize     — false = skip the ffmpeg normalize (mux/copy only);
//	                omitted (null) = default normalize.
//
// These tests pin that ReprocessUseCase.Execute actually forwards the
// flags into the processor's ProcessInput instead of silently ignoring
// them. The processor-level behavior (normalize=false skips ffmpeg,
// SkipPublish=true skips the Publisher) is pinned in
// internal/infrastructure/media/processor/processor_test.go; this file
// pins the use-case seam between the HTTP contract and the processor.
package clips

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Compile-time port assertions ──────────────────────────────────────
var (
	_ asset.Processor                   = (*fakeReprocessProcessor)(nil)
	_ mutations.AssetMutationDispatcher = (*fakeReprocessDispatcher)(nil)
	_ RemoteAssetReader                 = (*fakeReprocessReader)(nil)
)

// ── Port stubs ─────────────────────────────────────────────────────────

// fakeReprocessProcessor records the ProcessInput it receives so tests
// can assert which flags reached the processor.
type fakeReprocessProcessor struct {
	lastInput *asset.ProcessInput
	callCount int
	result    *asset.ProcessResult
	err       error
}

func (p *fakeReprocessProcessor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	p.lastInput = input
	p.callCount++
	if p.err != nil {
		return nil, p.err
	}
	if p.result != nil {
		return p.result, nil
	}
	return &asset.ProcessResult{
		Status:    "processed",
		LocalPath: "out/result.mp4",
		LegacyFileMD5:  "result-hash",
	}, nil
}

type fakeReprocessRepo struct {
	stub *asset.Asset
	err  error
}

func (r *fakeReprocessRepo) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.stub == nil || r.stub.ID != id {
		return nil, errors.New("fakeReprocessRepo: not found")
	}
	return r.stub, nil
}

func (r *fakeReprocessRepo) Upsert(ctx context.Context, a *asset.Asset) error { return nil }
func (r *fakeReprocessRepo) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *fakeReprocessRepo) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return 0, nil
}
func (r *fakeReprocessRepo) SoftDelete(ctx context.Context, id string) error { return nil }
func (r *fakeReprocessRepo) Restore(ctx context.Context, id string) error    { return nil }
func (r *fakeReprocessRepo) HardDelete(ctx context.Context, id string) error { return nil }
func (r *fakeReprocessRepo) FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error) {
	return nil, nil
}

type fakeReprocessDispatcher struct {
	calledWithAsset *asset.Asset
	calledWithHash  string
	calledCount     int
	returnErr       error
}

func (d *fakeReprocessDispatcher) EnqueueAndIndex(ctx context.Context, a *asset.Asset, contentHash string) error {
	d.calledWithAsset = a
	d.calledWithHash = contentHash
	d.calledCount++
	return d.returnErr
}

func (d *fakeReprocessDispatcher) EnqueueAndRestore(ctx context.Context, assetID string) error {
	return nil
}
func (d *fakeReprocessDispatcher) EnqueueAndDelete(ctx context.Context, assetID string) error {
	return nil
}

// fakeReprocessReader serves clip_drive staging downloads.
type fakeReprocessReader struct {
	body        string
	contentType string
	callCount   int
	err         error
}

func (r *fakeReprocessReader) DownloadFile(ctx context.Context, driveID string) (io.ReadCloser, string, error) {
	r.callCount++
	if r.err != nil {
		return nil, "", r.err
	}
	if r.contentType == "" {
		r.contentType = "video/mp4"
	}
	return io.NopCloser(bytes.NewReader([]byte(r.body))), r.contentType, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// boolPtr is a local pointer helper (the processor package defines its
// own copy in its test file — packages cannot share test helpers).
func boolPtr(b bool) *bool { return &b }

// newReprocessTestUC builds a ReprocessUseCase wired with the fakes.
func newReprocessTestUC(t *testing.T, clip *asset.Asset, proc *fakeReprocessProcessor, reader *fakeReprocessReader) (*ReprocessUseCase, *fakeReprocessRepo, *fakeReprocessDispatcher) {
	t.Helper()
	repo := &fakeReprocessRepo{stub: clip}
	disp := &fakeReprocessDispatcher{}
	uc := NewReprocessUseCase(repo, proc, disp, "clips-root-folder")
	if reader != nil {
		uc.SetRemoteAssetReader(reader)
	}
	return uc, repo, disp
}

// driveBackedClip builds a clip_drive-shaped asset with a real rendition
// file on disk (so the force=false short-circuit can find it).
func driveBackedClip(t *testing.T, id string) (*asset.Asset, string) {
	t.Helper()
	tmpDir := t.TempDir()
	rendition := filepath.Join(tmpDir, "existing.mp4")
	if err := os.WriteFile(rendition, []byte("existing rendition bytes"), 0o644); err != nil {
		t.Fatalf("write rendition: %v", err)
	}
	a := &asset.Asset{ID: id, Name: id + " clip"}
	a.SetDriveFileID("drive-file-1")
	a.SetFolderID("folder-1")
	a.SetLocalPath(rendition)
	a.SetLegacyFileMD5("existing-hash")
	// Canonical clip filename (yt_<videoID>_<start>_<end>_<policy>_<slug>.mp4)
	// as persisted in media_assets.filename by the YouTube pipeline.
	a.Filename = "yt_abc123_0_30_v1_" + id + ".mp4"
	return a, rendition
}

// ── upload_drive=false → SkipPublish=true ─────────────────────────────

func TestReprocessExecute_UploadDriveFalse_SetsSkipPublish(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-upfalse")
	proc := &fakeReprocessProcessor{
		result: &asset.ProcessResult{
			Status: "processed", LocalPath: "out/result.mp4", LegacyFileMD5: "new-hash",
		},
	}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, disp := newReprocessTestUC(t, clip, proc, reader)

	res, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-upfalse",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: false,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.callCount != 1 {
		t.Fatalf("processor.Process called %d times, want 1", proc.callCount)
	}
	if proc.lastInput == nil {
		t.Fatal("processor received nil ProcessInput")
	}
	if !proc.lastInput.SkipPublish {
		t.Errorf("upload_drive=false must set ProcessInput.SkipPublish=true, got false")
	}
	// upload_drive=false must also skip the Drive staging requirement:
	// clip_drive staging still happens (the source file is needed), but
	// the canonical publish must be skipped end-to-end.
	if reader.callCount != 1 {
		t.Errorf("remote reader called %d times, want 1 (staging still required)", reader.callCount)
	}
	if res.DriveLink != "" {
		t.Errorf("DriveLink = %q, want empty when upload_drive=false", res.DriveLink)
	}
	if res.DownloadLink != "" {
		t.Errorf("DownloadLink = %q, want empty when upload_drive=false", res.DownloadLink)
	}
	if disp.calledCount != 1 {
		t.Errorf("dispatcher.EnqueueAndIndex called %d times, want 1 (local rendition still persisted)", disp.calledCount)
	}
}

// ── normalize=false → ProcessInput.Normalize=false ────────────────────

func TestReprocessExecute_NormalizeFalse_PassesFalse(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-normfalse")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, _ := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:    "clip-normfalse",
		Source:    "clip_drive",
		Force:     true,
		Normalize: boolPtr(false),
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.lastInput == nil || proc.lastInput.Normalize == nil {
		t.Fatal("normalize=false must forward a non-nil *bool to ProcessInput.Normalize")
	}
	if *proc.lastInput.Normalize {
		t.Errorf("ProcessInput.Normalize = true, want false when normalize=false in the request")
	}
}

// ── normalize omitted → nil (processor default) ───────────────────────

func TestReprocessExecute_NormalizeOmitted_PassesNil(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-normnil")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, _ := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-normnil",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: true,
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.lastInput == nil {
		t.Fatal("processor received nil ProcessInput")
	}
	if proc.lastInput.Normalize != nil {
		t.Errorf("ProcessInput.Normalize = %v, want nil when the request omits normalize (processor default normalize)", *proc.lastInput.Normalize)
	}
	// Default contract: upload_drive=true → publish enabled.
	if proc.lastInput.SkipPublish {
		t.Errorf("ProcessInput.SkipPublish = true, want false when upload_drive=true")
	}
}

// ── force=false + existing rendition → short-circuit ──────────────────

func TestReprocessExecute_ForceFalse_ReusesExistingRendition(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-forcefalse")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, disp := newReprocessTestUC(t, clip, proc, reader)

	res, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-forcefalse",
		Source:      "clip_drive",
		Force:       false,
		UploadDrive: true,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.callCount != 0 {
		t.Errorf("processor.Process called %d times, want 0 (force=false short-circuit)", proc.callCount)
	}
	if reader.callCount != 0 {
		t.Errorf("remote reader called %d times, want 0 (no Drive download on short-circuit)", reader.callCount)
	}
	if disp.calledCount != 0 {
		t.Errorf("dispatcher called %d times, want 0 (no re-persist on short-circuit)", disp.calledCount)
	}
	if res.Status != "processed" || res.LegacyFileMD5 != "existing-hash" {
		t.Errorf("short-circuit result = {status:%q hash:%q}, want {processed, existing-hash}", res.Status, res.LegacyFileMD5)
	}
	if res.LocalPath != clip.LocalPath() {
		t.Errorf("short-circuit LocalPath = %q, want existing rendition %q", res.LocalPath, clip.LocalPath())
	}
}

// ── force=true + existing rendition → full pipeline still runs ────────

func TestReprocessExecute_ForceTrue_RerunsPipelineDespiteRendition(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-forcetrue")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, disp := newReprocessTestUC(t, clip, proc, reader)

	res, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-forcetrue",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: true,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.callCount != 1 {
		t.Errorf("processor.Process called %d times, want 1 (force=true bypasses the short-circuit)", proc.callCount)
	}
	if reader.callCount != 1 {
		t.Errorf("remote reader called %d times, want 1 (clip_drive always re-stages)", reader.callCount)
	}
	if disp.calledCount != 1 {
		t.Errorf("dispatcher called %d times, want 1 (fresh rendition persisted)", disp.calledCount)
	}
	if disp.calledWithHash != "result-hash" {
		t.Errorf("dispatcher contentHash = %q, want %q (result.LegacyFileMD5)", disp.calledWithHash, "result-hash")
	}
	if res.LegacyFileMD5 != "result-hash" {
		t.Errorf("result.LegacyFileMD5 = %q, want %q", res.LegacyFileMD5, "result-hash")
	}
}

// ── drive identity persistence: DriveFileID + md5 + publish_action ─────
//
// Pins that ReprocessUseCase.Execute tracks the NEWLY published Drive
// file (DriveFileID) and records the Drive-returned md5 + Publisher
// action onto the dispatched clip, instead of leaving drive_file_id
// pointing at the stale pre-reprocess file (the DB↔Drive divergence
// that orphaned every fresh upload).

func TestReprocessExecute_PersistsDriveIdentityFromResult(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-driveid")
	proc := &fakeReprocessProcessor{
		result: &asset.ProcessResult{
			Status:        "processed",
			LocalPath:     "out/result.mp4",
			LegacyFileMD5:      "result-hash",
			DriveFileID:   "new-drive-file-id",
			DriveLink:     "https://drive.google.com/file/d/new-drive-file-id/view",
			DownloadLink:  "https://drive.google.com/uc?id=new-drive-file-id",
			MD5:           "new-md5-checksum",
			PublishAction: "created",
		},
	}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, disp := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-driveid",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: true,
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if disp.calledCount != 1 {
		t.Fatalf("dispatcher called %d times, want 1", disp.calledCount)
	}
	dispatched := disp.calledWithAsset
	if dispatched == nil {
		t.Fatal("dispatcher received nil asset")
	}
	if got := dispatched.DriveFileID(); got != "new-drive-file-id" {
		t.Errorf("DriveFileID = %q, want %q (must track the newly published Drive file)", got, "new-drive-file-id")
	}
	if got := dispatched.GetMetadataString("md5"); got != "new-md5-checksum" {
		t.Errorf("md5 = %q, want %q", got, "new-md5-checksum")
	}
	if got := dispatched.GetMetadataString("publish_action"); got != "created" {
		t.Errorf("publish_action = %q, want %q", got, "created")
	}
}

func TestReprocessExecute_EmptyDriveFieldsDoNotClobber(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-driveempty")
	proc := &fakeReprocessProcessor{
		result: &asset.ProcessResult{
			Status:    "processed",
			LocalPath: "out/result.mp4",
			LegacyFileMD5:  "result-hash",
			// DriveFileID / MD5 / PublishAction intentionally empty:
			// a nil/non-published result must not erase prior values.
		},
	}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, disp := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-driveempty",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: false, // no publish → empty Drive fields
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if disp.calledCount != 1 {
		t.Fatalf("dispatcher called %d times, want 1", disp.calledCount)
	}
	dispatched := disp.calledWithAsset
	if dispatched == nil {
		t.Fatal("dispatcher received nil asset")
	}
	if got := dispatched.DriveFileID(); got != "drive-file-1" {
		t.Errorf("DriveFileID = %q, want prior value %q (empty result must not clobber)", got, "drive-file-1")
	}
	if got := dispatched.GetMetadataString("md5"); got != "" {
		t.Errorf("md5 = %q, want empty when the result carries no Drive md5", got)
	}
	if got := dispatched.GetMetadataString("publish_action"); got != "" {
		t.Errorf("publish_action = %q, want empty when the result carries no action", got)
	}
}

// ── canonical filename forwarding: upload overwrites, never orphans ────

func TestReprocessExecute_ForwardsCanonicalFilename(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-filename")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, _ := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-filename",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: true,
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.lastInput == nil {
		t.Fatal("processor received nil ProcessInput")
	}
	if proc.lastInput.Filename != clip.Filename {
		t.Errorf("ProcessInput.Filename = %q, want canonical clip filename %q (so ConflictOverwrite updates in place)",
			proc.lastInput.Filename, clip.Filename)
	}
}

// ── clip_drive staging: DriveFileID used, never the URL ───────────────

func TestReprocessExecute_ClipDrive_StagesFromDriveFileID(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-staging")
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, _ := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-staging",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: false,
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if reader.callCount != 1 {
		t.Fatalf("remote reader called %d times, want 1", reader.callCount)
	}
	if proc.lastInput == nil {
		t.Fatal("processor received nil ProcessInput")
	}
	if proc.lastInput.LocalPath == "" {
		t.Error("clip_drive reprocess must pass the staged local path to the processor")
	}
	if proc.lastInput.SourceURL != "" {
		t.Errorf("clip_drive reprocess must clear SourceURL (staged file is the source), got %q", proc.lastInput.SourceURL)
	}
}

// ── folder alignment: DestinationYouTubeClip + Group/Subject ───────────
//
// Pins that ReprocessUseCase.Execute routes the Drive upload through the
// canonical YouTubeClip destination (registry-resolved real folder) instead
// of the legacy Artlist destination + stale ParentFolderID.

func TestReprocessExecute_ForwardsYouTubeClipDestination(t *testing.T) {
	t.Parallel()
	clip, _ := driveBackedClip(t, "clip-dest")
	clip.SetFolderPath("Love")              // real Drive folder name (the group segment)
	clip.SetMetadataSourceVideoID("abc123") // YouTube video ID (the subject segment)
	proc := &fakeReprocessProcessor{}
	reader := &fakeReprocessReader{body: "staged bytes"}
	uc, _, _ := newReprocessTestUC(t, clip, proc, reader)

	if _, err := uc.Execute(context.Background(), ReprocessRequest{
		ClipID:      "clip-dest",
		Source:      "clip_drive",
		Force:       true,
		UploadDrive: true,
	}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if proc.lastInput == nil {
		t.Fatal("processor received nil ProcessInput")
	}
	if got := proc.lastInput.Destination; got != "youtube_clip" {
		t.Errorf("ProcessInput.Destination = %q, want %q (canonical YouTubeClip destination)", got, "youtube_clip")
	}
	if got := proc.lastInput.Group; got != "Love" {
		t.Errorf("ProcessInput.Group = %q, want %q (real folder name)", got, "Love")
	}
	if got := proc.lastInput.Subject; got != "abc123" {
		t.Errorf("ProcessInput.Subject = %q, want %q (source video ID)", got, "abc123")
	}
}

// reprocessGroup/reprocessSubject fallback chains — unit pins.

func TestReprocessGroup_PrefersFolderPathOverPlaceholder(t *testing.T) {
	t.Parallel()
	a := &asset.Asset{ID: "yt_abc_1_2_v1", Group: "group", Category: ""}
	a.SetFolderPath("Love")
	if got := reprocessGroup(a); got != "Love" {
		t.Errorf("reprocessGroup = %q, want %q (folder_path is the real name, group is the legacy placeholder)", got, "Love")
	}
}

func TestReprocessGroup_SkipsLegacyGroupPlaceholder(t *testing.T) {
	t.Parallel()
	a := &asset.Asset{ID: "yt_abc_1_2_v1", Group: "group", Category: "Boxe"}
	if got := reprocessGroup(a); got != "Boxe" {
		t.Errorf("reprocessGroup = %q, want %q (skip the legacy %q placeholder, use category)", got, "Boxe", "group")
	}
}

func TestReprocessSubject_DerivesFromClipIDWhenMetadataAbsent(t *testing.T) {
	t.Parallel()
	a := &asset.Asset{ID: "yt_2JFBX65Tsnc_12_22_v1"}
	if got := reprocessSubject(a); got != "2JFBX65Tsnc" {
		t.Errorf("reprocessSubject = %q, want %q (first segment of the canonical clip ID)", got, "2JFBX65Tsnc")
	}
}

func TestReprocessSubject_PrefersSourceVideoID(t *testing.T) {
	t.Parallel()
	a := &asset.Asset{ID: "yt_ignored_12_22_v1"}
	a.SetMetadataSourceVideoID("realVideoID")
	if got := reprocessSubject(a); got != "realVideoID" {
		t.Errorf("reprocessSubject = %q, want %q (persisted source_video_id wins)", got, "realVideoID")
	}
}
