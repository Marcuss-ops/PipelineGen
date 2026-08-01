// Package downloader provides YouTube/social media download capabilities via yt-dlp.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor, mediapipeline, and artlist service.
//
// File layout (split by responsibility, July 2026):
//
//	downloader.go          core: struct, ProcessRunner port, constructor, request types
//	downloader_ytdlp.go    yt-dlp execution: Download / DownloadRange / DownloadSections
//	downloader_staging.go  staging + local files: section normalization, path resolution, file:// copies
//	downloader_helpers.go  metadata + channel listing helpers
package downloader

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
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
	cookiesPath := cfg.External.ResolveYouTubeCookiesPath()
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
