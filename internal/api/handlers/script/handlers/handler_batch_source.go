package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"velox/go-master/internal/config"
	"velox/go-master/pkg/urlutil"
)

type youtubeMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Channel     string `json:"channel"`
	Fulltitle   string `json:"fulltitle"`
	UploadDate  string `json:"upload_date"`
	WebpageURL  string `json:"webpage_url"`
	Duration    int    `json:"duration"`
	Chapters    []struct {
		Title     string  `json:"title"`
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	} `json:"chapters"`
}

func resolveBatchSourceText(ctx context.Context, cfg *config.Config, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "empty", nil
	}
	if !isYouTubeSourceURL(raw) {
		return raw, "inline_text", nil
	}
	if sourceText, err := buildYouTubeSourceBundle(ctx, cfg, raw); err == nil && strings.TrimSpace(sourceText) != "" {
		return sourceText, "youtube_url", nil
	} else if err != nil {
		return raw, "youtube_url_fallback", err
	}
	return raw, "youtube_url_fallback", nil
}

func isYouTubeSourceURL(raw string) bool {
	_, err := urlutil.ExtractVideoID(raw)
	return err == nil
}

func buildYouTubeSourceBundle(ctx context.Context, cfg *config.Config, rawURL string) (string, error) {
	videoID, err := urlutil.ExtractVideoID(rawURL)
	if err != nil {
		return "", err
	}

	ytdlpPath := "yt-dlp"
	if cfg != nil {
		ytdlpPath = cfg.External.ResolvedYtdlpPath()
	}
	canonicalURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	metaJSON, err := runYTDLPJSON(ctx, ytdlpPath, canonicalURL)
	if err != nil {
		metaJSON = nil
	}

	var meta youtubeMetadata
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &meta)
	}

	transcript, _ := extractYouTubeTranscript(ctx, ytdlpPath, canonicalURL)

	var b strings.Builder
	b.WriteString("YOUTUBE SOURCE\n")
	b.WriteString("URL: ")
	b.WriteString(canonicalURL)
	b.WriteString("\n")
	if meta.Title != "" {
		b.WriteString("Title: ")
		b.WriteString(meta.Title)
		b.WriteString("\n")
	}
	if meta.Channel != "" {
		b.WriteString("Channel: ")
		b.WriteString(meta.Channel)
		b.WriteString("\n")
	}
	if meta.UploadDate != "" {
		b.WriteString("Upload Date: ")
		b.WriteString(meta.UploadDate)
		b.WriteString("\n")
	}
	if meta.Duration > 0 {
		b.WriteString(fmt.Sprintf("Duration: %d seconds\n", meta.Duration))
	}
	if meta.Description != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(strings.TrimSpace(meta.Description))
		b.WriteString("\n")
	}
	if len(meta.Chapters) > 0 {
		b.WriteString("\nChapters:\n")
		for i, ch := range meta.Chapters {
			b.WriteString(fmt.Sprintf("%d. %s (%.0f-%.0f)\n", i+1, strings.TrimSpace(ch.Title), ch.StartTime, ch.EndTime))
		}
	}
	if transcript != "" {
		b.WriteString("\nTranscript:\n")
		b.WriteString(transcript)
		b.WriteString("\n")
	}

	sourceText := strings.TrimSpace(b.String())
	if sourceText == "" {
		return "", fmt.Errorf("empty youtube source bundle")
	}
	return sourceText, nil
}

func runYTDLPJSON(ctx context.Context, ytdlpPath, rawURL string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ytdlpPath, "--dump-json", "--skip-download", rawURL)
	return cmd.Output()
}

func extractYouTubeTranscript(ctx context.Context, ytdlpPath, rawURL string) (string, error) {
	tempDir, err := os.MkdirTemp("", "yt_batch_subs_")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)

	subPrefix := filepath.Join(tempDir, "subs")
	cmd := exec.CommandContext(ctx, ytdlpPath,
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en,it", "--sub-format", "vtt",
		"-o", subPrefix,
		rawURL,
	)
	_ = cmd.Run()

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "subs.") || !strings.HasSuffix(entry.Name(), ".vtt") {
			continue
		}
		transcript, err := parseTranscriptVTT(filepath.Join(tempDir, entry.Name()))
		if err != nil {
			continue
		}
		if transcript != "" {
			return transcript, nil
		}
	}

	return "", fmt.Errorf("no youtube transcript available")
}

func parseTranscriptVTT(vttPath string) (string, error) {
	data, err := os.ReadFile(vttPath)
	if err != nil {
		return "", err
	}

	content := string(data)
	content = regexp.MustCompile(`(?s)^WEBVTT.*?\n\n`).ReplaceAllString(content, "")
	blocks := strings.Split(content, "\n\n")
	var out []string

	timeRegex := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3})`)
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 {
			continue
		}
		var textLines []string
		hasTimestamp := false
		for _, line := range lines {
			if timeRegex.MatchString(line) {
				hasTimestamp = true
				continue
			}
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "align:") || strings.HasPrefix(line, "position:") {
				continue
			}
			textLines = append(textLines, line)
		}
		if !hasTimestamp || len(textLines) == 0 {
			continue
		}
		text := strings.Join(textLines, " ")
		text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
		text = strings.TrimSpace(text)
		if text != "" {
			out = append(out, text)
		}
	}

	result := strings.Join(out, " ")
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	result = strings.TrimSpace(result)
	if len(strings.Fields(result)) == 0 {
		return "", fmt.Errorf("empty transcript")
	}
	return result, nil
}
