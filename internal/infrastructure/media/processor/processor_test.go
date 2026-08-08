package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	ffmpegtypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Pre-F2.8 failing-path tests (now require Publisher via fakePublisher; nil would panic in NewProcessor) ──

func TestProcessorHandlesNilInput(t *testing.T) {
	p := NewProcessor(nil, nil, nil, zap.NewNop(), ProcessorConfig{}, nil, &fakePublisher{})

	result, err := p.Process(context.Background(), nil)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "ProcessInput")
}

func TestProcessorHandlesYTDLPFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	p := NewProcessor(
		&fakeYTDLP{err: errors.New("yt-dlp boom")},
		&fakeHTTPDownloader{},
		&fakeFFmpeg{},
		zap.NewNop(),
		ProcessorConfig{
			DataDir:  tmp,
			TempDir:  "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{},
		},
		nil,
		&fakePublisher{},
	)

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-1",
		Name:      "test clip",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir: filepath.Join(tmp, "out"),
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "download failed")
}

func TestProcessorHandlesFFmpegFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	ff := &fakeFFmpeg{normalizeErr: errors.New("ffmpeg boom")}

	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		ff,
		zap.NewNop(),
		ProcessorConfig{
			DataDir:  tmp,
			TempDir:  "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{},
		},
		nil,
		&fakePublisher{},
	)

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-1",
		Name:      "test clip",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir: filepath.Join(tmp, "out"),
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.True(t, ff.normalizeCalled)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "process failed")
}

func TestProcessRenditions_ProxyFailureIsTerminal(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	ff := &fakeFFmpeg{proxyErr: errors.New("NVENC encode required and failed: encoder unavailable")}
	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		ff,
		zap.NewNop(),
		ProcessorConfig{DataDir: tmp, TempDir: "tmp", VideoCfg: ffmpeg.NormalizeOptions{}},
		nil,
		&fakePublisher{},
	)

	rawPath := writeStagedFileForTest(t, "staged-video")
	_, err := p.processRenditions(ctx, &asset.ProcessInput{
		ID:        "proxy-failure",
		Name:      "proxy failure",
		OutputDir: filepath.Join(tmp, "out"),
	}, rawPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "preview generation failed")
	assert.Contains(t, err.Error(), "NVENC encode required and failed")
}

func TestProcessorPassesDefaultNormalizePolicyWithoutCodecOverride(t *testing.T) {
	ctx := context.Background()
	localPath := writeStagedFileForTest(t, "staged-bytes")
	ff := &fakeFFmpeg{}
	defaultOpts := ffmpegtypes.DefaultNormalizeOptions(&config.Config{})
	p := NewProcessor(
		&fakeYTDLP{}, &fakeHTTPDownloader{}, ff, zap.NewNop(),
		ProcessorConfig{DataDir: t.TempDir(), TempDir: "tmp", VideoCfg: defaultOpts},
		nil, &fakePublisher{},
	)

	_, err := p.Process(ctx, &asset.ProcessInput{
		ID: "default-policy", Name: "default policy", LocalPath: localPath,
		OutputDir: filepath.Join(t.TempDir(), "out"),
	})
	require.NoError(t, err)
	require.True(t, ff.normalizeCalled)
	assert.Empty(t, ff.lastNormalizeOpts.Policy.Codec,
		"generic processor must not reintroduce libx264 into the default policy")
	assert.Empty(t, ff.lastNormalizeOpts.Codec,
		"legacy codec field must remain empty for FFmpeg runtime resolution")
	assert.Equal(t, "veryfast", ff.lastNormalizeOpts.Policy.Preset)
	assert.Equal(t, 23, ff.lastNormalizeOpts.Policy.CRF)
}

