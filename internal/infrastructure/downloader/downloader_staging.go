// internal/infrastructure/downloader/downloader_staging.go —
// staging + local-file handling: output path resolution and file:// copies.
// Extracted from downloader.go; no behavior change.
package downloader

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDownloadedSegmentPath finds the actual output file matching the
// yt-dlp output template. Returns the first non-part, non-ytdl file that
// exists and is non-empty. Returns an error if no matching file is found.
//
// Blocco 1c (July 2026): now returns (string, error) with os.Stat +
// size verification. Pre-fix returned a fallback ".mp4" path even when
// no file existed, producing silent false-success.
func ResolveDownloadedSegmentPath(outputTemplate string) (string, error) {
	base := strings.TrimSuffix(outputTemplate, ".%(ext)s")
	candidates, _ := filepath.Glob(base + ".*")
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate, ".part") {
			continue
		}
		if strings.HasSuffix(candidate, ".ytdl") {
			continue
		}
		fi, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if fi.Size() == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("resolveDownloadedSegmentPath: no output file found for template %q (globbed %d candidates)", outputTemplate, len(candidates))
}

func (d *YTDLPDownloader) downloadLocalFile(req *DownloadRequest, parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("file url parse failed")
	}
	sourcePath := parsed.Path
	if sourcePath == "" {
		return fmt.Errorf("file url has no path")
	}

	outputTemplate := req.OutputPath
	if !strings.Contains(outputTemplate, "%(ext)s") {
		outputTemplate = outputTemplate + ".%(ext)s"
	}
	dstPath := strings.TrimSuffix(outputTemplate, ".%(ext)s")
	ext := filepath.Ext(sourcePath)
	if ext == "" {
		ext = ".mp4"
	}
	finalPath := dstPath + ext

	outputDir := filepath.Dir(finalPath)
	if outputDir != "." && outputDir != "" {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}

	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", sourcePath, err)
	}
	defer src.Close()

	dst, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", finalPath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		_ = os.Remove(finalPath)
		return fmt.Errorf("copy source file %q to %q: %w", sourcePath, finalPath, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(finalPath)
		return fmt.Errorf("close output file %q: %w", finalPath, err)
	}
	if verifyErr := d.verifier.VerifyFile(finalPath); verifyErr != nil {
		return verifyErr
	}
	return nil
}
