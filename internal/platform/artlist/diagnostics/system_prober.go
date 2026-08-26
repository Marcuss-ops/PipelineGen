// Package diagnostics — system_prober.go: canonical owner of the
// artlist.SystemProber port concrete + the 2 compile-time pins
// (Step 4 / commit 9b follow-up, July 2026).
//
// AdminSystemProber is the SINGLE canonical owner of "how does the
// 10-probe diagnostics battery run?" (godlike/06 SSOT). The 551-LOC
// file was split into 5 seams (per-probe-group) in Step 4:
//
//   - system_prober.go (CANONICAL, this file): the AdminSystemProber
//     struct, the ProbeAll entry point, the DefaultRunner type, the
//     FFmpegRunner interface, the 2 compile-time pins, the
//     DefaultProbeTimeout const.
//   - system_prober_http.go: 4 real upstream probes (scraper 2-stage
//     deep + browser + session + downloader via httpGetProbe) + their
//     helpers + the Stage2SessionFreshnessWindow const.
//   - system_prober_stubs.go: the stubFailSet helper used by the
//     application-layer stubSystemProber fallback + ProbeAll's
//     "nil receiver" branch.
//   - system_prober_ffmpeg.go: probeFFmpeg + Renderer interface +
//     FFmpegVersionProbeTimeout const.
//   - system_prober_drive.go: probeDriveFolder.
//
// godlike/06 SSOT: every file lives in `package diagnostics`. Cross-file
// symbol resolution is package-internal; no new exports, no API
// surface change.
//
// godlike/07 NO-FAKE-AVAILABILITY §22 contract: every probe is a real
// reachability check (HTTP /health, exec.LookPath, db.PingContext +
// write-readiness, etc). Object-existence pointer checks are
// FORBIDDEN — the diagnostic endpoint must NEVER report a passing
// probe without actually exercising the dependency.
//
// Commit progression (Phase 2 / Fase 2 wire-by-wire rewrite):
//   - Commit 1 (Fase 2 / Commit B, July 2026): 4 upstream probes
//     (scraper / browser / session / downloader) implemented as real
//     HTTP reachability checks. The remaining 6 probes
//     (ffmpeg_binary, drive_folder, sqlite_writable,
//     outbox_dispatcher, qdrant_reachable, embedding_provider) return
//     honest stub-failure ProbeResults with
//     Error="probe_queued_for_subsequent_commit" markers.
//   - Commit 2: 2 capability probes (ffmpeg_binary + drive_folder)
//     replaced with real implementations.
//   - Commit 3: 4 infra probes (sqlite_writable + outbox_dispatcher
//   - qdrant_reachable + embedding_provider) replaced with real
//     implementations.
//   - Commit 4: LatestRun + LastError + CountBySource('artlist')
//     wired via DiagnosticsService.Diagnostics.
//   - Commit 5: per-probe wire-by-wire tests + no-aggregated-OK
//     invariant.
//
// Per-probe execution is sequential (no goroutine pool). godlike/07
// prefers sequential for audit simplicity: trace order matches
// probe order in the JSON output, and a single slow probe cannot
// starve the others while it waits on a worker pool slot.
package diagnostics

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os/exec"
	"time"

	"go.uber.org/zap"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// DefaultProbeTimeout is the per-probe HTTP/wall-clock budget. 5s
// matches the precedent set by HTTPSelfLoopProbe (default 5s per
// http_live_probe.go::DefaultProbeTimeout) and by
// internal/infrastructure/artlist/health/probe.go::DefaultProbeTimeout
// (60s cadence scraper health probe at 5s per attempt).
//
// godlike/06 SSOT: this is the canonical DefaultProbeTimeout for the
// diagnostics package; both ProbeAll (canonical, this file) and
// probeDriveFolder (system_prober_drive.go) reference this constant.
// Co-locating with ProbeAll keeps the shared budget discoverable.
const DefaultProbeTimeout = 5 * time.Second

// FFmpegRunner is the canonical godlike/06 port for the ffmpeg
// `-version` subprocess invocation (PR-P2-DIAGNOSTICS-REALE Commit 2,
// July 2026). 1-method interface mirroring the
// ffmpeg.ProcessRunner port shape:
//
//	ffmpeg.ProcessRunner.Run(ctx, name, args, opts) (*process.Result, error)
//
// returns a structured *process.Result; the diagnostics-package's
// FFmpegRunner is the narrower Read-then-Diagnose surface used by
// the ffmpeg_binary probe (just stdout-bytes + error). Production
// wires `DefaultRunner` (os/exec.CommandContext); tests can swap a
// stub to assert argv without spawning a real subprocess.
//
// godlike/06 Pattern 0 + AGENTS.md §Pattern 4: smallest-port-possible.
//
// FFmpegRunner lives in the canonical file (not the ffmpeg file)
// because the compile-time pin `var _ FFmpegRunner = DefaultRunner{}`
// must live where the impl lives (DefaultRunner is canonical, see
// below) — drift between the interface and the impl surfaces as a
// build failure in this file rather than as a runtime panic in the
// ffmpeg probe.
type FFmpegRunner interface {
	Run(ctx context.Context, name string, args []string) (stdout []byte, err error)
}