func TestProcessorPreservesResolvedEncoderPolicyDuringNormalization(t *testing.T) {
	ctx := context.Background()
	localPath := writeStagedFileForTest(t, "staged-bytes")
	ff := &fakeFFmpeg{}
	policy := ffmpeg.NormalizeOptions{
		Profile: config.CanonicalVideoProfile{},
		Policy: config.VideoEncoderPolicy{
			Codec:  "h264_nvenc",
			Preset: "p1",
			CRF:    19,
		},
		Codec:  "h264_nvenc",
		Preset: "p1",
		CRF:    19,
	}
	p := NewProcessor(
		&fakeYTDLP{}, &fakeHTTPDownloader{}, ff, zap.NewNop(),
		ProcessorConfig{DataDir: t.TempDir(), TempDir: "tmp", VideoCfg: policy},
		nil, &fakePublisher{},
	)

	_, err := p.Process(ctx, &asset.ProcessInput{
		ID: "policy-preserved", Name: "policy test", LocalPath: localPath,
		OutputDir: filepath.Join(t.TempDir(), "out"),
	})
	require.NoError(t, err)
	require.True(t, ff.normalizeCalled)
	assert.Equal(t, "h264_nvenc", ff.lastNormalizeOpts.Policy.Codec)
	assert.Equal(t, "h264_nvenc", ff.lastNormalizeOpts.Codec)
	assert.Equal(t, "p1", ff.lastNormalizeOpts.Policy.Preset)
	assert.Equal(t, 19, ff.lastNormalizeOpts.Policy.CRF)
}

func TestProcessorZeroCopyOptimization(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	ff := &fakeFFmpeg{}

	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		ff,
		zap.NewNop(),
		ProcessorConfig{
			DataDir: tmp,
			TempDir: "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{
				Width:  1920,
				Height: 1080,
				FPS:    30,
			},
		},
		nil,
		&fakePublisher{},
	)

	// Case 1: StreamCopy is true but persisted clips still normalize.
	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:         "clip-1",
		Name:       "test clip",
		SourceURL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir:  filepath.Join(tmp, "out"),
		StreamCopy: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, ff.normalizeCalled)
	assert.Equal(t, "processed", result.Status)

	// Case 2: StreamCopy is true but specs don't match -> Normalize SHOULD be called.
	ff.normalizeCalled = false
	p.videoCfg.FPS = 60

	result, err = p.Process(ctx, &asset.ProcessInput{
		ID:         "clip-2",
		Name:       "test clip 2",
		SourceURL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir:  filepath.Join(tmp, "out2"),
		StreamCopy: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, ff.normalizeCalled)
}

