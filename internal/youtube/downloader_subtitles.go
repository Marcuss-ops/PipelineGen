package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// GetSubtitles scarica sottotitoli da un video YouTube
func (d *Downloader) GetSubtitles(ctx context.Context, url, language string) (*LegacySubtitleResult, error) {
	langCode := language
	if len(language) > 2 {
		langCode = language[:2] // es. "en-US" -> "en"
	}

	// Crea directory temporanea
	tmpDir, err := os.MkdirTemp("", "yt_subs_")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	languagesToTry := []string{langCode, "en", "en-orig", "en-US"}

	for _, tryLang := range languagesToTry {
		args := []string{
			"--write-auto-sub", "--write-sub",
			"--sub-lang", tryLang,
			"--sub-format", "vtt",
			"--skip-download", "--no-warnings", "--quiet",
			"-o", filepath.Join(tmpDir, "%(id)s.%(ext)s"),
			url,
		}

		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.Debug("Subtitle extraction attempt failed",
				zap.String("lang", tryLang),
				zap.String("output", string(output)),
				zap.Error(err))
			continue
		}

		// Cerca file VTT
		files, err := filepath.Glob(filepath.Join(tmpDir, "*.vtt"))
		if err != nil || len(files) == 0 {
			continue
		}

		vttContent, err := os.ReadFile(files[0])
		if err != nil {
			continue
		}

		content := string(vttContent)
		logger.Info("Downloaded YouTube subtitles",
			zap.String("url", url),
			zap.String("lang", tryLang),
			zap.Int("chars", len(content)))

		return &LegacySubtitleResult{
			YouTubeURL: url,
			Language:   tryLang,
			CharCount:  len(content),
			VTTContent: content,
		}, nil
	}

	return nil, fmt.Errorf("no subtitles found for %s", url)
}
