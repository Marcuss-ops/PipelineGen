// Package downloader provides YouTube/social media download capabilities via yt-dlp.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor, mediapipeline, and artlist service.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// YTDLPDownloader handles YouTube/social media downloads via yt-dlp.
type YTDLPDownloader struct {
	path               string
	cookiesPath        string
	artlistCookiesPath string // July 2026 (PR-ARTLIST-COOKIES-CONFIG): empty = skip --cookies flag (godlike/07 fail-closed)
	cmdBuilder         *ytdlp.CommandBuilder
	verifier           *ytdlp.OutputVerifier
	// runner is the Pattern 0 port for executing external processes
	// (godlike/07 minimum-blast-radius + testability). The production
	// default is `defaultRunner{}` which wraps process.Run; tests inject
	// a `captureRunner` mock to capture argv without spawning yt-dlp.
	// See downloader_test.go for the canonical captureRunner pattern.
	runner ProcessRunner
}

func (d *YTDLPDownloader) run(ctx context.Context, args []string, opts process.Options) (*process.Result, error) {
	return d.runner.Run(ctx, d.path, args, opts)
}

func shouldForceNoPlaylist(url string) bool {
	return strings.Contains(url, "youtube.com") ||
		strings.Contains(url, "youtu.be") ||
		strings.Contains(url, "artlist")
}

// ProcessRunner is the Pattern 0 port for executing external processes.
// godlike/06 SSOT: this port lives in the downloader package (mirrors
// the metadata/subtitle adapter's ProcessRunnerPort pattern); the
// canonical default implementation (defaultRunner) wraps process.Run.
// Tests inject a mock (captureRunner) to capture argv without spawning
// an actual subprocess.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts process.Options) (*process.Result, error)
}

// defaultRunner is the production ProcessRunner that delegates to
// process.Run. It is the canonical SSOT for "what the downloader
// actually executes in production" — tests that inject a different
// ProcessRunner are validating the argv contract, NOT the exec contract.
type defaultRunner struct{}

func (defaultRunner) Run(ctx context.Context, name string, args []string, opts process.Options) (*process.Result, error) {
	return process.Run(ctx, name, args, opts)
}

// Compile-time pin (godlike/06 SSOT): defaultRunner MUST satisfy the
// ProcessRunner port. Drift in the Run signature surfaces as a build
// failure rather than a runtime panic.
var _ ProcessRunner = defaultRunner{}

// NewYTDLP creates a new yt-dlp downloader.
// Blocco 5 (July 2026): constructs the shared ytdlp.CommandBuilder once
// so YouTube-specific args (cookies, JS runtime, extractor-args) are
// centralized instead of duplicated.
func NewYTDLP(cfg *config.Config) *YTDLPDownloader {
	path := cfg.External.ResolvedYtdlpPath()
	cookiesPath := cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "cookies.txt"
	}
	return &YTDLPDownloader{
		path:               path,
		cookiesPath:        cookiesPath,
		artlistCookiesPath: cfg.External.ArtlistCookiesPath,
		cmdBuilder:         ytdlp.NewCommandBuilder(cfg),
		verifier:           &ytdlp.OutputVerifier{},
		runner:             defaultRunner{},
	}
}

// Path returns the configured yt-dlp binary path.
func (d *YTDLPDownloader) Path() string {
	return d.path
}

// DownloadRequest configures a download operation.
type DownloadRequest struct {
	URL              string
	OutputPath       string
	Format           string // e.g. "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/best"
	MergeFormat      string // e.g. "mp4"
	NoPlaylist       bool
	DownloadSections []string // e.g. ["*00:01:20-00:01:35"]
	ForceKeyframes   bool
	StreamCopy       bool // If true, force stream copy (fast but less precise)
	Timeout          time.Duration
	// UseCookies enables passing YouTube cookies to yt-dlp.
	// Cookies are needed for age-restricted or auth-required videos,
	// but they disable the android client (which doesn't support cookies),
	// falling back to web-only extraction that may fail n-challenge solving.
	// Leave false for public/unrestricted videos to keep android+web clients active.
	UseCookies bool
}

// DownloadedSegment represents a successfully downloaded segment.
type DownloadedSegment struct {
	Path  string
	Name  string
	Index int
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

	args := []string{}
	if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
		args = append(args, "--no-playlist")
	}

	// Blocco 5 (July 2026): BaseArgs centralizes cookies, JS runtime,
	// --no-warnings, and --extractor-args. Format selection is via
	// FormatArg so the downloader doesn't inline the -f string.
	args = append(args, d.cmdBuilder.BaseArgs(req.URL, req.UseCookies)...)
	args = append(args, d.cmdBuilder.FormatArg(true)...)

	// Dynamically use aria2c to accelerate downloads if available
	args = d.addExternalDownloaderArgs(args)

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

	args = append(args, req.URL)

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	_, err := d.run(ctx, args, process.Options{
		Timeout:        timeout,
		CombinedOutput: true,
	})
	if err != nil {
		return err
	}

	// Blocco 5 (July 2026): verify the output file exists and is
	// non-empty after a successful yt-dlp exit (yt-dlp can exit zero
	// but leave no output).
	resolvedPath, resolveErr := ResolveDownloadedSegmentPath(outputTemplate)
	if resolveErr != nil {
		return fmt.Errorf("download succeeded but output file not found: %w", resolveErr)
	}
	if verifyErr := d.verifier.VerifyFile(resolvedPath); verifyErr != nil {
		return verifyErr
	}
	return nil
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

	args := []string{}
	if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
		args = append(args, "--no-playlist")
	}
	args = append(args, d.cmdBuilder.BaseArgs(req.URL, req.UseCookies)...)
	args = append(args, d.cmdBuilder.FormatArg(true)...)
	args = d.addExternalDownloaderArgs(args)

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
	args = append(args, req.URL)

	_, err := d.run(ctx, args, process.Options{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to download range: %w", err)
	}

	path, pathErr := ResolveDownloadedSegmentPath(outputTemplate)
	if pathErr != nil {
		return "", fmt.Errorf("download range succeeded but output file not found: %w", pathErr)
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
		args := []string{}
		if req.NoPlaylist || shouldForceNoPlaylist(req.URL) {
			args = append(args, "--no-playlist")
		}
		args = append(args, d.cmdBuilder.BaseArgs(req.URL, req.UseCookies)...)
		args = append(args, d.cmdBuilder.FormatArg(true)...)
		args = d.addExternalDownloaderArgs(args)

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
		args = append(args, req.URL)

		_, err := d.run(ctx, args, process.Options{
			Timeout:        10 * time.Minute,
			CombinedOutput: true,
		})
		if err != nil {
			return results, fmt.Errorf("failed to download section %d: %w", i, err)
		}

		resolvedPath, pathErr := ResolveDownloadedSegmentPath(outputTemplate)
		if pathErr != nil {
			return results, fmt.Errorf("section %d: %w", i, pathErr)
		}

		results = append(results, DownloadedSegment{
			Path:  resolvedPath,
			Name:  fmt.Sprintf("segment_%03d", i+1),
			Index: i,
		})
	}

	return results, nil
}
