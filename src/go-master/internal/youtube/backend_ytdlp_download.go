package youtube

import (
	"context"
	"fmt"
	"os/exec"
)

func (b *YtDlpBackend) downloadOnce(ctx context.Context, url, outputPath, format, cookiesFile, proxy string, progress ProgressCallback) (*downloadResult, error) {
	baseArgs := []string{
		"--format", format,
		"--output", outputPath,
		"--no-playlist",
		"--restrict-filenames",
		"--no-warnings",
		"--max-filesize", "5G", // Prevent accidental terabyte downloads
	}

	baseArgs = append(baseArgs, BuildYtDlpAuthArgs(cookiesFile, b.config.DefaultCookiesFile)...)

	if proxy != "" {
		baseArgs = append(baseArgs, "--proxy", proxy)
	} else if b.config.Proxy != "" {
		baseArgs = append(baseArgs, "--proxy", b.config.Proxy)
	}

	// Add progress flag if callback provided
	if progress != nil {
		baseArgs = append(baseArgs, "--newline")
	}

	var lastErr error
	for _, extractorArgs := range YouTubeExtractorArgsVariants() {
		args := append([]string{}, baseArgs...)
		if extractorArgs != "" {
			args = append(args, "--extractor-args", extractorArgs)
		}
		args = append(args, url)

		cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("yt-dlp download failed (extractor-args=%q): %w\n%s", extractorArgs, err, string(output))
			continue
		}

		// Find the downloaded file
		files, err := findDownloadedFiles(outputPath)
		if err != nil || len(files) == 0 {
			lastErr = fmt.Errorf("could not find downloaded video file")
			continue
		}

		filePath := files[0]
		fileSize := getFileSize(filePath)

		return &downloadResult{
			FilePath: filePath,
			FileSize: fileSize,
		}, nil
	}

	return nil, lastErr
}

func (b *YtDlpBackend) downloadAudioOnce(ctx context.Context, url, outputPath, format, audioFormat, cookiesFile, proxy string, progress ProgressCallback) (*downloadResult, error) {
	baseArgs := []string{
		"--format", format,
		"--output", outputPath,
		"--no-playlist",
		"--extract-audio",
		"--audio-format", audioFormat,
		"--max-filesize", "5G", // Prevent accidental terabyte downloads
	}

	baseArgs = append(baseArgs, BuildYtDlpAuthArgs(cookiesFile, b.config.DefaultCookiesFile)...)

	if proxy != "" {
		baseArgs = append(baseArgs, "--proxy", proxy)
	} else if b.config.Proxy != "" {
		baseArgs = append(baseArgs, "--proxy", b.config.Proxy)
	}

	var lastErr error
	for _, extractorArgs := range YouTubeExtractorArgsVariants() {
		args := append([]string{}, baseArgs...)
		if extractorArgs != "" {
			args = append(args, "--extractor-args", extractorArgs)
		}
		args = append(args, url)

		cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("yt-dlp audio download failed (extractor-args=%q): %w\n%s", extractorArgs, err, string(output))
			continue
		}

		files, err := findDownloadedFiles(outputPath)
		if err != nil || len(files) == 0 {
			lastErr = fmt.Errorf("could not find downloaded audio file")
			continue
		}

		filePath := files[0]
		fileSize := getFileSize(filePath)

		return &downloadResult{
			FilePath: filePath,
			FileSize: fileSize,
		}, nil
	}

	return nil, lastErr
}

func (b *YtDlpBackend) buildFormatString(req *DownloadRequest) string {
	if req.Format != "" {
		return req.Format
	}

	if req.MaxHeight > 0 {
		return fmt.Sprintf("best[height<=%d][ext=mp4]/best[height<=%d]", req.MaxHeight, req.MaxHeight)
	}

	if b.config.DefaultMaxHeight > 0 {
		return fmt.Sprintf("best[height<=%d][ext=mp4]/best", b.config.DefaultMaxHeight)
	}

	return b.config.DefaultFormat
}