// ── E2E audit-pin (F2.8): happy-path Publisher + 5 field propagation ──
//
// User spec: "input valido → DB ha drive_file_id, drive_link, download_link,
// md5, publish_action valorizzati". The canonical surface is ProcessResult —
// the lifecycle.Finalize + artifacts.Registry.Upsert pair persists these
// fields to media_assets (verified separately at the integration tier);
// this test pins the application-layer contract: when Process() returns
// Status="processed" with FolderID set, the 5 fields are present on
// ProcessResult AND the Publisher was invoked with the canonical
// PublishRequest shape (Destination=DestinationArtlist, LocalPath,
// Filename, Description populated).
func TestProcessorE2E_PublishesAndPopulatesDriveFieldsOnValidInput(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pub := &fakePublisher{}
	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		&fakeFFmpeg{},
		zap.NewNop(),
		ProcessorConfig{
			DataDir: tmp,
			TempDir: "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{
				Width:  1920,
				Height: 1080,
				FPS:    30,
			},
		},
		nil,
		pub,
	)

	// FolderID set = upload stage reached per processor.Process Step 4 logic.
	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-e2e",
		Name:      "e2e test clip",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Term:      "e2e",
		OutputDir: filepath.Join(tmp, "out"),
		FolderID:  "fake-folder-id-for-e2e",
	})

	require.NoError(t, err, "Process with valid input must succeed")
	require.NotNil(t, result)
	assert.Equal(t, "processed", result.Status, "Status must be 'processed' for successful upload")

	// F2.8 audit-pins: the 5 fields must be populated on ProcessResult.
	// These ARE the media_assets columns (drive_file_id / drive_link /
	// download_link / md5 / publish_action) persisted downstream by
	// lifecycle.Finalize + artifacts.Registry.Upsert.
	assert.NotEmpty(t, result.DriveFileID, "F2.8: DriveFileID (= media_assets.drive_file_id) must be populated on valid input")
	assert.NotEmpty(t, result.DriveLink, "F2.8: DriveLink (= media_assets.drive_link) must be populated on valid input")
	assert.NotEmpty(t, result.DownloadLink, "F2.8: DownloadLink (= media_assets.download_link) must be populated on valid input")
	assert.NotEmpty(t, result.MD5, "F2.8: MD5 (= media_assets.md5 / md5_checksum) must be populated on valid input")
	assert.NotEmpty(t, result.PublishAction, "F2.8: PublishAction (= media_assets.publish_action) must be populated on valid input")

	// Specific value-pinning so a future drift that flips the mapping
	// (e.g. accidentally swapping DriveLink for WebViewLink) surfaces
	// immediately in the test, not in production.
	assert.Equal(t, "fake-file-id", result.DriveFileID, "F2.8: DriveFileID maps from PublishResult.FileID")
	assert.Equal(t, "https://drive.google.com/file/d/fake-file-id/view", result.DriveLink, "F2.8: DriveLink maps from PublishResult.WebViewLink")
	assert.Equal(t, "https://drive.google.com/uc?id=fake-file-id&export=download", result.DownloadLink, "F2.8: DownloadLink maps from PublishResult.DownloadLink (canonical — not reconstructed via string interpolation)")
	assert.Equal(t, "fake-md5-checksum", result.MD5, "F2.8: MD5 maps from PublishResult.MD5Checksum")
	assert.Equal(t, "created", result.PublishAction, "F2.8: PublishAction maps from PublishResult.Action string")

	// Publisher contract surface: PublishRequest was constructed with
	// the canonical Destination + LocalPath + Filename + Description +
	// AssetID + Group + Subject + RootFolderOverride fields.
	// (ConflictPolicy is zero-value ConflictOverwrite = legacy default.)
	assert.NotNil(t, pub.lastReq, "F2.8: Publisher must have been invoked")
	assert.Equal(t, delivery.DestinationArtlist, pub.lastReq.Destination, "F2.8: PublishRequest.Destination MUST default to DestinationArtlist (Input has no Destination field today; TODO F2.9 when a non-artlist caller emerges)")
	assert.NotEmpty(t, pub.lastReq.LocalPath, "F2.8: PublishRequest.LocalPath must point to processed file")
	assert.NotEmpty(t, pub.lastReq.Filename, "F2.8: PublishRequest.Filename must be the canonical SafeName+ID form")
	assert.NotEmpty(t, pub.lastReq.Description, "F2.8: PublishRequest.Description must include input.Name + input.ID")
	assert.Equal(t, "clip-e2e", pub.lastReq.AssetID, "F2.8: PublishRequest.AssetID = input.ID")
	assert.Equal(t, "e2e", pub.lastReq.Group, "F2.8: PublishRequest.Group = input.Term (PathBuilder input)")
	// F2.8 reviewer-feedback Q1: Subject defaults to empty string (NOT
	// input.ID). Leaking media_assets.id into the PathBuilder Subject
	// slot produces opaque UUID folder metadata that humans see via
	// PathSegments. A meaningful Subject (artlist asset UUID,
	// YouTube video ID) MUST be plumbed explicitly via F2.9.
	assert.Equal(t, "", pub.lastReq.Subject, "F2.8: PublishRequest.Subject MUST default to empty string (no leaky UUID in Drive folder metadata; a meaningful Subject comes via F2.9)")
	assert.Equal(t, "fake-folder-id-for-e2e", pub.lastReq.RootFolderOverride, "F2.8: PublishRequest.RootFolderOverride = input.FolderID (explicit-folder caller inheritance)")
}