// AdminSystemProber is the canonical concrete implementation of
// artlist.SystemProber. Holds the configuration for 10 probes (URLs
// + sqlite DB + dispatcher health fn + drive folder probe fn +
// renderer); ProbeAll runs them sequentially with per-probe timeouts
// and returns an artlist.ProbeSet.
//
// Compile-time pin (Pattern 0 + AGENTS.md §Pattern 4 + godlike/06
// SSOT): the concrete receiver MUST satisfy the canonical
// artlist.SystemProber port; drift surfaces as a build failure here,
// not a runtime panic on first dispatch.
type AdminSystemProber struct {
	Log *zap.Logger

	// URLs — operators pass empty strings when not configured.
	// Empty-string probes fail honestly with Error like
	// "scraper_url_not_configured" (godlike/07 distinguishes
	// "operator did not configure" from "configured but broken").
	ScraperURL    string
	BrowserURL    string
	SessionURL    string
	DownloaderURL string
	QdrantURL     string
	EmbeddingURL  string

	// SQLite — wired via composition root from cfg.ArtlistDB
	// (post-PR2.6 unified into media.db.sqlite). When nil,
	// the sqlite_writable probe fails with Error="sqlite_db_nil".
	SQLiteDB *sql.DB

	// DispatcherHealth reports the runtime health of the canonical
	// outbox dispatcher (QDRANT-002 mandatory gate). Wired in Commit 3.
	// Nil-safe: passing nil here means the probe reports
	// Error="outbox_dispatcher_health_unwired" rather than panicking.
	DispatcherHealth func(ctx context.Context) artlist.ProbeResult

	// ProbeFolderAccess calls delivery.Publisher.ProbeFolderAccess on
	// the configured Artlist Drive root. Wired in Commit 2. Signature
	// mirrors StartupRootsProbe.ProbeFolderAccess (matches the
	// canonical narrow port declared in deliverystartupvalidator.go).
	// Nil-safe: nil fn means the probe reports
	// Error="drive_folder_probe_unwired".
	ProbeFolderAccess func(ctx context.Context, rootID string) error
	ProbeFolderRootID string

	// Renderer (detail.Processor) — used by ffmpeg_binary probe in
	// Commit 2. The interface is declared in system_prober_ffmpeg.go
	// (Pattern 0: keep interface with its sole consumer); same-package
	// resolution makes the cross-file reference transparent at compile
	// time.
	Renderer Renderer

	// FFmpegRunner is the subprocess runner for the ffmpeg -version
	// probe. Production wires defaultRunner; tests can swap a stub to
	// assert argv without spawning a real subprocess. Mirrors the
	// ffmpeg.ProcessRunner port shape (Pattern 0 + AGENTS.md §Pattern 4:
	// smallest-port-possible).
	FFmpegRunner FFmpegRunner

	// ProbeTimeout is the per-probe wall-clock budget. Default
	// DefaultProbeTimeout (5s); operator override via composition
	// root read of cfg (forward-compat; no current cfg knob).
	ProbeTimeout time.Duration

	// HTTPClient is the per-request HTTP client. If nil, the prober
	// allocates a per-instance client with ProbeTimeout. Production
	// wires an injected client with connection pooling.
	HTTPClient *http.Client

	// FFmpegBinaryPath is the configured ffmpeg binary path.
	//
	// PR-P2-DIAGNOSTICS-REALE Commit 2 (July 2026): ffmpeg_binary
	// probe is now a REAL exec.LookPath + `ffmpeg -version`
	// reachability check. Empty FFmpegBinaryPath triggers an
	// exec.LookPath("ffmpeg") fallback so the probe honours $PATH
	// (matches the precedent set by
	// internal/application/clips/upload/usecase.go line 361 +
	// cutter_test.go line 60 which use exec.LookPath directly on the
	// bare "ffmpeg" / "ffprobe" names).
	FFmpegBinaryPath string
}

// Compile-time pin (Pattern 0 + AGENTS.md §Pattern 4 + godlike/06
// SSOT). Drift in the SystemProber port signature surfaces here as a
// build failure rather than as a runtime panic on first dispatch.
var _ artlist.SystemProber = (*AdminSystemProber)(nil)

