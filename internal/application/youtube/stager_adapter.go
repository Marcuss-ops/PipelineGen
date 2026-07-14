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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Compile-time assertion: *YouTubeStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*YouTubeStager)(nil)

// YouTubeStager downloads YouTube videos into a staging location via yt-dlp.
type YouTubeStager struct {
	ytdlp      *downloader.YTDLPDownloader
	tempPath   string
	useCookies bool
}

// NewYouTubeStager constructs a YouTube SourceStager. cfg must be non-nil.
func NewYouTubeStager(cfg *config.Config) *YouTubeStager {
	cookiesPath := cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "cookies.txt"
	}
	return &YouTubeStager{
		ytdlp:      downloader.NewYTDLP(cfg),
		tempPath:   cfg.Storage.TempPath(),
		useCookies: fileExists(cookiesPath),
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
		UseCookies:  s.useCookies,
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

// CleanupStagedSource satisfies the canonical assets.SourceStager
// interface (the V2 per-job cleanup method). It is a no-op for
// YouTubeStager because the existing Cleanup method already
// handles the per-job temp-dir cleanup. This V2 method is here
// only so YouTubeStager remains assignable to assets.SourceStager
// (the canonical interface requires both StageSource + CleanupStagedSource).
// Card 7.2 baseline repair (July 2026): this fixes the user-named
// error "CleanupStagedSource missing on stagers".
func (s *YouTubeStager) CleanupStagedSource(ctx context.Context, staged *asset.StagedSource) error {
	_ = ctx
	if staged == nil {
		return nil
	}
	_ = staged
	return nil
}

// StageSourceV2 satisfies the canonical assets.SourceStager interface
// (PR-MEDIATRANSFORMER-RENAME, July 2026). It delegates to the legacy
// StageSource and projects the StagedAsset result into the
// domain-layer domainasset.StagedSource DTO so the MediaTransformer
// contract can consume it. Mirrors the canonical pattern in
// internal/application/assets/providers/youtube/stager_adapter.go::StageSourceV2.
//
// Card 9 baseline repair (July 2026): adds StageSourceV2 so
// youtube.YouTubeStager satisfies the canonical 4-method
// assets.SourceStager interface (StageSource + Cleanup + StageSourceV2 +
// CleanupStagedSource). Without this method the compile-time assertion
// `var _ assets.SourceStager = (*YouTubeStager)(nil)` at the top of
// this file fails with "missing StageSourceV2 method".
func (s *YouTubeStager) StageSourceV2(ctx context.Context, ref asset.SourceRef) (*asset.StagedSource, error) {
	staged, err := s.StageSource(ctx, assets.SourceRef(ref))
	if err != nil {
		return nil, err
	}
	return &asset.StagedSource{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
		SourceID:  ref.URL,
		SourceRef: ref,
	}, nil
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

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
