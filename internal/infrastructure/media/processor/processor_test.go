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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// ── Remote stubs (fakeYTDLP / fakeHTTPDownloader / fakeFFmpeg) ──

type fakeYTDLP struct {
	err error
}

func (f *fakeYTDLP) Download(ctx context.Context, req *downloader.DownloadRequest) error {
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(req.OutputPath, []byte("fake-video"), 0o644)
}

type fakeHTTPDownloader struct{}

func (f *fakeHTTPDownloader) Download(ctx context.Context, req *downloader.HTTPDownloadRequest) error {
	return os.WriteFile(req.OutputPath, []byte("fake-http-video"), 0o644)
}

type fakeFFmpeg struct {
	normalizeErr    error
	normalizeCalled bool
}

func (f *fakeFFmpeg) Normalize(ctx context.Context, inputPath, outputPath string, opts ffmpeg.NormalizeOptions) error {
	f.normalizeCalled = true
	if f.normalizeErr != nil {
		return f.normalizeErr
	}
	return os.WriteFile(outputPath, []byte("processed-video"), 0o644)
}

func (f *fakeFFmpeg) RemuxHLS(ctx context.Context, sourceURL, outputPath string) error {
	return os.WriteFile(outputPath, []byte("hls-video"), 0o644)
}

func (f *fakeFFmpeg) Probe(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	return &ffmpeg.MediaInfo{
		Width:      1920,
		Height:     1080,
		FPS:        30,
		VideoCodec: "h264",
	}, nil
}

func (f *fakeFFmpeg) ExtractFrame(ctx context.Context, input, output string, timestamp float64) error {
	return os.WriteFile(output, []byte("fake-frame"), 0o644)
}

// ── fakePublisher (F2.8 stub for delivery.Publisher) ──
//
// Returns a PublishResult populating all 5 fields the assertion
// targets (FileID/WebViewLink/DownloadLink/MD5Checksum/Action). lastReq
// captures the most recent PublishRequest so E2E tests can assert the
// canonical request surface (Destination, LocalPath, Filename, etc.).
type fakePublisher struct {
	err     error
	lastReq delivery.PublishRequest
}

func (f *fakePublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &delivery.PublishResult{
		FileID:       "fake-file-id",
		WebViewLink:  "https://drive.google.com/file/d/fake-file-id/view",
		DownloadLink: "https://drive.google.com/uc?id=fake-file-id&export=download",
		MD5Checksum:  "fake-md5-checksum",
		Action:       delivery.PublishActionCreated,
	}, nil
}

func (f *fakePublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	return "fake-folder-id", nil
}

// Compile-time assertion: if a future method is added to
// delivery.Publisher, fakePublisher must satisfy it (AGENTS.md
// Pattern 0 convention).
var _ delivery.Publisher = (*fakePublisher)(nil)

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

	// Case 1: StreamCopy is true and specs match -> Normalize should NOT be called.
	result, err := p.Process(ctx, &asset.ProcessInput{
		ID:         "clip-1",
		Name:       "test clip",
		SourceURL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputDir:  filepath.Join(tmp, "out"),
		StreamCopy: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, ff.normalizeCalled)
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