// ── E2E audit-pin (F2.8): Publish failure is best-effort + stamps Result.Error ──
//
// Per the reviewer's Q2 feedback (June 2026): on Publish failure, the
// processor: (1) warn+continue (preserves Status="processed"), (2)
// stamps Result.Error with the publisher error so callers don't have
// to grep the log to discover why Drive fields are empty. The HARD
// fail-closed (UPLOAD_FAILED) gate is owned by
// lifecycle.Service.Finalize + RequireDrive (per F2.7 closure); the
// processor stays best-effort by design (clean dependency boundary
// between layers).
func TestProcessorE2E_PublishFailureIsBestEffort(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pub := &fakePublisher{err: errors.New("Drive unreachable")}
	p := NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		&fakeFFmpeg{},
		zap.NewNop(),
		ProcessorConfig{
			DataDir: tmp,
			TempDir: "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{
				Width:  1920,
				Height: 1080,
				FPS:    30,
			},
		},
		nil,
		pub,
	)

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-e2e-publish-fail",
		Name:      "e2e publish fail clip",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir: filepath.Join(tmp, "out"),
		FolderID:  "fake-folder-id-for-e2e",
	})

	require.NoError(t, err, "F2.8: processor.Process returns nil err on Publish failure; the warn+continue path keeps Stage 1 status='processed' so the lifecycle.RequireDrive gate is the single canonical authority that flips to UPLOAD_FAILED")
	require.NotNil(t, result)
	assert.Equal(t, "processed", result.Status, "F2.8: Stage 1 status remains 'processed' on Publish failure (best-effort; lifecycle.RequireDrive owns UPLOAD_FAILED)")

	// Drive fields MUST be empty — Publisher returned an error so the
	// canonical surface reflects that. lifecycle.RequireDrive=true will
	// fail-closed at the Finalize layer.
	assert.Empty(t, result.DriveFileID, "F2.8: DriveFileID must be empty on Publish failure")
	assert.Empty(t, result.DriveLink, "F2.8: DriveLink must be empty on Publish failure")
	assert.Empty(t, result.DownloadLink, "F2.8: DownloadLink must be empty on Publish failure")
	assert.Empty(t, result.MD5, "F2.8: MD5 must be empty on Publish failure")
	assert.Empty(t, result.PublishAction, "F2.8: PublishAction must be empty on Publish failure")

	// F2.8 reviewer-feedback Q2 audit-pin: Result.Error MUST be stamped
	// with the publisher error so a downstream caller (ingest pipeline,
	// worker) can surface why the Drive side is empty without grepping
	// the log. Status stays "processed" (lifecycle owns UPLOAD_FAILED).
	assert.Contains(t, result.Error, "drive upload failed",
		"F2.8: Result.Error must be stamped with 'drive upload failed' on Publish failure so callers don't have to grep the log")
	assert.Contains(t, result.Error, "Drive unreachable",
		"F2.8: Result.Error must include the underlying publisher error verb so downstream triage is one-line")

	// LocalPath + FileHash are still populated (local save + hash succeeded).
	assert.NotEmpty(t, result.LocalPath, "F2.8: LocalPath must be populated even on Publish failure (local save succeeded)")
	assert.NotEmpty(t, result.FileHash, "F2.8: FileHash must be populated even on Publish failure")
}

// ── Step 9/12 follow-up: PR-LOCALPATH-OSREMOVE-TEST-PIN (CHANGELOG forward-pointer closure) ──
//
// Three tests pin the 3 os.Remove(actualRawPath) guards in Processor.Process:
// processor.go Step 2 cleanup (after processStep fail) + Step 3 cleanup
// (after hashStep fail) + final cleanup (after success). All three guards
// share the contract: when input.LocalPath is set, Processor.Process MUST
// NOT delete the caller-provided path even on failure. The caller
// (typically the shared assets.SourceStager port) owns the staged file's
// lifecycle.
//
// Usage note: these tests deliberately use an EMPTY ProcessorConfig.VideoCfg
// (Width=0/Height=0/FPS=0). The empty VideoCfg purposefully fails the
// zero-copy resMatch check in processStep (target.Width=0 != probed 1920),
// forcing the path through ffmpeg.Normalize instead of os.Rename on the
// LocalPath. Zero-copy's os.Rename would physically MOVE (not copy) the
// staged file to processedPath, defeating the gateway-preservation test.

// TestProcess_LocalPathPreservedOnProcessStepFailure — pin Step 2 cleanup guard.
//
// Forces ffmpeg.Normalize to fail (fakeFFmpeg.normalizeErr) and asserts
// the caller-provided LocalPath file STILL exists after Process returns.
// This pins processor.go's behavior at the `if err != nil` branch of
// processStep: `_ = os.Remove(actualRawPath)` is GUARDED by
// `if input.LocalPath == ""` — a future refactor that drops the guard
// would delete caller-owned files on transient normalize failures.
func TestProcess_LocalPathPreservedOnProcessStepFailure(t *testing.T) {
	ctx := context.Background()
	localPath := writeStagedFileForTest(t, "staged-bytes")

	ff := &fakeFFmpeg{
		normalizeErr: errors.New("forced normalize failure (PR-LOCALPATH-OSREMOVE-TEST-PIN Step 2 guard)"),
	}
	p := newProcessorForLocalPathTest(t, ff)

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-processfail",
		Name:      "test clip",
		LocalPath: localPath, // <-- gateway field set: caller-owned staged path
		OutputDir: filepath.Join(t.TempDir(), "out"),
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "process failed")
	assert.True(t, ff.normalizeCalled, "ffmpeg.Normalize MUST have been called to exercise the processStep-failure cleanup path")

	// PR-LOCALPATH-OSREMOVE-TEST-PIN Step 2 guard: gateway contract.
	_, statErr := os.Stat(localPath)
	require.NoError(t, statErr,
		"PR-LOCALPATH-OSREMOVE-TEST-PIN: Processor.Process deleted caller-provided LocalPath on processStep failure (Step 2 cleanup guard violated)")
}

