package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"go.uber.org/zap"

	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
)

// VideoInfo is the result of GetVideoInfo. Mirrors the existing
// downloader.YouTubeMetadata but stripped to the fields the application
// layer actually reads.
type VideoInfo struct {
	ID           string
	URL          string
	Title        string
	Description  string
	Uploader     string
	UploadDate   string
	ViewCount    int64
	Duration     float64
	ThumbnailURL string
	Thumbnails   []VideoThumbnail
	Chapters     []VideoChapter
	Categories   []string
	Tags         []string
}

type VideoThumbnail struct {
	URL    string
	Width  int
	Height int
}

type VideoChapter struct {
	Title     string
	StartTime float64
	EndTime   float64
}

// YTDLPAdapter is the concrete implementation of SearchRunner. It is
// wired in internal/app/dependencies.go and injected into the
// application layer via SetSearchRunner.
type YTDLPAdapter struct {
	cfg *ytcfg.Config
	log *zap.Logger
}

// NewYTDLPAdapter constructs the adapter. cfg.External.ResolvedYtdlpPath()
// must be set (composition root guarantees it).
func NewYTDLPAdapter(cfg *ytcfg.Config, log *zap.Logger) *YTDLPAdapter {
	return &YTDLPAdapter{cfg: cfg, log: log}
}

// SearchLive implements SearchRunner.SearchLive. ytsearchN:query for
// relevance-sorted and /results?search_query=...&sp=CAM%253D for views-
// sorted. Cookies-as-flag pattern: only attach when cookies.txt exists
// (Android client cannot be combined with cookies).
func (a *YTDLPAdapter) SearchLive(ctx context.Context, query string, limit int, sort string) ([]LiveSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	path := a.cfg.External.ResolvedYtdlpPath()
	var args []string
	if sort == "views" {
		u := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&sp=CAM%%253D", url.QueryEscape(query))
		args = []string{u, "--dump-json", "--flat-playlist", "--no-warnings", "--playlist-end", strconv.Itoa(limit)}
	} else {
		args = []string{
			fmt.Sprintf("ytsearch%d:%s", limit, query),
			"--dump-json",
			"--flat-playlist",
			"--no-warnings",
		}
	}
	cookiesPath := a.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "cookies.txt"
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
	}

	stdout, stderr, err := a.run(ctx, path, args...)
	if err != nil {
		a.log.Error("yt-dlp search failed", zap.Error(err), zap.String("stderr", stderr))
		return nil, fmt.Errorf("search failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	results := make([]LiveSearchResult, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var item struct {
			ID         string  `json:"id"`
			URL        string  `json:"url"`
			Title      string  `json:"title"`
			Duration   float64 `json:"duration"`
			Uploader   string  `json:"uploader"`
			Thumbnails []struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			a.log.Warn("failed to unmarshal search result line", zap.Error(err))
			continue
		}
		thumb := ""
		if len(item.Thumbnails) > 0 {
			thumb = item.Thumbnails[len(item.Thumbnails)-1].URL
		}
		results = append(results, LiveSearchResult{
			ID:        item.ID,
			URL:       item.URL,
			Title:     item.Title,
			Duration:  item.Duration,
			Uploader:  item.Uploader,
			Thumbnail: thumb,
		})
	}
	return results, nil
}

// GetVideoInfo implements SearchRunner.GetVideoInfo. Mirrors the
// previous application-side implementation. Cookies intentionally OFF
// here (cookies disable the Android client and break n-challenge solving).
func (a *YTDLPAdapter) GetVideoInfo(ctx context.Context, videoURL string) (VideoInfo, error) {
	if videoURL == "" {
		return VideoInfo{}, fmt.Errorf("url is required")
	}
	if err := security.ValidateDownloadURL(videoURL); err != nil {
		return VideoInfo{}, err
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

	stdout, stderr, err := a.run(ctx, path, args...)
	if err != nil {
		a.log.Error("yt-dlp info failed", zap.Error(err), zap.String("stderr", stderr))
		return VideoInfo{}, fmt.Errorf("failed to get video info: %w", err)
	}

	var raw struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Duration    float64 `json:"duration"`
		Uploader    string  `json:"uploader"`
		UploadDate  string  `json:"upload_date"`
		ViewCount   int64   `json:"view_count"`
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
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return VideoInfo{}, fmt.Errorf("failed to parse video info: %w", err)
	}

	out := VideoInfo{
		ID:          raw.ID,
		URL:         videoURL,
		Title:       raw.Title,
		Description: raw.Description,
		Duration:    raw.Duration,
		Uploader:    raw.Uploader,
		UploadDate:  raw.UploadDate,
		ViewCount:   raw.ViewCount,
		Categories:  raw.Categories,
		Tags:        raw.Tags,
	}
	if len(raw.Thumbnails) > 0 {
		out.ThumbnailURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
		for _, t := range raw.Thumbnails {
			out.Thumbnails = append(out.Thumbnails, VideoThumbnail{URL: t.URL, Width: t.Width, Height: t.Height})
		}
	}
	for _, c := range raw.Chapters {
		out.Chapters = append(out.Chapters, VideoChapter{Title: c.Title, StartTime: c.StartTime, EndTime: c.EndTime})
	}
	return out, nil
}

// run is the private helper for invoking yt-dlp. Kept here so callers
// inside this package don't repeat the same os/exec plumbing.
func (a *YTDLPAdapter) run(ctx context.Context, path string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}
