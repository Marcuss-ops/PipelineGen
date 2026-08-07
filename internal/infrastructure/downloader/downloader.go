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
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strings"
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
	// ytMinSleepSeconds / ytMaxSleepSeconds pace YouTube downloads
	// (August 2026 rate-limit recovery). 0 = disabled. Clamped by
	// NewYTDLP via cfg.External.ResolvedYouTubeSleepSeconds.
	ytMinSleepSeconds int
	ytMaxSleepSeconds int
	// runner is the Pattern 0 port for executing external processes
	// (godlike/07 minimum-blast-radius + testability). The production
	// default is `defaultRunner{}` which wraps process.Run; tests inject
	// a `captureRunner` mock to capture argv without spawning yt-dlp.
	// See downloader_test.go for the canonical captureRunner pattern.
	runner ProcessRunner
	// transportSleep is a test seam for bounded transport retry backoff. The
	// production value is nil, which uses the context-aware real sleep.
	transportSleep func(context.Context, time.Duration) error
}

func (d *YTDLPDownloader) run(ctx context.Context, args []string, opts process.Options) (*process.Result, error) {
	return d.runner.Run(ctx, d.path, args, opts)
}

// isYouTubeURL reports whether the URL targets YouTube (the only host where
// the player-client fallback and sleep pacing apply).
func isYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
}

// youtubeClientRetryableRe matches errors where another YouTube player client
// can produce a different set of formats or bypass a client-specific gate.
// Keep this classifier scoped to YouTube in isYouTubeClientRetryableError;
// generic HTTP errors and invalid URLs must not trigger a fallback ladder.
var youtubeClientRetryableRe = regexp.MustCompile(`(?i)` +
	`sign\s+in\s+to\s+confirm|not\s+a\s+bot|` +
	`requested\s+format\s+is\s+not\s+available|no\s+video\s+formats\s+found|` +
	`(?:challenge|player)\s+(?:extraction|response)|` +
	`(?:googlevideo|youtube).*(?:403|forbidden)|(?:403|forbidden).*(?:googlevideo|youtube)`)

// isYouTubeClientRetryableError reports whether a failed YouTube invocation
// should try the next player client. process.Run embeds combined yt-dlp
// output in the error, so classification is based on the wrapped message.
func isYouTubeClientRetryableError(url string, err error) bool {
	return err != nil && isYouTubeURL(url) && youtubeClientRetryableRe.MatchString(err.Error())
}

// ytdlpClients returns the ordered attempt list: the canonical primary
// client first, then each configured fallback (deduplicated). At minimum it
// always contains the primary client so a misconfigured builder degrades to
// the pre-fallback behaviour (single attempt).
func (d *YTDLPDownloader) ytdlpClients() []string {
	var clients []string
	seen := make(map[string]bool)
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		clients = append(clients, c)
	}
	if d.cmdBuilder != nil {
		add(d.cmdBuilder.PrimaryYouTubePlayerClient())
		for _, c := range d.cmdBuilder.FallbackYouTubePlayerClients() {
			add(c)
		}
	}
	if len(clients) == 0 {
		return []string{"android_creator"}
	}
	return clients
}

// youtubeSleepIntervalArgs returns the yt-dlp random-sleep flags for YouTube
// downloads when pacing is enabled (min > 0). Max is clamped to at least min.
func (d *YTDLPDownloader) youtubeSleepIntervalArgs(url string) []string {
	if !isYouTubeURL(url) || d.ytMinSleepSeconds <= 0 {
		return nil
	}
	max := d.ytMaxSleepSeconds
	if max < d.ytMinSleepSeconds {
		max = d.ytMinSleepSeconds
	}
	return []string{
		"--min-sleep-interval", fmt.Sprintf("%d", d.ytMinSleepSeconds),
		"--max-sleep-interval", fmt.Sprintf("%d", max),
	}
}

// sleepBetweenAttempts sleeps a random duration in the configured
// [min,max] window before a fallback retry, so retries after a client error
// don't hammer a hot IP back-to-back. No-op when pacing is disabled.
func (d *YTDLPDownloader) sleepBetweenAttempts() {
	if d.ytMinSleepSeconds <= 0 {
		return
	}
	max := d.ytMaxSleepSeconds
	if max < d.ytMinSleepSeconds {
		max = d.ytMinSleepSeconds
	}
	span := max - d.ytMinSleepSeconds + 1
	delay := d.ytMinSleepSeconds + rand.Intn(span)
	time.Sleep(time.Duration(delay) * time.Second)
}

// runWithClientFallback executes a yt-dlp command through buildArgs, which
// is called once per player-client attempt. The canonical client runs first;
// after a retryable YouTube client error each configured fallback client is
// tried with a random pacing delay in between. Non-retryable errors abort
// immediately (a different player client cannot fix a 404 or malformed request).
// The last result+error is returned once the client list is exhausted.
func (d *YTDLPDownloader) runWithClientFallback(ctx context.Context, url string, opts process.Options, buildArgs func(playerClient string) []string) (*process.Result, error) {
	clients := d.ytdlpClients()
	var lastResult *process.Result
	var lastErr error
	for i, client := range clients {
		if i > 0 {
			d.sleepBetweenAttempts()
		}
		result, err := d.run(ctx, buildArgs(client), opts)
		if err == nil {
			return result, nil
		}
		lastResult, lastErr = result, err
		if !isYouTubeClientRetryableError(url, err) || i == len(clients)-1 {
			return result, err
		}
		log.Printf("downloader: retryable YouTube client error with player client %q, retrying with %q (attempt %d/%d)", client, clients[i+1], i+1, len(clients))
	}
	return lastResult, lastErr
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
	minSleep, maxSleep := cfg.External.ResolvedYouTubeSleepSeconds()
	return &YTDLPDownloader{
		path:               path,
		cookiesPath:        cookiesPath,
		artlistCookiesPath: cfg.External.ArtlistCookiesPath,
		cmdBuilder:         ytdlp.NewCommandBuilder(cfg),
		verifier:           &ytdlp.OutputVerifier{},
		ytMinSleepSeconds:  minSleep,
		ytMaxSleepSeconds:  maxSleep,
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