// TestProcess_LocalPathPreservedOnHashStepFailure — pin Step 3 cleanup guard.
//
// Forces hashStep to fail by creating a DIRECTORY at processedPath via
// fakeFFmpeg.normalizeAsDir — hashutil.MD5File on a directory returns
// "is a directory" error. Asserts the caller-provided LocalPath file
// STILL exists after Process returns. Pins processor.go's behavior at
// the `if err != nil` branch of hashStep: `_ = os.Remove(actualRawPath)`
// is GUARDED by `if input.LocalPath == ""` — without the guard, a
// tmpdir-cleanup noise (os.ErrNotExist warning logged by caller-side
// defer Cleanup) would surface in production without a pinning test.
func TestProcess_LocalPathPreservedOnHashStepFailure(t *testing.T) {
	ctx := context.Background()
	localPath := writeStagedFileForTest(t, "staged-bytes")

	ff := &fakeFFmpeg{normalizeAsDir: true} // <-- forces hashStep MD5File failure
	p := newProcessorForLocalPathTest(t, ff)

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-hashfail",
		Name:      "test clip",
		LocalPath: localPath,
		OutputDir: filepath.Join(t.TempDir(), "out"),
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "hash failed")

	// PR-LOCALPATH-OSREMOVE-TEST-PIN Step 3 guard: gateway contract.
	_, statErr := os.Stat(localPath)
	require.NoError(t, statErr,
		"PR-LOCALPATH-OSREMOVE-TEST-PIN: Processor.Process deleted caller-provided LocalPath on hashStep failure (Step 3 cleanup guard violated)")
}

// TestProcess_LocalPathPreservedOnHappyPath — pin post-success cleanup guard.
//
// Happy-path run: Normalize succeeds, hashStep succeeds, Publisher
// succeeds. Asserts the caller-provided LocalPath file STILL exists
// after Process returns status="processed". Pins processor.go's final
// `if input.LocalPath == "" { _ = os.Remove(actualRawPath) }` cleanup
// guard. Without this pin, a refactor that drops the guard on the
// post-success path would silently delete the SourceStager's staged
// file — the very next defer Cleanup at the artlist call site would
// see os.ErrNotExist and log warn noise.
func TestProcess_LocalPathPreservedOnHappyPath(t *testing.T) {
	ctx := context.Background()
	localPath := writeStagedFileForTest(t, "staged-bytes")

	p := newProcessorForLocalPathTest(t, &fakeFFmpeg{}) // standard WriteFile path

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        "clip-happy",
		Name:      "test clip",
		LocalPath: localPath,
		OutputDir: filepath.Join(t.TempDir(), "out"),
		FolderID:  "test-folder", // triggers Publisher path
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "processed", result.Status)

	// PR-LOCALPATH-OSREMOVE-TEST-PIN post-success guard: gateway contract.
	_, statErr := os.Stat(localPath)
	require.NoError(t, statErr,
		"PR-LOCALPATH-OSREMOVE-TEST-PIN: Processor.Process deleted caller-provided LocalPath after success (post-success cleanup guard violated)")
}

// TestProcess_AtomicNormalize_ReplacesReadOnlyOutput pins the retry-safe
// normalization path: when the final output already exists and is read-only,
// Processor.Process MUST normalize to a sibling temp file and promote it
// atomically instead of writing in place.
func TestProcess_AtomicNormalize_ReplacesReadOnlyOutput(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	outputDir := filepath.Join(tmp, "out")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	name := "test clip"
	id := "clip-atomic"
	finalPath := filepath.Join(outputDir, textutil.SafeName(name)+" "+id+".mp4")
	require.NoError(t, os.WriteFile(finalPath, []byte("old-bytes"), 0o444))
	require.NoError(t, os.Chmod(finalPath, 0o444))

	localPath := writeStagedFileForTest(t, "staged-bytes")
	p := newProcessorForLocalPathTest(t, &fakeFFmpeg{})

	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:        id,
		Name:      name,
		LocalPath: localPath,
		OutputDir: outputDir,
		FolderID:  "test-folder",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "processed", result.Status)

	data, readErr := os.ReadFile(finalPath)
	require.NoError(t, readErr)
	assert.Equal(t, "processed-video", string(data))
}
