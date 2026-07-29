// Package youtube — stager_adapter.go (ART-002 P4.1, July 2026).
//
// YouTubeStager implements assets.SourceStager by wrapping the YouTube
// adapter's Fetch method (which delegates to yt-dlp DownloadAndCut via
// the existing providers.FetchProvider contract). It translates
// assets.SourceRef into providers.FetchRequest and returns the staged
// file path produced by yt-dlp.
//
// godlike/06 SSOT — one canonical owner per fact:
//
//	"Given a SourceKind=SourceKindYouTube SourceRef, which bytes land on
//	 disk and at what path?" lives here. The artlist canonical pattern
//	 (ArtlistStager) is the precedent — the YouTube variant mirrors the
//	 same StageSource + Cleanup surface for cross-provider consistency
//	 in the SourceStagerRegistry lookup. Each adapter exposes a
//	 `var _ assets.SourceStager = (*YouTubeStager)(nil)` compile-time
//	 assertion pinned at the top of this file.
//
// godlike/07 typed-error contract: ErrYoutubeStagerNotWired +
// ErrYoutubeStagerEmptyURL + ErrYoutubeStagerNoStagedFile are typed
// sentinels reachable via errors.Is from any caller seam; StageSource
// returns the appropriate sentinel wrapped with the failing ref in the
// message so log-scanners can surface which ref was rejected.
package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Compile-time assertion: *YouTubeStager satisfies assets.SourceStager.
// Mirrors the canonical ArtlistStager pattern in
// internal/application/assets/providers/artlist/stager_adapter.go.
var _ assets.SourceStager = (*YouTubeStager)(nil)

// Typed-error sentinels (godlike/07). Reachable via errors.Is from any
// caller seam. Each sentinel's message carries the failing ref in the
// fmt.Errorf %w chain so log-scanners can correlate the rejection with
// the SourceRef that triggered it.
var (
	// ErrYoutubeStagerNotWired is returned when the YouTubeStager is
	// constructed with a nil adapter (composition-time fail-closed).
	ErrYoutubeStagerNotWired = errors.New("youtube stager: adapter not wired")

	// ErrYoutubeStagerEmptyURL is returned when StageSource is called
	// with a SourceRef whose URL is empty.
	ErrYoutubeStagerEmptyURL = errors.New("youtube stager: empty URL")

	// ErrYoutubeStagerNoStagedFile is returned when the underlying
	// adapter.Fetch completes without producing a LocalPath (nil
	// result or empty path).
	ErrYoutubeStagerNoStagedFile = errors.New("youtube stager: no staged file produced")
)

// YouTubeStager adapts the YouTube adapter's Fetch method to the shared
// assets.SourceStager interface. The adapter handles yt-dlp extraction
// (full video or segment cut) and the resulting LocalPath is the staged
// file the caller owns and must explicitly Cleanup after use.
//
// SourceRef.DownloadSection is honoured by the underlying adapter (the
// YouTube adapter translates SegmentStart/SegmentEnd from the canonical
// FetchRequest into yt-dlp DownloadSections). SourceRef.ForceKeyframes
// and SourceRef.MergeFormat are NOT yet honoured by the YouTube
// adapter; if a future plan forwards them, the registry lookup
// continues to work because StageSource accepts the full SourceRef.
type YouTubeStager struct {
	adapter *Adapter
}

// NewYouTubeStager wraps a YouTube *Adapter as an assets.SourceStager.
// adapter must be non-nil; the constructor does not perform a nil guard
// here so the compile-time assertion catches drift at build time, but
// StageSource performs the runtime check via checkWired (matches the
// ArtlistStager precedent).
func NewYouTubeStager(adapter *Adapter) *YouTubeStager {
	return &YouTubeStager{adapter: adapter}
}

// StageSource downloads the YouTube asset identified by ref.URL via
// yt-dlp. The staged file lands in a temp dir produced by the
// underlying adapter.
func (s *YouTubeStager) StageSource(ctx context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	if s.adapter == nil {
		return nil, fmt.Errorf("%w", ErrYoutubeStagerNotWired)
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("%w", ErrYoutubeStagerEmptyURL)
	}

	// Derive a safe AssetID for the underlying FetchRequest. We use
	// filepath.Base(ref.URL) when possible; fall back to a timestamped
	// sentinel when the URL has no basename.
	safeName := filepath.Base(ref.URL)
	if safeName == "" || safeName == "." || safeName == "/" {
		safeName = fmt.Sprintf("yt_fetch_%d", timeNowUnixNano())
	}

	fetchReq := providers.FetchRequest{
		SourceRef: ref.URL,
		AssetID:   safeName,
		// SegmentStart/SegmentEnd are derived from DownloadSection
		// when set; the underlying adapter translates these into
		// yt-dlp DownloadSections. Zero values (full video) are
		// supported by the adapter's full-video sentinel.
	}

	// Translate DownloadSection (yt-dlp "HH:MM:SS-HH:MM:SS" format)
	// into SegmentStart/SegmentEnd seconds. The YouTube adapter's
	// Fetch uses these to drive yt-dlp's segment cut.
	if ref.DownloadSection != "" {
		start, end, err := parseDownloadSection(ref.DownloadSection)
		if err != nil {
			return nil, fmt.Errorf("youtube stager: parse download section %q: %w", ref.DownloadSection, err)
		}
		fetchReq.SegmentStart = start
		fetchReq.SegmentEnd = end
	}

	fetched, err := s.adapter.Fetch(ctx, fetchReq)
	if err != nil {
		return nil, fmt.Errorf("youtube stager: fetch %q: %w", ref.URL, err)
	}
	if fetched == nil || fetched.LocalPath == "" {
		return nil, fmt.Errorf("%w: url=%q", ErrYoutubeStagerNoStagedFile, ref.URL)
	}

	return &assets.StagedAsset{
		LocalPath: fetched.LocalPath,
		Bytes:     fetched.Bytes,
		SourceID:  ref.URL,
	}, nil
}

// Cleanup removes the staged file. The YouTube adapter stages into a
// temp dir owned by the caller; we remove the file directly. The parent
// directory is left intact (the temp dir may contain other staged
// files from parallel calls).
func (s *YouTubeStager) Cleanup(ctx context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}
	if err := os.Remove(staged.LocalPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("youtube stager: cleanup %q: %w", staged.LocalPath, err)
	}
	return nil
}

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

func (s *YouTubeStager) CleanupStagedSource(ctx context.Context, staged *asset.StagedSource) error {
	if staged == nil {
		return nil
	}
	staged.CleanedUp = true
	return s.Cleanup(ctx, &assets.StagedAsset{
		LocalPath: staged.LocalPath,
		Bytes:     staged.Bytes,
	})
}
