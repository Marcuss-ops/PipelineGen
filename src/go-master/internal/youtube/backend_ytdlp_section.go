package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// SectionDownloadOptions configures a precise yt-dlp download for a time range.
type SectionDownloadOptions struct {
	YtDlpPath          string
	URL                string
	OutputFile         string
	StartSec           int
	Duration           int
	Format             string
	CookiesFile        string
	DefaultCookiesFile string
	Proxy              string
	MaxFilesize        string
	MaxHeight          int
}

// DownloadSection downloads a time-bounded section of a YouTube video using yt-dlp.
func DownloadSection(ctx context.Context, opts SectionDownloadOptions) error {
	if opts.YtDlpPath == "" {
		opts.YtDlpPath = "yt-dlp"
	}
	if opts.URL == "" {
		return fmt.Errorf("missing video URL")
	}
	if opts.OutputFile == "" {
		return fmt.Errorf("missing output file")
	}
	if opts.Duration <= 0 {
		return fmt.Errorf("missing duration")
	}
	if opts.MaxFilesize == "" {
		opts.MaxFilesize = "1G"
	}
	if opts.Format == "" {
		maxHeight := opts.MaxHeight
		if maxHeight <= 0 {
			maxHeight = 1080
		}
		opts.Format = fmt.Sprintf("best[height<=%d][ext=mp4]/best[height<=%d]/best", maxHeight, maxHeight)
	}

	endSec := opts.StartSec + opts.Duration
	sectionArg := fmt.Sprintf("*%d:%02d-%d:%02d", opts.StartSec/60, opts.StartSec%60, endSec/60, endSec%60)

	cookiesCandidates := collectCookiesCandidates(opts.CookiesFile, opts.DefaultCookiesFile)
	if len(cookiesCandidates) == 0 {
		cookiesCandidates = []string{""}
	}

	var lastErr error
	for attempt, cookieFile := range cookiesCandidates {
		authArgs := BuildYtDlpAuthArgs(cookieFile, "")
		cookieLabel := cookieFile
		if cookieFile != "" {
			cookieLabel = fmt.Sprintf("%s age=%s", cookieFile, cookieAge(cookieFile).Round(time.Minute))
		}

		baseArgs := []string{
			"--format", opts.Format,
			"--download-section", sectionArg,
			"-o", opts.OutputFile,
			"--no-playlist",
			"--restrict-filenames",
			"--no-warnings",
			"--force-keyframes-at-cuts",
			"--max-filesize", opts.MaxFilesize,
			opts.URL,
		}
		baseArgs = append(authArgs, baseArgs...)

		for _, extractorArgs := range YouTubeExtractorArgsVariants() {
			args := append([]string{}, baseArgs...)
			if extractorArgs != "" {
				args = append(args, "--extractor-args", extractorArgs)
			}

			cmd := exec.CommandContext(ctx, opts.YtDlpPath, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				lastErr = fmt.Errorf("yt-dlp section download failed (cookie=%s extractor-args=%q): %w\n%s", cookieLabel, extractorArgs, err, string(output))
				continue
			}

			info, err := os.Stat(opts.OutputFile)
			if err != nil {
				lastErr = fmt.Errorf("downloaded clip not found: %w", err)
				continue
			}
			if info.Size() < 1000 {
				lastErr = fmt.Errorf("downloaded clip too small (%d bytes)", info.Size())
				continue
			}
			return nil
		}

		if attempt < len(cookiesCandidates)-1 {
			backoff := time.Duration(1<<uint(attempt)) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return lastErr
}
