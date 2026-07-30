// Package metadata (internal/application/youtube/metadata) owns the
// YouTube metadata surface: accepts a URL, returns the canonical
// YouTubeMetadata describing the video.
//
// FORWARD-ARCHAEOLOGY: this file replaces
// internal/youtube/metadata/metadata.go, retired in P1-4 step 2
// (godlike/07 ZERO_LEGACY_POLICY). Only the canonical-owner
// filepath changed; the package-decl (`package metadata`) is
// unchanged so callers consuming this surface keep the same
// `metadata.X` and `ytmetadata.X` reference shape. Operators
// greppable by `youtube/metadata:` prefix (preserved verbatim
// from the legacy surface).
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026, phase 1): typed-narrow
// godlike/06 SSOT contract is in place. The SearchServiceAdapter
// constructor accepts the canonical application-layer
// youtubeports.SearchRunnerPort so the composition root can
// validate wiring at boot (godlike/07 fail-closed), but the
// actual field projection (incl. Duration's float64→int64
// reconciliation) is DEFERRED to phase 2. Phase 1 is the typed
// skeleton + the typed sentinel.
//
// godlike/06 SSOT: this file is a co-resident of the canonical
// metadata package (alongside service.go + builder.go +
// enrichment.go etc.); future refactors that grow the canonical
// metadata surface MUST incorporate this file's logic into
// service.go's own type set rather than introducing a sibling
// surface — the package is the SSOT owner, not the files
// within it.
package metadata

import (
	"context"
	"fmt"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// Metadata is the typed-narrow port for the YouTube metadata surface.
type Metadata interface {
	Get(ctx context.Context, url string) (*YouTubeMetadata, error)
}

// YouTubeMetadata is the canonical metadata DTO.
type YouTubeMetadata struct {
	ID           string
	Title        string
	URL          string
	Description  string
	Uploader     string
	UploadDate   string
	ViewCount    int64
	DurationSec  float64 // float64 to mirror youtubeports.DownloaderMetadata.Duration
	ThumbnailURL string
	Categories   []string
	Tags         []string
}

// ErrMetadataNotWired is the typed sentinel returned when no
// canonical runner is wired at construction time (godlike/07).
var ErrMetadataNotWired = fmt.Errorf("youtube/metadata: metadata not wired (godlike/07 fail-closed)")

// ErrMetadataNotImplemented is the phase-1 typed sentinel
// returned by Get when the canonical implementation is not
// yet promoted into this package's godlike/06 SSOT owner
// surface. godlike/07 NO-FAKE-AVAILABILITY: never a silent
// empty / placeholder.
var ErrMetadataNotImplemented = fmt.Errorf("youtube/metadata: canonical runner delegation deferred to phase 2 (godlike/07 typed sentinel; Duration field-shape projection pending)")

// SearchServiceAdapter is the canonical impl of Metadata.
type SearchServiceAdapter struct {
	runner youtubeports.SearchRunnerPort
}

// NewSearchServiceAdapter constructs the canonical Metadata.
// nil runner → ErrMetadataNotWired (godlike/07 fail-closed).
func NewSearchServiceAdapter(runner youtubeports.SearchRunnerPort) (*SearchServiceAdapter, error) {
	if runner == nil {
		return nil, ErrMetadataNotWired
	}
	return &SearchServiceAdapter{runner: runner}, nil
}

// Get returns the phase-1 typed sentinel. Phase 2 will
// delegate to runner.GetVideoInfo and project the
// application-layer DownloaderMetadata fields into the
// package-local YouTubeMetadata DTO.
//
// godlike/07 NO-FAKE-AVAILABILITY: never silent empty result —
// the typed sentinel + the deferred-phase metadata are
// observable via errors.Is + the wrapper message.
func (m *SearchServiceAdapter) Get(ctx context.Context, url string) (*YouTubeMetadata, error) {
	if m == nil {
		return nil, ErrMetadataNotWired
	}
	if url == "" {
		return nil, fmt.Errorf("youtube/metadata: url is required (godlike/07 fail-closed)")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w (url=%q)", ErrMetadataNotImplemented, url)
}

// Compile-time pinning: *SearchServiceAdapter satisfies Metadata.
var _ Metadata = (*SearchServiceAdapter)(nil)
