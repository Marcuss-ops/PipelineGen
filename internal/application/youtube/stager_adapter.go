// Package youtube — stager_adapter.go (Step 9/12, July 2026).
//
// YouTubeStager implements assets.SourceStager for YouTube video URLs.
// It uses the shared yt-dlp downloader to stage a full video (or a
// section) to a temp directory. The caller is responsible for cleanup.
//
// NOTE: This adapter stages the FULL video (or a time section). It does
// NOT cut or transcode — that's the caller's responsibility via the
// VideoCutter / VideoPipelinePort ports. This separation lets the
// channel-monitor and ingest pipelines stage source bytes before
// deciding which segments to extract.
package youtube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Compile-time assertion: *YouTubeStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*YouTubeStager)(nil)

// YouTubeStager downloads YouTube videos into a staging location via yt-dlp.
type YouTubeStager struct {
	ytdlp    *downloader.YTDLPDownloader
	tempPath string
}

// NewYouTubeStager constructs a YouTube SourceStager. cfg must be non-nil.
func NewYouTubeStager(cfg *config.Config) *YouTubeStager {
	return &YouTubeStager{
		ytdlp:    downloader.NewYTDLP(cfg),
		tempPath: cfg.Storage.TempPath(),
	}
}

// StageSource downloads the YouTube video identified by ref.URL into a
// temp directory and returns the staged file path. The download uses
// --no-playlist and a 10-minute timeout.
func (s *YouTubeStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if ref.URL == "" {
		return nil, fmt.Errorf("youtube stagervc: empty URL")
	}

	tempDir, err := os.MkdirTemp(s.tempPath, "yt_stage_")
	if err != nil {
		return nil, fmt.Errorf("youtube stagervc: create temp dir: %w", err)
	}

	outputTemplate := filepath.Join(tempDir, "source.%(ext)s")

	dlReq := &downloader.DownloadRequest{
		URL:         ref.URL,
		OutputPath:  outputTemplate,
		NoPlaylist:  true,
		Timeout:     10 * time.Minute,
		MergeFormat: "mp4",
	}

	if err := s.ytdlp.Download(ctx, dlReq); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("youtube stagervc: download %q: %w", ref.URL, err)
	}

	localPath, err := downloader.ResolveDownloadedSegmentPath(outputTemplate)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("youtube stagervc: resolve output: %w", err)
	}

	fi, statErr := os.Stat(localPath)
	if statErr != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("youtube stagervc: stat %q: %w", localPath, statErr)
	}
	if fi.Size() == 0 {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("youtube stagervc: downloaded file is empty: %s", localPath)
	}

	return &assets.StagedAsset{
		LocalPath: localPath,
		Bytes:     fi.Size(),
	}, nil
}

// Cleanup removes the staged file's parent temp directory.
func (s *YouTubeStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	return stageCleanupDir(staged.LocalPath)
}

// stageCleanupDir removes the parent directory of localPath. Safe to
// call with empty path (no-op). Errors are logged but not returned
// — cleanup is best-effort by design.
func stageCleanupDir(localPath string) error {
	dir := filepath.Dir(localPath)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.RemoveAll(dir)
}
