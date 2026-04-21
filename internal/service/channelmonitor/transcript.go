package channelmonitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// extractTranscript extracts transcript from a YouTube video using yt-dlp
func (m *Monitor) extractTranscript(ctx context.Context, videoID string) (string, error) {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	logger.Info("Extracting transcript from YouTube video",
		zap.String("video_id", videoID),
		zap.String("language", "en"),
	)

	// Create a timeout context for transcript extraction
	transcriptCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Try yt-dlp with auto-subtitle extraction
	args := []string{
		"--write-auto-sub",
		"--write-sub",
		"--sub-lang", "en",
		"--skip-download",
		"--sub-format", "srt",
		"-o", fmt.Sprintf("/tmp/%s.%%(ext)s", videoID),
		"--no-warnings",
		url,
	}
	if m.config.CookiesPath != "" {
		args = append(args, "--cookies", m.config.CookiesPath)
	}

	cmd := exec.CommandContext(transcriptCtx, m.config.YtDlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Debug("yt-dlp subtitle extraction failed",
			zap.String("video_id", videoID),
			zap.String("output", string(output)),
		)
	}

	// Try to find the .srt file
	pattern := fmt.Sprintf("/tmp/%s*.en.srt", videoID)
	files, _ := filepath.Glob(pattern)
	if len(files) == 0 {
		pattern = fmt.Sprintf("/tmp/%s*.srt", videoID)
		files, _ = filepath.Glob(pattern)
	}
	if len(files) > 0 {
		data, err := os.ReadFile(files[0])
		if err == nil {
			text := cleanSubtitle(string(data))
			// Cleanup temp files
			for _, f := range files {
				os.Remove(f)
			}
			if len(text) > 100 {
				return text, nil
			}
		}
	}

	// Fallback: try simple yt-dlp dump with transcript
	args2 := []string{
		"--skip-download",
		"--write-info-json",
		"-o", fmt.Sprintf("/tmp/%s.%%(ext)s", videoID),
		"--no-warnings",
		url,
	}
	cmd2 := exec.CommandContext(transcriptCtx, m.config.YtDlpPath, args2...)
	cmd2.Run()

	// If still no transcript, return empty and let caller skip
	return "", fmt.Errorf("no transcript available for video %s", videoID)
}

// cleanSubtitle cleans SRT subtitle format
func cleanSubtitle(srt string) string {
	// Remove SRT timestamps and metadata
	lines := strings.Split(srt, "\n")
	var cleaned []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines, index numbers, and timestamp lines
		if line == "" {
			continue
		}
		if _, err := fmt.Sscanf(line, "%d", &struct{}{}); err == nil {
			continue // Index line
		}
		if strings.Contains(line, "-->") {
			continue // Timestamp line
		}
		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, " ")
}
