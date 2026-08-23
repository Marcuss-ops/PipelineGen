package processor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// Remote stubs used by processor tests.
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
	normalizeErr      error
	proxyErr          error
	normalizeCalled   bool
	normalizeAsDir    bool
	extractFrameCalls int
	lastNormalizeOpts mediaexec.NormalizeOptions
}

func (f *fakeFFmpeg) Normalize(ctx context.Context, inputPath, outputPath string, opts mediaexec.NormalizeOptions) error {
	f.normalizeCalled = true
	f.lastNormalizeOpts = opts
	if f.normalizeErr != nil {
		return f.normalizeErr
	}
	if f.normalizeAsDir {
		return os.MkdirAll(outputPath, 0o755)
	}
	return os.WriteFile(outputPath, []byte("processed-video"), 0o644)
}

func (f *fakeFFmpeg) RemuxHLS(ctx context.Context, sourceURL, outputPath string) error {
	return os.WriteFile(outputPath, []byte("hls-video"), 0o644)
}

func (f *fakeFFmpeg) Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error) {
	return &mediaexec.MediaInfo{Width: 1920, Height: 1080, FPS: 30, VideoCodec: "h264"}, nil
}

func (f *fakeFFmpeg) ExtractFrame(ctx context.Context, input, output string, timestamp float64) error {
	f.extractFrameCalls++
	return os.WriteFile(output, []byte("fake-frame"), 0o644)
}

func (f *fakeFFmpeg) GenerateProxy(ctx context.Context, input, output string) error {
	if f.proxyErr != nil {
		return f.proxyErr
	}
	return os.WriteFile(output, []byte("fake-proxy"), 0o644)
}

func (f *fakeFFmpeg) GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error {
	return os.WriteFile(output, []byte("fake-storyboard"), 0o644)
}

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

var _ delivery.Publisher = (*fakePublisher)(nil)

func writeStagedFileForTest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeStagedFileForTest: %v", err)
	}
	return path
}

func newProcessorForLocalPathTest(t *testing.T, ff *fakeFFmpeg) *Processor {
	t.Helper()
	tmp := t.TempDir()
	return NewProcessor(
		&fakeYTDLP{},
		&fakeHTTPDownloader{},
		ff,
		zap.NewNop(),
		ProcessorConfig{
			DataDir:  tmp,
			TempDir:  "tmp",
			VideoCfg: mediaexec.NormalizeOptions{},
		},
		nil,
		&fakePublisher{},
	)
}
