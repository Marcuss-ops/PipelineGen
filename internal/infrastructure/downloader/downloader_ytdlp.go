// internal/infrastructure/downloader/downloader_ytdlp.go —
// yt-dlp execution paths (Download / DownloadRange / DownloadSections).
// Extracted from downloader.go. Sectioned downloads intentionally keep
// yt-dlp/ffmpeg in control instead of selecting an external downloader.
package downloader

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
)

func shouldForceNoPlaylist(url string) bool {
	return strings.Contains(url, "youtube.com") ||
		strings.Contains(url, "youtu.be") ||
		strings.Contains(url, "artlist")
}

func (d *YTDLPDownloader) addExternalDownloaderArgs(args []string) []string {
	if _, err := exec.LookPath("aria2c"); err == nil {
		args = append(args, "--external-downloader", "aria2c", "--external-downloader-args", "aria2c:-j8 -s8 -x8 -k1M")
	}
	return args
}

// Download downloads a full video.
func (d *YTDLPDownloader) Download(ctx context.Context, req *DownloadRequest) error {
	if err := security.ValidateDownloadURL(req.URL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed, err := url.Parse(req.URL); err == nil && parsed.Scheme == "file" {
		return d.downloadLocalFile(req, parsed)
	}

	// buildArgs is called once per player-client attempt so the fallback
	// loop can re-run a bot-checked download with an alternate client
	// without duplicating the command construction.
	buildArgs := func(playerClient string) []string {
		args := []string{}
		if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
			args = append(args, "--no-playlist")
		}

		// Resume interrupted downloads from yt-dlp's on-disk .part files
		// instead of restarting from 0%. The stock pipeline's staging root
		// is persistent across process restarts (acquisition
		// FilesystemStager), so a job re-claimed after a graceful server
		// restart continues the in-flight download rather than re-fetching
		// the whole source (PR-STOCK-RESUME, August 2026). Explicit flag
		// (rather than relying on yt-dlp's default) documents the intent
		// and survives any future config/flag drift.
		args = append(args, "--continue")

		// Blocco 5 (July 2026): BaseArgs centralizes cookies, JS runtime,
		// --no-warnings, and extractor-args. Format selection is via
		// FormatArg so the downloader doesn't inline the -f string.
		args = append(args, d.cmdBuilder.BaseArgsForClient(req.URL, req.UseCookies, playerClient)...)
		if len(req.DownloadSections) > 0 {
			args = append(args, d.cmdBuilder.SectionFormatArg(true)...)
		} else {
			args = append(args, d.cmdBuilder.FormatArg(true)...)
		}

		// aria2c is intentionally limited to full-source downloads. Section
		// downloads must remain under yt-dlp/ffmpeg control so the time window
		// is preserved; see DownloadRange and DownloadSections below.
		if len(req.DownloadSections) == 0 {
			args = d.addExternalDownloaderArgs(args)
		}

		// Add Artlist-specific args (cookies, headers, impersonation).
		// July 2026 (PR-ARTLIST-COOKIES-CONFIG): the --cookies path is now
		// config-driven (cfg.External.ArtlistCookiesPath, env ARTLIST_COOKIES_PATH).
		// When empty (the godlike/07 fail-closed default), the --cookies flag is
		// SKIPPED entirely so operators see a visible 403 from Artlist instead of
		// a silent `--cookies /nonexistent/path` failure on a hardcoded path.
		if strings.Contains(req.URL, "artlist") {
			if d.artlistCookiesPath != "" {
				args = append(args, "--cookies", d.artlistCookiesPath)
			}
			args = append(args, "--add-header", "Referer:https://artlist.io/")
			args = append(args, "--add-header", "Origin:https://artlist.io/")
			args = append(args, "--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
			args = append(args, "--extractor-args", "generic:impersonate")
		}

		if req.Format != "" {
			args = append(args, "-f", req.Format)
		}
		mergeFormat := req.MergeFormat
		// YouTube's best format is commonly a separate video+audio pair. Always
		// merge that pair into one deterministic file; otherwise the resolver can
		// return the video-only .mp4 member before the audio merge completes.
		if mergeFormat == "" && (strings.Contains(req.URL, "youtube.com") || strings.Contains(req.URL, "youtu.be")) {
			mergeFormat = "mp4"
		}
		if mergeFormat != "" {
			args = append(args, "--merge-output-format", mergeFormat)
		}

		outputTemplate := req.OutputPath
		if !strings.Contains(outputTemplate, "%(ext)s") {
			outputTemplate = outputTemplate + ".%(ext)s"
		}
		args = append(args, "-o", outputTemplate)

		if len(req.DownloadSections) > 0 {
			for _, section := range req.DownloadSections {
				args = append(args, "--download-sections", section)
			}
			if req.ForceKeyframes {
				args = append(args, "--force-keyframes-at-cuts")
			} else if req.StreamCopy {
				args = append(args, "--downloader-args", "ffmpeg:-c copy")
			}
		}

		// August 2026 rate-limit pacing: random sleep before each YouTube
		// download keeps a hot IP from being hammered back-to-back.
		args = append(args, d.youtubeSleepIntervalArgs(req.URL)...)

		args = append(args, req.URL)
		return args
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	_, err := d.runWithTransportRetry(ctx, req.URL, func() (*process.Result, error) {
		return d.runWithClientFallback(ctx, req.URL, process.Options{
			Timeout:        timeout,
			CombinedOutput: true,
		}, buildArgs)
	})
	if err != nil {
		return err
	}

	// Blocco 5 (July 2026): verify the output file exists and is
	// non-empty after a successful yt-dlp exit (yt-dlp can exit zero
	// but leave no output).
	resolvedPath, resolveErr := ResolveDownloadedSegmentPath(outputTemplateFor(req.OutputPath))
	if resolveErr != nil {
		return fmt.Errorf("download succeeded but output file not found: %w", resolveErr)
	}
	if verifyErr := d.verifier.VerifyFile(resolvedPath); verifyErr != nil {
		return verifyErr
	}
	return nil
}

// outputTemplateFor expands a bare output path into the yt-dlp template
// form (appending .%(ext)s) that ResolveDownloadedSegmentPath expects.
func outputTemplateFor(outputPath string) string {
	if !strings.Contains(outputPath, "%(ext)s") {
		return outputPath + ".%(ext)s"
	}
	return outputPath
}

// DownloadRange downloads a single contiguous time range from a video.
// Unlike DownloadSections which makes N separate yt-dlp calls for N sections,
// this method makes ONE yt-dlp call for a single range.
// Returns the path to the downloaded file.
func (d *YTDLPDownloader) DownloadRange(ctx context.Context, req *DownloadRequest) (string, error) {
	if err := security.ValidateDownloadURL(req.URL); err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if len(req.DownloadSections) == 0 {
		return "", fmt.Errorf("no download sections specified")
	}

	// Create output directory if needed
	outputDir := filepath.Dir(req.OutputPath)
	if outputDir != "." && outputDir != "" {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return "", fmt.Errorf("failed to create output dir: %w", err)
		}
	}

	outputTemplate := req.OutputPath
	if !strings.Contains(outputTemplate, "%(ext)s") {
		outputTemplate = outputTemplate + ".%(ext)s"
	}

	buildArgs := func(playerClient string) []string {
		args := []string{}
		if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
			args = append(args, "--no-playlist")
		}
		args = append(args, d.cmdBuilder.BaseArgsForClient(req.URL, req.UseCookies, playerClient)...)
		args = append(args, d.cmdBuilder.SectionFormatArg(true)...)
		// Do not add aria2c here. yt-dlp must retain control of the
		// sectioned download and ffmpeg cut; an external downloader can
		// bypass or interfere with the requested time range.

		if req.Format != "" {
			args = append(args, "-f", req.Format)
		}
		if req.MergeFormat != "" {
			args = append(args, "--merge-output-format", req.MergeFormat)
		}

		for _, section := range req.DownloadSections {
			args = append(args, "--download-sections", section)
		}

		if req.ForceKeyframes {
			args = append(args, "--force-keyframes-at-cuts")
		}
		args = append(args, "-o", outputTemplate)

		args = append(args, d.youtubeSleepIntervalArgs(req.URL)...)
		args = append(args, req.URL)
		return args
	}

	_, err := d.runWithTransportRetry(ctx, req.URL, func() (*process.Result, error) {
		return d.runWithClientFallback(ctx, req.URL, process.Options{
			Timeout:        10 * time.Minute,
			CombinedOutput: true,
		}, buildArgs)
	})
	if err != nil {
		return "", fmt.Errorf("failed to download range: %w", err)
	}

	path, pathErr := ResolveDownloadedSegmentPath(outputTemplate)
	if pathErr != nil {
		return "", fmt.Errorf("download range succeeded but output file not found: %w", pathErr)
	}
	// Verify the downloaded artifact like Download does: yt-dlp can exit
	// zero while leaving an empty/truncated file, and a bot-checked
	// section download can write a stub container with no media data.
	// VerifyFile catches missing/empty files; the Step 5a ffprobe gate
	// catches zero-stream stubs downstream.
	if verifyErr := d.verifier.VerifyFile(path); verifyErr != nil {
		return "", verifyErr
	}
	return path, nil
}

