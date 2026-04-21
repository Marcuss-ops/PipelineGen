package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"velox/go-master/internal/pipeline"
)

type YtDlpFetcher struct {
	binPath     string
	cookiesPath string
}

func NewYtDlpFetcher(binPath string) *YtDlpFetcher {
	if binPath == "" {
		binPath = "yt-dlp"
	}
	return &YtDlpFetcher{
		binPath:     binPath,
		cookiesPath: os.Getenv("VELOX_YTDLP_COOKIES"),
	}
}

func (f *YtDlpFetcher) FetchMetadata(ctx context.Context, videoID string) (*pipeline.VideoInfo, error) {
	url := "https://www.youtube.com/watch?v=" + videoID
	args := []string{"--dump-json", "--no-warnings"}
	if f.cookiesPath != "" {
		args = append(args, "--cookies", f.cookiesPath)
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, f.binPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata failed: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, err
	}

	info := &pipeline.VideoInfo{
		ID:          videoID,
		Title:       getString(raw, "title"),
		Channel:     getString(raw, "uploader"),
		Description: getString(raw, "description"),
		Duration:    int(getFloat(raw, "duration")),
	}

	return info, nil
}

func (f *YtDlpFetcher) FetchTranscript(ctx context.Context, videoID string) (string, error) {
	url := "https://www.youtube.com/watch?v=" + videoID
	args := []string{"--get-subs", "--skip-download", "--sub-langs", "en,it", "--write-auto-subs", "--stdout"}
	if f.cookiesPath != "" {
		args = append(args, "--cookies", f.cookiesPath)
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, f.binPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("transcript fetch failed: %w", err)
	}

	return string(output), nil
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}
