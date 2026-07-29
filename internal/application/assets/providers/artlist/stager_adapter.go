// Package artlist — stager_adapter.go (Step 9/12, July 2026).
//
// ArtlistStager implements assets.SourceStager by wrapping the Artlist
// Downloader port. It translates SourceRef to DownloadRequest and
// delegates to the concrete downloader.
package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Compile-time assertion: *ArtlistStager satisfies assets.SourceStager.
var _ assets.SourceStager = (*ArtlistStager)(nil)

// ArtlistStager adapts the Artlist Downloader port to the shared
// assets.SourceStager interface. The downloader handles HLS (m3u8)
// via yt-dlp and progressive MP4 via HTTP.
type ArtlistStager struct {
	downloader Downloader
}

// NewArtlistStager wraps an Artlist Downloader as an assets.SourceStager.
// downloader must be non-nil.
func NewArtlistStager(downloader Downloader) *ArtlistStager {
	return &ArtlistStager{downloader: downloader}
}

// StageSource downloads the Artlist asset identified by ref.URL. The
// Downloader handles the transport (HLS/yt-dlp or HTTP). The staged
// file lands in the system temp directory.
func (s *ArtlistStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.downloader == nil {
		return nil, fmt.Errorf("artlist stagervc: downloader not wired")
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("artlist stagervc: empty URL")
	}

	// Derive a safe filename from the URL path.
	filename := filepath.Base(ref.URL)
	if filename == "" || filename == "." {
		filename = "artlist_asset"
	}

	req := DownloadRequest{
		SourceRef:     ref.URL,
		DestinationID: os.TempDir(),
		Filename:      filename,
	}

	result, err := s.downloader.Download(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("artlist stagervc: download %q: %w", ref.URL, err)
	}
	if result == nil || result.LocalPath == "" {
		return nil, fmt.Errorf("artlist stagervc: no staged file for %q", ref.URL)
	}

	return &assets.StagedAsset{
		LocalPath: result.LocalPath,
		Bytes:     result.Bytes,
	}, nil
}

// Cleanup removes the staged file's parent temp directory.
func (s *ArtlistStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	dir := filepath.Dir(staged.LocalPath)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.RemoveAll(dir)
}

func (s *ArtlistStager) StageSourceV2(ctx context.Context, ref asset.SourceRef) (*asset.StagedSource, error) {
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

func (s *ArtlistStager) CleanupStagedSource(ctx context.Context, staged *asset.StagedSource) error {
	if staged == nil {
		return nil
	}
	staged.CleanedUp = true
	return s.Cleanup(ctx, &assets.StagedAsset{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
	})
}
