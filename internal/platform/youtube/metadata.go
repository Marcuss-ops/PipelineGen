// Package youtube — infrastructure-layer yt-dlp metadata adapter.
//
// Per PR2 followup (June 2026): the application-layer port
// `VideoMetadataFetcherPort.GetVideoMetadata` returns the canonical
// `*DownloaderMetadata` DTO (defined in `internal/capabilities/youtube/ports.go`)
// instead of an infrastructure-private struct. This adapter:
//
//  1. Parses raw yt-dlp dump-json into a private `ytDLPJSON` struct that
//     mirrors the JSON wire shape.
//  2. Converts the parsed value into the canonical `*youtubedto.DownloaderMetadata`
//     DTO so callers can read concrete fields (`Description`, `Tags`, etc.)
//     without depending on an empty-marker interface.
//
// The infrastructure layer never lets a raw yt-dlp JSON value escape;
// callers always receive the canonical DTO shape.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// ytDLPJSON is the shape of a single yt-dlp --dump-json payload. Private to
// the infrastructure adapter; the conversion to the canonical
// `*youtubedto.DownloaderMetadata` happens at the bottom of GetVideoMetadata.
type ytDLPJSON struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	LiveStatus  string  `json:"live_status"`
	Description string  `json:"description"`
	Duration    float64 `json:"duration"`
	Uploader    string  `json:"uploader"`
	UploadDate  string  `json:"upload_date"`
	ViewCount   int64   `json:"view_count"`
	Language    string  `json:"language"`
	Thumbnail   string  `json:"thumbnail"`
	Thumbnails  []struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"thumbnails"`
	Chapters []struct {
		Title     string  `json:"title"`
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	} `json:"chapters"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
}

// MetadataFetcherAdapter implements youtube.VideoMetadataFetcherPort. It shells out
// to yt-dlp --dump-json via the injected ProcessRunnerPort and returns the
// canonical app-layer DTO.
type MetadataFetcherAdapter struct {
	cfg    *ytcfg.Config
	runner ProcessRunnerPort
}

// Compile-time assertion: *MetadataFetcherAdapter satisfies the port.
var _ youtubedto.VideoMetadataFetcherPort = (*MetadataFetcherAdapter)(nil)

// NewMetadataFetcherAdapter wires the adapter. cfg.External.ResolvedYtdlpPath()
// must be set (composition root guarantees it). A nil runner falls back
// to a fresh ProcessRunnerAdapter.
func NewMetadataFetcherAdapter(cfg *ytcfg.Config, runner ProcessRunnerPort) *MetadataFetcherAdapter {
	if runner == nil {
		runner = NewProcessRunnerAdapter()
	}
	return &MetadataFetcherAdapter{cfg: cfg, runner: runner}
}

// GetVideoMetadata runs yt-dlp --dump-json for videoURL, parses the raw JSON,
// and converts the parsed value into the canonical
// `*youtubedto.DownloaderMetadata` DTO.
func (a *MetadataFetcherAdapter) GetVideoMetadata(ctx context.Context, videoURL string) (*youtubedto.DownloaderMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("metadata: videoURL is required")
	}

	path := a.cfg.External.ResolvedYtdlpPath()
	// Delegate to ytdlp.BaseArgs — the canonical SOLE owner of the
	// yt-dlp argv prefix (godlike/06 SSOT). PR-PLAYER-CLIENT-DRIFT-FIX
	// (2026-07-06): the previous inline `youtube:player_client=android,web`
	// literal was the REVERSED order vs the canonical web,android
	// centralized in cmd_builder.go by f3f1ee90 (android-first returns
	// wrong durations + missing formats for some videos). Delegating
	// also fixes a latent bug where --js-runtime was injected without
	// --remote-components ejs:github (BaseArgs always injects both).
	b := ytdlp.NewCommandBuilder(a.cfg)
	args := b.BaseArgs(videoURL, b.YouTubeCookiesConfigured())
	args = append(args, "--dump-json", "--no-playlist", videoURL)

	stdout, stderr, err := a.runner.Run(ctx, path, args)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp dump-json failed for %s: %w (stderr: %s)",
			sanitizedURL(videoURL), err, truncate(stderr, 512))
	}

	var raw ytDLPJSON
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("metadata: parse yt-dlp dump-json for %s: %w", sanitizedURL(videoURL), err)
	}
	if raw.ID == "" {
		raw.ID = extractIDFromURL(videoURL)
	}
	thumbURL := raw.Thumbnail
	if thumbURL == "" && len(raw.Thumbnails) > 0 {
		thumbURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
	}

	dto := &youtubedto.DownloaderMetadata{
		ID:           raw.ID,
		Title:        raw.Title,
		LiveStatus:   raw.LiveStatus,
		URL:          videoURL,
		Description:  raw.Description,
		Duration:     raw.Duration,
		Uploader:     raw.Uploader,
		UploadDate:   raw.UploadDate,
		ViewCount:    raw.ViewCount,
		Language:     raw.Language,
		ThumbnailURL: thumbURL,
		Categories:   raw.Categories,
		Tags:         raw.Tags,
	}
	for _, t := range raw.Thumbnails {
		dto.Thumbnails = append(dto.Thumbnails, youtubedto.VideoThumbnail{
			URL:    t.URL,
			Width:  t.Width,
			Height: t.Height,
		})
	}
	for _, ch := range raw.Chapters {
		dto.Chapters = append(dto.Chapters, youtubedto.VideoChapter{
			Title:     ch.Title,
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		})
	}
	return dto, nil
}

// sanitizedURL strips the query string so errors don't echo signed
// cookies / oauth tokens.
func sanitizedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	return u.String()
}

// extractIDFromURL pulls the YouTube video ID from common URL shapes.
func extractIDFromURL(raw string) string {
	markers := []string{"v=", "/shorts/", "/embed/", "/live/", "youtu.be/"}
	for _, m := range markers {
		if idx := strings.Index(raw, m); idx >= 0 {
			rest := raw[idx+len(m):]
			if stop := strings.IndexAny(rest, "?&/ "); stop >= 0 {
				return rest[:stop]
			}
			return rest
		}
	}
	return ""
}

// truncate caps s at max bytes, suffixing with "…" when shortened.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
