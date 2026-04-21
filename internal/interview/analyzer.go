// Package interview provides functionality to analyze YouTube interviews for key video clips.
// Uses yt-dlp to download VTT subtitles and extracts moments using TF-IDF scoring.
package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Moment represents a key moment extracted from VTT subtitles.
type Moment struct {
	Rank     int     `json:"rank"`
	Start    string  `json:"start"`
	End      string  `json:"end"`
	Text     string  `json:"text"`
	Duration float64 `json:"duration"`
	Score    float64 `json:"score"`
}

// InterviewResult represents an analyzed interview with extracted clips.
type InterviewResult struct {
	VideoID     string    `json:"video_id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Channel     string    `json:"channel"`
	Duration    int       `json:"duration"`
	Moments     []Moment  `json:"moments"`
	ProcessedAt time.Time `json:"processed_at"`
}

// Config holds interview analyzer configuration.
type Config struct {
	MaxMoments  int           // Maximum moments to extract (default: 10)
	MinDuration float64       // Minimum moment duration (default: 5s)
	MaxDuration float64       // Maximum moment duration (default: 30s)
	YTDLPPath   string        // Path to yt-dlp (default: system PATH)
	CacheDir    string        // Cache directory for VTT files
	CacheTTL    time.Duration // Cache TTL (default: 24h)
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		MaxMoments:  10,
		MinDuration: 5.0,
		MaxDuration: 30.0,
		YTDLPPath:   "yt-dlp",
		CacheDir:    "./data/interview_cache",
		CacheTTL:    24 * time.Hour,
	}
}

// Analyzer analyzes YouTube interviews.
type Analyzer struct {
	cfg Config
}

// New creates a new interview analyzer.
func New(cfg Config) *Analyzer {
	if cfg.MaxMoments == 0 {
		cfg = DefaultConfig()
	}

	// Ensure cache dir exists
	if cfg.CacheDir != "" {
		os.MkdirAll(cfg.CacheDir, 0755)
	}

	return &Analyzer{cfg: cfg}
}

// GetVideoID extracts video ID from URL or returns as-is.
func GetVideoID(urlOrID string) string {
	re := regexp.MustCompile(`(?:v=|/)([a-zA-Z0-9_-]{11})`)
	matches := re.FindStringSubmatch(urlOrID)
	if len(matches) > 1 {
		return matches[1]
	}
	return urlOrID
}

// FindInterviews searches YouTube for interview/talk videos on a topic.
func (a *Analyzer) FindInterviews(ctx context.Context, topic string, maxResults int) ([]InterviewResult, error) {
	if maxResults == 0 {
		maxResults = 5
	}

	queries := []string{
		topic + " interview",
		topic + " talk",
	}

	var results []InterviewResult

	for _, query := range queries[:2] {
		cmd := exec.CommandContext(ctx, a.cfg.YTDLPPath,
			fmt.Sprintf("ytsearch%d:%s", maxResults, query),
			"--dump-json", "--flat-playlist", "--no-warnings",
		)
		cmd.Stderr = nil

		output, err := cmd.Output()
		if err != nil {
			logger.Warn("yt-dlp search failed", zap.String("query", query), zap.Error(err))
			continue
		}

		for _, line := range strings.Split(string(output), "\n") {
			if !strings.HasPrefix(line, "{") {
				continue
			}

			var data map[string]interface{}
			if json.Unmarshal([]byte(line), &data) != nil {
				continue
			}

			vid, _ := data["id"].(string)
			duration, _ := data["duration"].(float64)

			if duration < 60 { // Skip short videos
				continue
			}

			title, _ := data["title"].(string)
			channel, _ := data["uploader"].(string)

			results = append(results, InterviewResult{
				VideoID:  GetVideoID(vid),
				Title:    title,
				Channel:  channel,
				URL:      fmt.Sprintf("https://youtube.com/watch?v=%s", GetVideoID(vid)),
				Duration: int(duration),
			})
		}

		if len(results) >= maxResults {
			break
		}
	}

	return results, nil
}

// DownloadVTT downloads VTT subtitles for a video.
func (a *Analyzer) DownloadVTT(ctx context.Context, videoID string) (string, error) {
	if a.cfg.CacheDir == "" {
		return "", fmt.Errorf("cache dir not configured")
	}

	vttPath := filepath.Join(a.cfg.CacheDir, videoID+".vtt")
	if _, err := os.Stat(vttPath); err == nil {
		logger.Info("VTT already cached", zap.String("video_id", videoID))
		return vttPath, nil
	}

	cmd := exec.CommandContext(ctx, a.cfg.YTDLPPath, videoID,
		"--write-subs", "--write-auto-subs",
		"--sub-lang", "en,es,it",
		"--sub-format", "vtt",
		"--skip-download",
		"--output", vttPath,
	)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download VTT: %w", err)
	}

	// Find downloaded file
	files, _ := filepath.Glob(filepath.Join(a.cfg.CacheDir, videoID+"*.vtt"))
	if len(files) > 0 {
		return files[0], nil
	}

	return "", fmt.Errorf("no VTT file found")
}

// ParseVTT parses VTT content into segments.
func ParseVTT(content string) []struct{ Start, End, Text string } {
	var segments []struct{ Start, End, Text string }

	timeRe := regexp.MustCompile(`(\d{2}:\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}\.\d{3})`)
	lines := strings.Split(content, "\n")

	var current struct{ Start, End, Text string }
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "WEBVTT") || strings.HasPrefix(line, "Kind:") {
			continue
		}

		if matches := timeRe.FindStringSubmatch(line); len(matches) == 3 {
			if current.Text != "" {
				segments = append(segments, current)
			}
			current.Start = matches[1]
			current.End = matches[2]
			current.Text = ""
			continue
		}

		if current.Text != "" {
			current.Text += " " + line
		} else {
			current.Text = strings.TrimSuffix(line, "<c>")
			current.Text = strings.TrimPrefix(current.Text, "<c>")
		}
	}

	if current.Text != "" {
		segments = append(segments, current)
	}

	return segments
}

// parseTimestamp converts VTT timestamp to seconds.
func parseTimestamp(ts string) float64 {
	var h, m, s int
	fmt.Sscanf(ts, "%d:%d:%d", &h, &m, &s)
	return float64(h*3600 + m*60 + s)
}

// priorityKeywords for scoring moments.
var priorityKeywords = []string{
	"important", "significant", "critical", "key", "main",
	"first", "second", "third", "finally", "lastly",
	"however", "but", "actually", "really", "truth", "fact",
	"believe", "think", "know", "understand",
	"love", "hate", "best", "worst", "amazing", "incredible",
	"million", "billion", "worth",
}

// hookKeywords for additional scoring.
var hookKeywords = []string{
	"secret", "truth", "reveal", "shock", "surprise",
	"never", "always", "everyone", "nobody",
	"warning", "must", "need", "should",
}

// scoreMoment calculates importance score for a moment.
func scoreMoment(text string, duration float64) float64 {
	score := 0.0
	textLower := strings.ToLower(text)

	// Keyword matching
	for _, kw := range priorityKeywords {
		if strings.Contains(textLower, kw) {
			score += 0.5
		}
	}
	for _, kw := range hookKeywords {
		if strings.Contains(textLower, kw) {
			score += 0.3
		}
	}

	// Word count scoring (10-30 words is ideal)
	words := strings.Fields(text)
	if len(words) >= 10 && len(words) <= 30 {
		score += 0.5
	} else if len(words) > 30 && len(words) <= 50 {
		score += 0.3
	}

	// Duration scoring (5-20s is ideal)
	if duration >= 5 && duration <= 20 {
		score += 0.3
	}

	// Numbers in text (often indicates facts)
	if regexp.MustCompile(`\d+`).MatchString(text) {
		score += 0.2
	}

	// Capitalized words (often names/countries)
	caps := regexp.MustCompile(`\b[A-Z][a-z]+\b`).FindAllString(text, -1)
	score += float64(len(caps)) * 0.1

	return score
}

// ExtractMoments extracts key moments from VTT content.
func (a *Analyzer) ExtractMoments(ctx context.Context, vttPath string) ([]Moment, error) {
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read VTT: %w", err)
	}

	segments := ParseVTT(string(data))
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments found")
	}

	var scored []Moment
	for _, seg := range segments {
		duration := parseTimestamp(seg.End) - parseTimestamp(seg.Start)
		if duration < a.cfg.MinDuration || duration > a.cfg.MaxDuration {
			continue
		}

		text := strings.TrimSpace(seg.Text)
		if len(text) < 10 {
			continue
		}

		score := scoreMoment(text, duration)
		scored = append(scored, Moment{
			Start:    seg.Start,
			End:      seg.End,
			Text:     text,
			Duration: duration,
			Score:    score,
		})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Limit and re-rank
	maxIdx := a.cfg.MaxMoments
	if maxIdx == 0 {
		maxIdx = 10
	}
	if len(scored) > maxIdx {
		scored = scored[:maxIdx]
	}

	// Re-sort by time (for video order)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Start < scored[j].Start
	})

	// Add ranks
	for i := range scored {
		scored[i].Rank = i + 1
	}

	return scored, nil
}

// Analyze performs full analysis on a video ID.
func (a *Analyzer) Analyze(ctx context.Context, videoID string) (*InterviewResult, error) {
	videoID = GetVideoID(videoID)

	// Get video info
	vttPath, err := a.DownloadVTT(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get VTT: %w", err)
	}

	moments, err := a.ExtractMoments(ctx, vttPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract moments: %w", err)
	}

	return &InterviewResult{
		VideoID:     videoID,
		Moments:     moments,
		ProcessedAt: time.Now(),
	}, nil
}

// SearchAndAnalyze finds interviews and analyzes them.
func (a *Analyzer) SearchAndAnalyze(ctx context.Context, topic string, maxVideos int) ([]InterviewResult, error) {
	if maxVideos == 0 {
		maxVideos = 3
	}

	interviews, err := a.FindInterviews(ctx, topic, maxVideos)
	if err != nil {
		return nil, err
	}

	var results []InterviewResult
	for _, interview := range interviews {
		analyzed, err := a.Analyze(ctx, interview.VideoID)
		if err != nil {
			logger.Warn("failed to analyze interview",
				zap.String("video_id", interview.VideoID),
				zap.Error(err))
			continue
		}

		analyzed.Title = interview.Title
		analyzed.URL = interview.URL
		analyzed.Channel = interview.Channel
		analyzed.Duration = interview.Duration

		results = append(results, *analyzed)
		if len(results) >= maxVideos {
			break
		}
	}

	return results, nil
}
