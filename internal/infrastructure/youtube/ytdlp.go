package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	cfg        *ytcfg.Config
	log        *zap.Logger
	runner     ProcessRunnerPort
	cmdBuilder *ytdlp.CommandBuilder
}

// NewYTDLPAdapter constructs the adapter. cfg.External.ResolvedYtdlpPath()
// must be set (composition root guarantees it). The caller must supply
// a ProcessRunnerPort (production: ProcessRunnerAdapter) and a
// *ytdlp.CommandBuilder — both are created by NewSearchRunnerAdapter.
func NewYTDLPAdapter(cfg *ytcfg.Config, log *zap.Logger, runner ProcessRunnerPort, cmdBuilder *ytdlp.CommandBuilder) *YTDLPAdapter {
	return &YTDLPAdapter{cfg: cfg, log: log, runner: runner, cmdBuilder: cmdBuilder}
}

// SearchLive implements SearchRunner.SearchLive. ytsearchN:query for
// relevance-sorted and /results?search_query=...&sp=CAM%253D for views-
// sorted. Cookies-as-flag pattern: attach only when the common resolver
// returns a configured path
// (Android client cannot be combined with cookies).
func (a *YTDLPAdapter) SearchLive(ctx context.Context, query string, limit int, sort string) ([]LiveSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	var args []string
	if sort == "views" {
		u := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&sp=CAM%%253D", url.QueryEscape(query))
		args = append(args, u, "--dump-json", "--flat-playlist", "--playlist-end", strconv.Itoa(limit))
		// Blocco 5 (July 2026): for views-sorted search, pass the
		// constructed YouTube URL to BaseArgs so cookies/JS runtime/
		// extractor-args are applied (old code did this via os.Stat).
		args = append(a.cmdBuilder.BaseArgs(u, a.cmdBuilder.YouTubeCookiesConfigured()), args...)
	} else {
		args = append(args, fmt.Sprintf("ytsearch%d:%s", limit, query), "--dump-json", "--flat-playlist")
		// ytsearchN queries are handled internally by yt-dlp, so provide a
		// canonical YouTube URL to BaseArgs for the shared cookie/client
		// policy while keeping the query as the positional input.
		args = append(a.cmdBuilder.BaseArgs("https://www.youtube.com/", a.cmdBuilder.YouTubeCookiesConfigured()), args...)
	}

	stdout, stderr, err := a.runner.Run(ctx, a.cmdBuilder.Path, args)
	if err != nil {
		a.log.Error("yt-dlp search failed", zap.Error(err), zap.String("stderr", stderr))
		return nil, fmt.Errorf("search failed: %w", err)
	}
	if strings.TrimSpace(stdout) == "" {
		a.log.Warn("yt-dlp search returned empty stdout")
		return nil, fmt.Errorf("search returned no output")
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

	// Blocco 5 (July 2026): BaseArgs centralizes cookies, JS runtime,
	// --no-warnings, and --extractor-args. Cookies are intentionally OFF
	// for info (disables the Android client and breaks n-challenge solving).
	args := append(a.cmdBuilder.BaseArgs(videoURL, a.cmdBuilder.YouTubeCookiesConfigured()),
		videoURL,
		"--dump-json",
		"--no-playlist",
	)

	stdout, stderr, err := a.runner.Run(ctx, a.cmdBuilder.Path, args)
	if err != nil {
		a.log.Error("yt-dlp info failed", zap.Error(err), zap.String("stderr", stderr))
		return VideoInfo{}, fmt.Errorf("failed to get video info: %w", err)
	}
	if strings.TrimSpace(stdout) == "" {
		return VideoInfo{}, fmt.Errorf("video info returned no output")
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
