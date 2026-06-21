package youtube

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// MetadataFetcher fetches YouTube video metadata via yt-dlp --dump-json.
// Implements youtube.VideoMetadataFetcherPort from the application layer.
type MetadataFetcher struct {
	cfg *ytcfg.Config
	log *zap.Logger
}

// NewMetadataFetcher constructs the adapter.
func NewMetadataFetcher(cfg *ytcfg.Config, log *zap.Logger) *MetadataFetcher {
	return &MetadataFetcher{cfg: cfg, log: log}
}

// ytDlpMeta is the raw JSON shape returned by yt-dlp --dump-json.
type ytDlpMeta struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
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

// GetVideoMetadata fetches full metadata for a YouTube video without downloading it.
// Returns *YouTubeMetadataPort (the application-layer DTO defined in ports.go).
func (f *MetadataFetcher) GetVideoMetadata(ctx context.Context, videoURL string) (*youtubeapp.YouTubeMetadataPort, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if err := security.ValidateDownloadURL(videoURL); err != nil {
		return nil, err
	}

	videoID, _ := urlutil.ExtractVideoID(videoURL)
	ytdlpPath := f.cfg.External.ResolvedYtdlpPath()

	args := []string{
		videoURL,
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
	}
	if f.cfg.External.YouTubeJSRuntimePath != "" {
		args = append(args, "--js-runtime", f.cfg.External.YouTubeJSRuntimePath)
	}
	args = append(args, "--extractor-args", "youtube:player_client=android,web")

	runner := NewProcessRunner(f.log)
	stdout, stderr, err := runner.Run(ctx, ytdlpPath, args)
	if err != nil {
		f.log.Error("yt-dlp info failed", zap.Error(err), zap.String("stderr", stderr))
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	var raw ytDlpMeta
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	meta := &youtubeapp.YouTubeMetadataPort{
		ID:          raw.ID,
		Title:       raw.Title,
		Description: raw.Description,
		Duration:    raw.Duration,
		Uploader:    raw.Uploader,
		UploadDate:  raw.UploadDate,
		ViewCount:   raw.ViewCount,
		Language:    raw.Language,
		Categories:  raw.Categories,
		Tags:        raw.Tags,
	}
	if videoID != "" {
		meta.ID = videoID
	}
	if raw.Thumbnail != "" {
		meta.ThumbnailURL = raw.Thumbnail
	} else if len(raw.Thumbnails) > 0 {
		meta.ThumbnailURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
	}
	for _, c := range raw.Chapters {
		meta.Chapters = append(meta.Chapters, youtubeapp.VideoChapterPort{
			Title: c.Title, StartTime: c.StartTime, EndTime: c.EndTime,
		})
	}
	return meta, nil
}
