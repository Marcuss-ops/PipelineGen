package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// SectionDownloadOptions configures a precise yt-dlp download for a time range.
type SectionDownloadOptions struct {
	YtDlpPath          string
	URL                string
	OutputFile         string
	StartSec           int
	Duration           int
	CookiesFile        string
	DefaultCookiesFile string
	Proxy              string
	MaxFilesize        string
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

	endSec := opts.StartSec + opts.Duration
	sectionArg := fmt.Sprintf("*%d:%02d-%d:%02d", opts.StartSec/60, opts.StartSec%60, endSec/60, endSec%60)

	baseArgs := []string{
		"--download-section", sectionArg,
		"-o", opts.OutputFile,
		"--no-playlist",
		"--restrict-filenames",
		"--no-warnings",
		"--force-keyframes-at-cuts",
		"--max-filesize", opts.MaxFilesize,
		opts.URL,
	}
	baseArgs = append(BuildYtDlpAuthArgs(opts.CookiesFile, opts.DefaultCookiesFile), baseArgs...)

	var lastErr error
	for _, extractorArgs := range YouTubeExtractorArgsVariants() {
		args := append([]string{}, baseArgs...)
		if extractorArgs != "" {
			args = append(args, "--extractor-args", extractorArgs)
		}

		cmd := exec.CommandContext(ctx, opts.YtDlpPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("yt-dlp section download failed (extractor-args=%q): %w\n%s", extractorArgs, err, string(output))
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

	return lastErr
}
