// internal/infrastructure/downloader/downloader_staging.go —
// staging + local-file handling: section normalization, timestamp
// parsing, output path resolution, and file:// copies.
// Extracted from downloader.go; no behavior change.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// normalizeSectionFile removes the keyframe padding introduced by yt-dlp's
// --download-sections.  The section selector is a request, not a guarantee:
// with --force-keyframes-at-cuts ffmpeg may leave several seconds outside the
// requested interval in the resulting container.  Persisting that padded
// file makes the catalog lie about clip duration and breaks deterministic
// 4-second stock.  Re-encode the short segment to the requested duration.
func normalizeSectionFile(ctx context.Context, path, section string) error {
	rangeSpec := strings.TrimPrefix(section, "*")
	parts := strings.SplitN(rangeSpec, "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid section range %q", section)
	}
	start, err := parseTimestampSeconds(parts[0])
	if err != nil {
		return fmt.Errorf("parse section start %q: %w", section, err)
	}
	end, err := parseTimestampSeconds(parts[1])
	if err != nil || end <= start {
		return fmt.Errorf("parse section end %q: %w", section, err)
	}
	duration := end - start
	tmp := path + ".normalized.mp4"
	_, runErr := (defaultRunner{}).Run(ctx, "ffmpeg", []string{
		"-v", "error", "-y", "-i", path, "-t", strconv.FormatFloat(duration, 'f', 3, 64),
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", tmp,
	}, process.Options{Timeout: 10 * time.Minute, CombinedOutput: true})
	if runErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("normalize clip %q: %w", path, runErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace normalized clip %q: %w", path, err)
	}
	return nil
}

func parseTimestampSeconds(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if strings.Count(value, ":") == 0 {
		return strconv.ParseFloat(value, 64)
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("unsupported timestamp %q", value)
	}
	h, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	m, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, err
	}
	s, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	return h*3600 + m*60 + s, nil
}

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