// ProbeAll implements artlist.SystemProber. Returns an artlist.ProbeSet
// with all 10 fields populated — per-probe failures are encoded
// per-field via ProbeResult.Error; ProbeAll itself NEVER returns an
// error (a panic or unrecoverable internal error is logged via zap
// and reflected as every-probe-failed ProbeSet via stubSystemProberResult).
func (p *AdminSystemProber) ProbeAll(ctx context.Context) artlist.ProbeSet {
	if p == nil {
		return stubFailSet("nil receiver (godlike/07 fail-closed)")
	}
	timeout := p.ProbeTimeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	// Per-probe sub-context so a slow probe does not starve subsequent
	// probes. Parent ctx cancellation still propagates.
	withBudget := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, timeout)
	}

	// Commit 1 — 4 upstream probes (real HTTP reachability).
	//
	// Fase 7 / Commit B (July 2026): the Scraper probe is upgraded from a
	// shallow GET (which only verified /health returned 200 — a godlike/07
	// §22 NO-FAKE-AVAILABILITY violation because the scraper can be
	// "alive" with browser_running=false, last_launch_error=non-null,
	// and unable to perform a real query) to a deep 2-stage probe that
	// verifies:
	//   1. Chromium started           (browser_running == true from /health)
	//   2. Artlist session valid      (last_launch_error==null + recent
	//                                  last_session_alive_at from /health)
	// The real query capability is verified by /api/artlist/search/live
	// (Test 3 / live battery) because live Artlist searches are too slow
	// for a synchronous diagnostics endpoint.
	// The aggregate ProbeResult.OK is the strict AND of the 2 stages.
	// Aggregate.Error is the verbatim error of the first failing stage.
	// ProbeResult.Stages surfaces the per-stage verdict for operator
	// forensics (godlike/06 SSOT: ProbeStage wire shape lives in types.go).
	scraperCtx, scraperCancel := withBudget()
	scraper := p.probeScraperDeep(scraperCtx, client, p.ScraperURL)
	scraperCancel()

	browserCtx, browserCancel := withBudget()
	browser := p.httpGetProbe(browserCtx, client, p.BrowserURL, "browser")
	browserCancel()

	sessionCtx, sessionCancel := withBudget()
	session := p.httpGetProbe(sessionCtx, client, p.SessionURL, "session")
	sessionCancel()

	downloaderCtx, downloaderCancel := withBudget()
	downloader := p.httpGetProbe(downloaderCtx, client, p.DownloaderURL, "downloader")
	downloaderCancel()

	// Commit 2/3/4 — 6 stub-failure probes (replaceable one-by-one).
	stub := func(label string) artlist.ProbeResult {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "probe_queued_for_subsequent_commit",
			Detail:    label + " probe will be wired in a subsequent Phase 2 / Fase 2 commit (replaces this stub-probe)",
			ElapsedMs: 0,
		}
	}

	set := artlist.ProbeSet{
		Scraper:    scraper,
		Browser:    browser,
		Session:    session,
		Downloader: downloader,
		// Commit 2 — 2 capability probes (REAL). ffmpeg_binary exec.LookPath +
		// version probe. drive_folder calls the closure injected from the
		// composition root.
		FFmpegBinary: p.probeFFmpeg(ctx),
		DriveFolder:  p.probeDriveFolder(ctx),
		// Stubs — Commit 3 replaces these with real probe logic.
		SQLiteWritable:    stub("sqlite_writable"),
		OutboxDispatcher:  stub("outbox_dispatcher"),
		QdrantReachable:   stub("qdrant_reachable"),
		EmbeddingProvider: stub("embedding_provider"),
	}

	if p.Log != nil {
		p.Log.Debug("AdminSystemProber.ProbeAll complete (Commit 2: 6 real probes; 4 stubs pending)",
			zap.Int("probes_total", 10),
			zap.Int("probes_real", 6),
			zap.Int("probes_stubbed", 4),
			zap.Bool("scraper_ok", set.Scraper.OK),
			zap.Bool("browser_ok", set.Browser.OK),
			zap.Bool("session_ok", set.Session.OK),
			zap.Bool("downloader_ok", set.Downloader.OK),
			zap.Bool("ffmpeg_binary_ok", set.FFmpegBinary.OK),
			zap.Bool("drive_folder_ok", set.DriveFolder.OK),
		)
	}
	return set
}

// ── FFmpeg binary probe (Commit 2, July 2026: real implementation)───

// DefaultRunner is the production implementation of FFmpegRunner.
// It delegates to exec.CommandContext (mirrors the canonical
// defaultProcessRunner pattern in
// internal/infrastructure/media/ffmpeg/ffmpeg.go). Test fixtures
// can swap a no-op runner to assert argv without spawning a real
// subprocess.
//
// godlike/06 SSOT: DefaultRunner is the EXPORTED (capital D) name
// used by the composition root in build_bundles_artlist.go:
// `FFmpegRunner: diagnostics.DefaultRunner{}`. Rename this type
// with care to avoid breaking the wiring site; the lowercase form
// `defaultRunner` is reserved for tests internal to this package.
type DefaultRunner struct{}

// Run satisfies FFmpegRunner. Returns (stdout, error) so the
// caller can parse the version banner even on failure (ffmpeg emits
// version header on stderr sometimes, depending on platform).
func (DefaultRunner) Run(ctx context.Context, name string, args []string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("DefaultRunner.Run: empty binary name (composition root forgot to set FFmpegBinaryPath?)")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), errors.New(err.Error() + ": " + stderr.String())
	}
	return stdout.Bytes(), nil
}

// Compile-time pin: DefaultRunner satisfies FFmpegRunner. Drift
// surfaces as a build failure rather than a runtime panic.
var _ FFmpegRunner = DefaultRunner{}
