package mediaasset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

type fakeYTDLP struct {
	err error
}

func (f *fakeYTDLP) Download(ctx context.Context, req *downloader.DownloadRequest) error {
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(req.OutputPath, []byte("fake-video"), 0644)
}

type fakeHTTPDownloader struct{}

func (f *fakeHTTPDownloader) Download(ctx context.Context, req *downloader.HTTPDownloadRequest) error {
	return os.WriteFile(req.OutputPath, []byte("fake-http-video"), 0644)
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
	return os.WriteFile(outputPath, []byte("processed-video"), 0644)
}

func (f *fakeFFmpeg) RemuxHLS(ctx context.Context, sourceURL, outputPath string) error {
	return os.WriteFile(outputPath, []byte("hls-video"), 0644)
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
			DataDir: tmp,
			TempDir: "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{},
		},
		nil,
	)

	result, err := p.DownloadProcessUpload(ctx, AssetInput{
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
			DataDir: tmp,
			TempDir: "tmp",
			VideoCfg: ffmpeg.NormalizeOptions{},
		},
		nil,
	)

	result, err := p.DownloadProcessUpload(ctx, AssetInput{
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
