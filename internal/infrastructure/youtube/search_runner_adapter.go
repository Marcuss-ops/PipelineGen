// Package youtube — application-layer SearchRunnerPort adapter.
//
// Per PR2 (June 2026, YouTube fail-closed):
//
//	searcher.go + searcher_metadata.go call ytservice.ServiceDeps.SearchRunner
//	(a `youtube.SearchRunnerPort` whose methods return application-layer DTOs:
//	`[]youtubeports.SearchLiveResult` and `*youtubeports.DownloaderMetadata`).
//
//	The existing `YTDLPAdapter` (also in this package) wraps os/exec yt-dlp
//	calls but returns **`infra`-layer types** (`[]LiveSearchResult`,
//	`VideoInfo`). It therefore satisfies a DIFFERENT interface — the
//	infra-only `SearchRunner` in `internal/infrastructure/youtube/ports.go`,
//	which is reserved for callers that live below the application seam
//	(e.g. service-lifecycle tooling, ECS pre-build hooks). The
//	application-layer `SearchRunnerPort` in
//	`internal/capabilities/youtube/ports/ports.go` is what
//	`composition.go::BuildDomainBundle` wires into `youtube.ServiceDeps.SearchRunner`.
//
//	This file adds the missing adapter: `SearchRunnerAdapter` wraps
//	`*YTDLPAdapter` and converts the infra types to the canonical app-layer
//	DTOs (`SearchLiveResult`, `DownloaderMetadata`). Fail-closed contract:
//
//	  - nil config           → SearchRunnerAdapter returns nil; composition
//	                           root must treat this as a fatal misconfig.
//	  - ctx already Done     → return the unwrapped ctx.Err() (no double-wrap).
//	  - subprocess impossible → wrap the underlying error with
//	                             `ports.ErrSearchRunnerUnavailable` (search)
//	                             or `ports.ErrSearchRunnerVideoInfoUnavailable`
//	                             (info) via `errors.Is` so callers can branch.
//
//	The wrap ensures callers cannot accidentally treat "search errored out"
//	as "search returned no results" — the previous `searchRunnerStub` did
//	exactly that, returning `[]SearchLiveResult{}` with a nil error.
package youtube

import (
	"context"
	"errors"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// SearchRunnerAdapter is the application-layer bridge that satisfies
// `youtube.SearchRunnerPort`. It wraps a `*YTDLPAdapter` (which performs the
// real yt-dlp subprocess invocation) and converts each infra result into
// the canonical app-layer DTO before returning.
//
// Compilation guard: catch drift if the app-layer port signature changes.
var _ youtubeports.SearchRunnerPort = (*SearchRunnerAdapter)(nil)

// SearchRunnerAdapter implements the application-layer SearchRunnerPort.
// Internal: wraps a *YTDLPAdapter that owns the os/exec plumbing.
type SearchRunnerAdapter struct {
	inner *YTDLPAdapter
	log   *zap.Logger
}

// NewSearchRunnerAdapter wires the bridge. A nil cfg returns nil so the
// composition root can short-circuit before any service tries to use it
// (fail-closed at composition time). A nil inner is filled in with a fresh
// YTDLPAdapter for unit-test convenience.
//
// Blocco 5 (July 2026): the adapter now constructs the shared
// ytdlp.CommandBuilder and ProcessRunnerAdapter once and passes them to
// NewYTDLPAdapter — the YTDLPAdapter no longer owns its own os/exec plumbing.
func NewSearchRunnerAdapter(cfg *ytcfg.Config, log *zap.Logger) *SearchRunnerAdapter {
	if cfg == nil || log == nil {
		return nil
	}
	return &SearchRunnerAdapter{
		inner: NewYTDLPAdapter(cfg, log, NewProcessRunnerAdapter(), ytdlp.NewCommandBuilder(cfg)),
		log:   log,
	}
}

// SearchLive runs yt-dlp and converts the infra-layer []LiveSearchResult set
// into the application-layer []youtubeports.SearchLiveResult. Fail-closed
// contract:
//
//   - ctx already Done → return unwrapped ctx.Err() (no double-wrap).
//   - subprocess / parse failure → return ports.ErrSearchRunnerUnavailable
//     (wrapped with the underlying error
//     via %w so callers can debug with
//     errors.Unwrap).
//   - subprocess returned 0 hits → return (empty slice, nil) (success).
func (a *SearchRunnerAdapter) SearchLive(ctx context.Context, query string, limit int, sort string) ([]youtubeports.SearchLiveResult, error) {
	if a == nil || a.inner == nil {
		return nil, youtubeports.ErrSearchRunnerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := a.inner.SearchLive(ctx, query, limit, sort)
	if err != nil {
		// Fail-closed: surface unavailability explicitly, never swallow into
		// an empty result set.
		a.log.Warn("SearchRunner.SearchLive: subprocess failed",
			zap.Error(err),
			zap.String("query", query))
		return nil, errors.Join(youtubeports.ErrSearchRunnerUnavailable, err)
	}
	out := make([]youtubeports.SearchLiveResult, 0, len(raw))
	for _, r := range raw {
		out = append(out, youtubeports.SearchLiveResult{
			ID:        r.ID,
			Title:     r.Title,
			URL:       r.URL,
			Uploader:  r.Uploader,
			Duration:  r.Duration,
			Thumbnail: r.Thumbnail,
		})
	}
	return out, nil
}

// GetVideoInfo runs yt-dlp --dump-json on a single URL and converts the
// infra-layer VideoInfo struct into the canonical app-layer
// *youtubeports.DownloaderMetadata DTO (incl. Thumbnails + Chapters).
//
// Fail-closed contract mirrors SearchLive: ctx.Err() and subprocess errors
// are surfaced, never coerced to an empty DTO. The empty DTO returned by
// the previous searchRunnerStub is the exact failure mode that PR2 fixes.
func (a *SearchRunnerAdapter) GetVideoInfo(ctx context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	if a == nil || a.inner == nil {
		return nil, youtubeports.ErrSearchRunnerVideoInfoUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := a.inner.GetVideoInfo(ctx, videoURL)
	if err != nil {
		a.log.Warn("SearchRunner.GetVideoInfo: subprocess failed",
			zap.Error(err),
			zap.String("url", videoURL))
		return nil, errors.Join(youtubeports.ErrSearchRunnerVideoInfoUnavailable, err)
	}
	dto := &youtubeports.DownloaderMetadata{
		ID:           raw.ID,
		Title:        raw.Title,
		URL:          raw.URL,
		Description:  raw.Description,
		Uploader:     raw.Uploader,
		UploadDate:   raw.UploadDate,
		ViewCount:    raw.ViewCount,
		Duration:     raw.Duration,
		ThumbnailURL: raw.ThumbnailURL,
		Categories:   append([]string(nil), raw.Categories...),
		Tags:         append([]string(nil), raw.Tags...),
	}
	// Translate thumbnails — fixes CPR-LR-1 (the previous infra→app
	// conversion path dropped raw.Thumbnails array on the floor).
	for _, t := range raw.Thumbnails {
		dto.Thumbnails = append(dto.Thumbnails, youtubeports.VideoThumbnail{
			URL:    t.URL,
			Width:  t.Width,
			Height: t.Height,
		})
	}
	for _, ch := range raw.Chapters {
		dto.Chapters = append(dto.Chapters, youtubeports.VideoChapter{
			Title:     ch.Title,
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		})
	}
	return dto, nil
}