// DownloadSections downloads specific time sections from a video.
// Returns paths to downloaded segment files.
//
// The output template is derived from req.OutputPath so that concurrent
// calls targeting different segments write to different files. Previously
// every call used a hardcoded "001_segment.%(ext)s" in the output directory,
// which caused a race condition when multiple segments ran in parallel:
// all goroutines wrote to the same temp file, producing identical output
// for every segment.
func (d *YTDLPDownloader) DownloadSections(ctx context.Context, req *DownloadRequest) ([]DownloadedSegment, error) {
	if err := security.ValidateDownloadURL(req.URL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if len(req.DownloadSections) == 0 {
		return nil, fmt.Errorf("no download sections specified")
	}

	// Create output directory if needed
	outputDir := filepath.Dir(req.OutputPath)
	if outputDir != "." && outputDir != "" {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create output dir: %w", err)
		}
	}

	// Derive the output template from req.OutputPath so each call gets its
	// own file.  E.g. req.OutputPath = "/tmp/raw_clip_name.mp4" produces
	// "/tmp/raw_clip_name.%(ext)s" — unique per segment name, not a shared
	// "001_segment.%(ext)s" that would collide in concurrent runs.
	basePath := strings.TrimSuffix(req.OutputPath, filepath.Ext(req.OutputPath))

	var results []DownloadedSegment
	for i, section := range req.DownloadSections {
		// Validate timestamp format
		if err := security.SanitizeTimestamp(section); err != nil {
			return nil, fmt.Errorf("invalid section %d: %w", i, err)
		}

		var outputTemplate string
		if len(req.DownloadSections) == 1 {
			outputTemplate = basePath + ".%(ext)s"
		} else {
			outputTemplate = fmt.Sprintf("%s_%03d.%%(ext)s", basePath, i+1)
		}

		buildArgs := func(playerClient string) []string {
			args := []string{}
			if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
				args = append(args, "--no-playlist")
			}
			args = append(args, d.cmdBuilder.BaseArgsForClient(req.URL, req.UseCookies, playerClient)...)
			args = append(args, d.cmdBuilder.SectionFormatArg(true)...)
			// Keep section downloads on yt-dlp/ffmpeg. aria2c is reserved
			// for full-source downloads because it cannot own the time-range
			// cut without risking a full or incorrectly bounded output.

			if req.Format != "" {
				args = append(args, "-f", req.Format)
			}
			if req.MergeFormat != "" {
				args = append(args, "--merge-output-format", req.MergeFormat)
			}

			args = append(args, "--download-sections", section)
			if req.ForceKeyframes {
				args = append(args, "--force-keyframes-at-cuts")
			}
			args = append(args, "-o", outputTemplate)

			args = append(args, d.youtubeSleepIntervalArgs(req.URL)...)
			args = append(args, req.URL)
			return args
		}

		_, err := d.runWithTransportRetry(ctx, req.URL, func() (*process.Result, error) {
			return d.runWithClientFallback(ctx, req.URL, process.Options{
				Timeout:        10 * time.Minute,
				CombinedOutput: true,
			}, buildArgs)
		})
		if err != nil {
			return results, fmt.Errorf("failed to download section %d: %w", i, err)
		}

		resolvedPath, pathErr := ResolveDownloadedSegmentPath(outputTemplate)
		if pathErr != nil {
			return results, fmt.Errorf("section %d: %w", i, pathErr)
		}
		// Verify the downloaded section like Download does: yt-dlp can exit
		// zero while leaving an empty/truncated file, and a bot-checked
		// section download can write a stub container with no media data.
		// VerifyFile catches missing/empty files; the Step 5a ffprobe gate
		// catches zero-stream stubs downstream.
		if verifyErr := d.verifier.VerifyFile(resolvedPath); verifyErr != nil {
			return results, fmt.Errorf("section %d: %w", i, verifyErr)
		}
		results = append(results, DownloadedSegment{
			Path:  resolvedPath,
			Name:  fmt.Sprintf("segment_%03d", i+1),
			Index: i,
		})
	}

	return results, nil
}
