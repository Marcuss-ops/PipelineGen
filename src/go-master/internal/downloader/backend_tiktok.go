// Package downloader fornisce backend TikTok
package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// TikTokBackend implementa Downloader per TikTok
type TikTokBackend struct {
	ytdlpPath string
	userAgent string
	proxy     string
}

// NewTikTokBackend crea un nuovo backend TikTok
func NewTikTokBackend(ytdlpPath, userAgent, proxy string) *TikTokBackend {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	
	// User-Agent realistico per TikTok
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	
	return &TikTokBackend{
		ytdlpPath: ytdlpPath,
		userAgent: userAgent,
		proxy:     proxy,
	}
}

// GetInfo ottiene informazioni su un video TikTok
func (b *TikTokBackend) GetInfo(ctx context.Context, url string) (*VideoInfo, error) {
	logger.Info("Getting TikTok video info",
		zap.String("url", url),
	)

	args := []string{
		"--dump-json",
		"--no-download",
		"--no-warnings",
		"--user-agent", b.userAgent,
		url,
	}

	if b.proxy != "" {
		args = append(args, "--proxy", b.proxy)
	}

	cmd := exec.CommandContext(ctx, b.ytdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get TikTok video info: %w", err)
	}

	var info struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Duration    float64 `json:"duration"`
		Thumbnail   string  `json:"thumbnail"`
		Uploader    string  `json:"uploader"`
		ViewCount   int64   `json:"view_count"`
		UploadDate  string  `json:"upload_date"`
		Tags        []string `json:"tags"`
	}

	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse TikTok video info: %w", err)
	}

	videoInfo := &VideoInfo{
		ID:          info.ID,
		Platform:    PlatformTikTok,
		URL:         url,
		Title:       info.Title,
		Description: info.Description,
		Duration:    time.Duration(info.Duration) * time.Second,
		Thumbnail:   info.Thumbnail,
		Author:      info.Uploader,
		Views:       info.ViewCount,
		Tags:        info.Tags,
	}

	if info.UploadDate != "" {
		if t, err := time.Parse("20060102", info.UploadDate); err == nil {
			videoInfo.CreatedAt = t
		}
	}

	return videoInfo, nil
}

// Download scarica un video TikTok
func (b *TikTokBackend) Download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error) {
	if req.Retries <= 0 {
		req.Retries = 3
	}
	if req.OutputDir == "" {
		req.OutputDir = "/tmp/velox/downloads/tiktok"
	}

	videoID := extractTikTokID(req.URL)
	if videoID == "" {
		return nil, fmt.Errorf("could not extract TikTok video ID from URL")
	}

	if req.OutputFile == "" {
		req.OutputFile = videoID
	}

	outputPath := fmt.Sprintf("%s/%s.%%(ext)s", req.OutputDir, req.OutputFile)

	// TikTok-specific flags
	args := []string{
		"--format", "best[ext=mp4]/best",
		"--output", outputPath,
		"--no-playlist",
		"--restrict-filenames",
		"--user-agent", b.userAgent,
		"--referer", "https://www.tiktok.com/",
	}

	if req.Proxy != "" || b.proxy != "" {
		proxy := req.Proxy
		if proxy == "" {
			proxy = b.proxy
		}
		args = append(args, "--proxy", proxy)
	}

	if req.CookiesFile != "" {
		args = append(args, "--cookies", req.CookiesFile)
	}

	args = append(args, req.URL)

	var lastErr error
	for attempt := 1; attempt <= req.Retries; attempt++ {
		logger.Info("Downloading TikTok video",
			zap.String("url", req.URL),
			zap.String("video_id", videoID),
			zap.Int("attempt", attempt),
		)

		cmd := exec.CommandContext(ctx, b.ytdlpPath, args...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			// Trova file scaricato
			files, err := findDownloadedFiles(outputPath)
			if err != nil || len(files) == 0 {
				lastErr = fmt.Errorf("could not find downloaded file")
				continue
			}

			info, _ := b.GetInfo(ctx, req.URL)
			
			return &DownloadResult{
				VideoID:   videoID,
				Platform:  PlatformTikTok,
				Title:     info.Title,
				FilePath:  files[0],
				Duration:  info.Duration,
				Thumbnail: info.Thumbnail,
				Author:    info.Author,
			}, nil
		}

		lastErr = fmt.Errorf("download failed: %w\n%s", err, string(output))
		logger.Warn("Download attempt failed",
			zap.String("video_id", videoID),
			zap.Int("attempt", attempt),
			zap.Error(lastErr),
		)

		// Attendi prima di riprovare
		if attempt < req.Retries {
			waitTime := time.Duration(attempt) * 3 * time.Second // TikTok ha rate limiting più aggressivo
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime):
			}
		}
	}

	return nil, fmt.Errorf("download failed after %d attempts: %w", req.Retries, lastErr)
}

// Search cerca video su TikTok
func (b *TikTokBackend) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 30 {
		maxResults = 30 // TikTok ha limiti più stringenti
	}

	searchQuery := fmt.Sprintf("tiktoksearch%d:%s", maxResults, query)

	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(uploader)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		searchQuery,
		"--user-agent", b.userAgent,
	}

	if b.proxy != "" {
		args = append(args, "--proxy", b.proxy)
	}

	cmd := exec.CommandContext(ctx, b.ytdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("TikTok search failed: %w", err)
	}

	return b.parseSearchOutput(string(output)), nil
}

// GetTranscript estrae transcript da TikTok (generalmente non disponibile)
func (b *TikTokBackend) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	return "", fmt.Errorf("transcripts not available for TikTok videos")
}

// Platform ritorna la piattaforma
func (b *TikTokBackend) Platform() Platform {
	return PlatformTikTok
}

// IsAvailable verifica se yt-dlp è disponibile
func (b *TikTokBackend) IsAvailable(ctx context.Context) error {
	// Use a short timeout for availability check
	shortCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(shortCtx, b.ytdlpPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("yt-dlp not available: %w", err)
	}

	logger.Info("TikTok backend available",
		zap.String("ytdlp_version", strings.TrimSpace(string(output))),
	)

	return nil
}

// parseSearchOutput parsia l'output della ricerca
func (b *TikTokBackend) parseSearchOutput(output string) []SearchResult {
	var results []SearchResult

	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 8 {
			continue
		}

		var views int64
		fmt.Sscanf(parts[3], "%d", &views)

		var duration int
		fmt.Sscanf(parts[4], "%d", &duration)

		results = append(results, SearchResult{
			ID:         parts[0],
			Platform:   PlatformTikTok,
			URL:        fmt.Sprintf("https://www.tiktok.com/video/%s", parts[0]),
			Title:      parts[1],
			Author:     parts[2],
			Views:      views,
			Duration:   time.Duration(duration) * time.Second,
			UploadDate: parts[5],
			Thumbnail:  parts[7],
		})
	}

	return results
}

// Helper per trovare file scaricati
func findDownloadedFiles(pattern string) ([]string, error) {
	extensions := []string{".mp4", ".webm", ".mov"}
	var files []string

	for _, ext := range extensions {
		basePath := strings.TrimSuffix(pattern, ".%(ext)s")
		filePath := basePath + ext
		if _, err := exec.LookPath(filePath); err == nil {
			files = append(files, filePath)
		}
	}

	return files, nil
}
