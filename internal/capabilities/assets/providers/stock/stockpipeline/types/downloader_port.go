// Package stockpipeline — downloader_port.go (PR-REFACTOR-P0-IO-BINDER, July 2026).
//
// SourceDownloader is the Pattern 0 typed port for source video
// downloading. StockStager routes source downloads through this
// port so the application layer never imports infrastructure types
// (godlike/07). The concrete implementation lives in
// internal/platform/downloader/stock_adapter.go and is
// injected via WithDownloader at composition time.
//
// godlike/06 SSOT:
//   - This file is the SOLE owner of the SourceDownloader interface
//     and the SourceDownloadRequest / DownloadedSource DTOs.
//   - Tests inject fakes via WithDownloader(fake).
//
// godlike/07 fail-closed:
//   - When StockStager.s.downloader is nil and a download path is
//     required, StageSource surfaces a typed error.
package assets

import "context"

// SourceDownloadRequest is the application-layer DTO for configuring
// a source video download. It mirrors the fields StockStager needs
// without importing infrastructure types.
type SourceDownloadRequest struct {
	URL              string
	OutputPath       string
	DownloadSections []string
	ForceKeyframes   bool
	MergeFormat      string
	NoPlaylist       bool
	UseCookies       bool
}

// DownloadedSource is the application-layer DTO returned by a
// successful download. The ResolvedPath is the actual file path
// after the infrastructure adapter resolves yt-dlp's %(ext)s
// template internally.
type DownloadedSource struct {
	ResolvedPath string
	SizeBytes    int64
}

// SourceDownloader is the Pattern 0 typed port for source video
// downloading. The single method mirrors the canonical download
// contract without exposing infrastructure types.
//
// The infrastructure adapter (stock_adapter.go) translates
// SourceDownloadRequest → downloader.DownloadRequest, calls
// yt-dlp, resolves the output path, and returns DownloadedSource.
type SourceDownloader interface {
	Download(ctx context.Context, req *SourceDownloadRequest) (*DownloadedSource, error)
}
