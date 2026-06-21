package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// YouTubeMetadata is the infrastructure-layer DTO for the yt-dlp
// --dump-json output.
type YouTubeMetadata struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Duration     float64  `json:"duration"`
	Uploader     string   `json:"uploader"`
	UploadDate   string   `json:"upload_date"`
	ViewCount    int64    `json:"view_count"`
	Language     string   `json:"language"`
	ThumbnailURL string   `json:"thumbnail"`
	Thumbnails   []struct {
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

// MetadataFetcherAdapter implements MetadataFetcherPort. It shells out
// to yt-dlp --dump-json via the injected ProcessRunnerPort.
type MetadataFetcherAdapter struct {
	cfg    *ytcfg.Config
	runner ProcessRunnerPort
}

// Compile-time assertion: *MetadataFetcherAdapter satisfies the port.
var _ MetadataFetcherPort = (*MetadataFetcherAdapter)(nil)

// NewMetadataFetcherAdapter wires the adapter. cfg.External.ResolvedYtdlpPath()
// must be set (composition root guarantees it). A nil runner falls back
// to a fresh ProcessRunnerAdapter.
func NewMetadataFetcherAdapter(cfg *ytcfg.Config, runner ProcessRunnerPort) *MetadataFetcherAdapter {
	if runner == nil {
		runner = NewProcessRunnerAdapter()
	}
	return &MetadataFetcherAdapter{cfg: cfg, runner: runner}
}

// GetVideoMetadata runs yt-dlp --dump-json for videoURL and parses its
// JSON output.
func (a *MetadataFetcherAdapter) GetVideoMetadata(ctx context.Context, videoURL string) (*YouTubeMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("metadata: videoURL is required")
	}

	path := a.cfg.External.ResolvedYtdlpPath()
	args := []string{
		videoURL,
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
	}
	if a.cfg.External.YouTubeJSRuntimePath != "" {
		args = append(args, "--js-runtime", a.cfg.External.YouTubeJSRuntimePath)
	}
	args = append(args, "--extractor-args", "youtube:player_client=android,web")

	stdout, stderr, err := a.runner.Run(ctx, path, args)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp dump-json failed for %s: %w (stderr: %s)",
			sanitizedURL(videoURL), err, truncate(stderr, 512))
	}

	var raw YouTubeMetadata
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("metadata: parse yt-dlp dump-json for %s: %w", sanitizedURL(videoURL), err)
	}
	if raw.ID == "" {
		raw.ID = extractIDFromURL(videoURL)
	}
	if raw.ThumbnailURL == "" && len(raw.Thumbnails) > 0 {
		raw.ThumbnailURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
	}
	return &raw, nil
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
