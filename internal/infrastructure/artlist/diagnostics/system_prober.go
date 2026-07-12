// Package diagnostics — system_prober.go (Phase 2 / Fase 2, July 2026).
//
// AdminSystemProber is the canonical godlike/06 SSOT concrete for the
// artlist.SystemProber port declared in
// internal/application/assets/providers/artlist/diagnostics.go.
//
// godlike/07 NO-FAKE-AVAILABILITY §22 contract: every probe is a real
// reachability check (HTTP /health, exec.LookPath, db.PingContext +
// write-readiness, etc). Object-existence pointer checks (e.g.
// `if s.scraper != nil`) are FORBIDDEN — the diagnostic endpoint must
// NEVER report a passing probe without actually exercising the
// dependency. The benchmark is: "could a probe-read show a real Error
// string with the verbatim underlying failure if the dep is genuinely
// broken?"
//
// Commit progression (Phase 2 / Fase 2 wire-by-wire rewrite):
//   - Commit 1 (THIS FILE): 4 upstream probes (scraper / browser /
//     session / downloader) implemented as real HTTP reachability
//     checks. The remaining 6 probes (ffmpeg_binary, drive_folder,
//     sqlite_writable, outbox_dispatcher, qdrant_reachable,
//     embedding_provider) return honest stub-failure ProbeResults
//     with Error="probe_queued_for_subsequent_commit" markers.
//   - Commit 2: 2 capability probes (ffmpeg_binary + drive_folder)
//     replaced with real implementations.
//   - Commit 3: 4 infra probes (sqlite_writable + outbox_dispatcher
//   - qdrant_reachable + embedding_provider) replaced with real
//     implementations.
//   - Commit 4: LatestRun + LastError + CountBySource('artlist')
//     wired via DiagnosticsService.Diagnostics
//     (RunRepository.LatestRun + ClipsRepository.CountBySource).
//   - Commit 5: per-probe wire-by-wire tests + no-aggregated-OK invariant.
//
// Per-probe execution is sequential (no goroutine pool). godlike/07
// prefers sequential for audit simplicity: trace order matches probe
// order in the JSON output, and a single slow probe cannot starve the
// others while it waits on a worker pool slot.
package diagnostics

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
)

// DefaultProbeTimeout is the per-probe HTTP/wall-clock budget. 5s
// matches the precedent set by HTTPSelfLoopProbe (default 5s per
// http_live_probe.go::DefaultProbeTimeout) and by
// internal/infrastructure/artlist/health/probe.go::DefaultProbeTimeout
// (60s cadence scraper health probe at 5s per attempt).
const DefaultProbeTimeout = 5 * time.Second

// FFmpegVersionProbeTimeout is the per-probe FFmpeg `-version`
// invocation budget. 2s — ffmpeg -version is a fast startup probe
// (no media processing, just version banner emission); 5s would be
// overkill and would risk hanging the entire diagnostics endpoint
// pile-up if FFmpeg is installed but misconfigured.
const FFmpegVersionProbeTimeout = 2 * time.Second

// Renderer is the minimal interface AdminSystemProber needs from the
// asset.Processor cross-cutting dependency to probe FFmpeg binary
// presence/version. The concrete is ffmpeg.NewProcessor; we declare a
// narrow 1-method port here to keep the SystemProber decoupled from
// the cross-cutting asset.Processor surface (godlike/06 Pattern 0:
// smallest-port-possible). Wired in Commit 2 (ffmpeg_binary probe).
type Renderer interface {
	// FFmpegBinaryPath returns the resolved path to the ffmpeg binary,
	// or "" when no ffmpeg is available on the host.
	FFmpegBinaryPath() string
}

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

	// Renderer (asset.Processor) — used by ffmpeg_binary probe in
	// Commit 2. Nil-safe (Renderer.FFmpegBinaryPath is the only method
	// called; comment in Commit 2 implementation).
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
	// cutter_test.go line 60 which use exec.LookPath directly on
	// the bare "ffmpeg" / "ffprobe" names).
	FFmpegBinaryPath string
}

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
	scraperCtx, scraperCancel := withBudget()
	scraper := p.httpGetProbe(scraperCtx, client, p.ScraperURL, "scraper")
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

// httpGetProbe performs an HTTP GET against the configured URL with
// the supplied client + per-probe ctx. Returns ProbeResult on 2xx,
// explicitly failed ProbeResult on 4xx/5xx, transport error on
// DNS/TCP/timeout/connection-refused.
//
// probeLabel is included in Error and Detail messages so operators
// reading the JSON output can pinpoint the failing probe URL without
// grepping the field names (e.g. "scraper: HTTP 503" rather than just
// "HTTP 503").
//
// Does NOT cache across calls (godlike/07 audit-pinning; the caller
// can wrap with an LRU if they want periodic sampling).
func (p *AdminSystemProber) httpGetProbe(ctx context.Context, client *http.Client, url, probeLabel string) artlist.ProbeResult {
	start := time.Now()
	if url == "" {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + "_url_not_configured",
			Detail:    "operator did not configure the " + probeLabel + " endpoint URL (cfg field empty)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	if client == nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + "_http_client_nil",
			Detail:    "composition root forgot to inject http.Client (godlike/07 fail-closed)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + ": NewRequestWithContext: " + err.Error(),
			Detail:    "url=" + url + ", elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport-level failure (DNS/TCP/timeout/connection-refused)
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + ": client.Do " + url + ": " + err.Error(),
			Detail:    "transport failure, elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return artlist.ProbeResult{
			OK:        true,
			Detail:    "url=" + url + ", status=" + http.StatusText(resp.StatusCode) + ", elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	// 4xx/5xx — server reachable but unhealthy. Honest fail with
	// status code embedded for operator triage.
	return artlist.ProbeResult{
		OK:        false,
		Error:     probeLabel + ": non-2xx status " + http.StatusText(resp.StatusCode),
		Detail:    "url=" + url + ", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}

// stubFailSet returns the canonical "every probe failed" set used by
// the fallback stubSystemProber in the application layer (in
// NewDiagnosticsService). Mirrors ProbeAll's stub shape verbatim so
// the wire-shape contract is identical when AdminSystemProber is nil.
//
// Reason is forwarded into Detail so operators can grep for
// "stub_prober_activated" or "nil_receiver" etc.
func stubFailSet(reason string) artlist.ProbeSet {
	f := func(label string) artlist.ProbeResult {
		return artlist.ProbeResult{
			OK:        false,
			Error:     label + ": stub",
			Detail:    "stubSystemProber: " + reason + " (composition root forgot to wire SystemProber; godlike/07 fail-closed)",
			ElapsedMs: 0,
		}
	}
	return artlist.ProbeSet{
		Scraper:           f("scraper"),
		Browser:           f("browser"),
		Session:           f("session"),
		Downloader:        f("downloader"),
		FFmpegBinary:      f("ffmpeg_binary"),
		DriveFolder:       f("drive_folder"),
		SQLiteWritable:    f("sqlite_writable"),
		OutboxDispatcher:  f("outbox_dispatcher"),
		QdrantReachable:   f("qdrant_reachable"),
		EmbeddingProvider: f("embedding_provider"),
	}
}

// Compile-time pin (Pattern 0 + AGENTS.md §Pattern 4 + godlike/06
// SSOT). Drift in the SystemProber port signature surfaces here as a
// build failure rather than as a runtime panic on first dispatch.
var _ artlist.SystemProber = (*AdminSystemProber)(nil)

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

// probeFFmpeg returns the canonical ffmpeg_binary probe result.
// Two-step reachability check: (a) exec.LookPath("ffmpeg") honours
// $PATH when FFmpegBinaryPath is empty; (b) `ffmpeg -version` is
// invoked with a 2s timeout to verify the binary actually runs and
// emits the canonical `ffmpeg version X.Y.Z` banner.
//
// godlike/07 NO-FAKE-AVAILABILITY: the probe verifies both PATH
// presence AND runtime execution — distinguishable from a simple
// `which ffmpeg` shell out that would silently succeed on a stale
// binary that fails to launch.
//
// Errors are surfaced faithfully (string concatenation with the
// underlying exec.LookPath / Run error verbatim) so operators can
// grep for the canonical failure patterns (e.g.
// "exec.LookPath(\"ffmpeg\") failed: exec: \"ffmpeg\": executable
// file not found in $PATH").
func (p *AdminSystemProber) probeFFmpeg(ctx context.Context) artlist.ProbeResult {
	start := time.Now()

	ffmpegPath := strings.TrimSpace(p.FFmpegBinaryPath)
	if ffmpegPath == "" {
		// Fall back to exec.LookPath("ffmpeg") to honour $PATH —
		// matches the precedent set by
		// internal/application/clips/upload/usecase.go line 361 +
		// cutter_test.go line 60 which use exec.LookPath directly
		// on the bare "ffmpeg" / "ffprobe" names.
		resolved, err := exec.LookPath("ffmpeg")
		if err != nil {
			return artlist.ProbeResult{
				OK:        false,
				Error:     "ffmpeg_binary_not_found",
				Detail:    "exec.LookPath(\"ffmpeg\") failed: " + err.Error() + " (operator must install ffmpeg on the host or set AdminSystemProber.FFmpegBinaryPath explicitly)",
				ElapsedMs: time.Since(start).Milliseconds(),
			}
		}
		ffmpegPath = resolved
	}

	// Resolve the runner (test fixtures swap a stub; production
	// wires DefaultRunner) and run ffmpeg -version with a 2s
	// per-probe timeout.
	runner := p.FFmpegRunner
	if runner == nil {
		runner = DefaultRunner{}
	}
	verCtx, verCancel := context.WithTimeout(ctx, FFmpegVersionProbeTimeout)
	defer verCancel()

	stdout, err := runner.Run(verCtx, ffmpegPath, []string{"-version"})
	if err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "ffmpeg_version_probe_failed",
			Detail:    "ffmpeg -version invocation failed: " + err.Error(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	// Parse the version banner. ffmpeg -version output starts with
	// `ffmpeg version X.Y.Z ...`. If the banner is missing the
	// `ffmpeg` substring the binary at this path is not actually
	// FFmpeg (operator mis-config, wrong path); surface honestly
	// rather than silently OK.
	banner := strings.TrimSpace(string(stdout))
	if !strings.Contains(banner, "ffmpeg") {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "ffmpeg_version_unparseable",
			Detail:    "ffmpeg -version output did not contain 'ffmpeg' header (binary at " + ffmpegPath + " is not FFmpeg?): " + banner,
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	firstLine := banner
	if idx := strings.Index(banner, "\n"); idx >= 0 {
		firstLine = banner[:idx]
	}
	return artlist.ProbeResult{
		OK:        true,
		Detail:    "ffmpeg binary=" + ffmpegPath + ", banner=\"" + firstLine + "\", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}

// ── Drive folder probe (Commit 2, July 2026: real implementation)───

// probeDriveFolder returns the canonical drive_folder probe result.
// Calls the ProbeFolderAccess closure injected from the composition
// root against the configured Artlist Drive root (resolve via
// artlist.ResolveRootFolderID(cfg)). nil-safe in three layers:
//
//   - ProbeFolderAccess == nil: composition-time wiring gap, surface
//     Error="drive_folder_probe_unwired" honestly rather than fake-OK.
//   - ProbeFolderRootID == "": operator did not configure the root
//     folder, surface Error="drive_folder_root_not_configured" with
//     the operator-fix hint (cfg.Drive.ArtlistFolder()).
//   - ProbeFolderAccess returns non-nil error: surface the verbatim
//     error message from the underlying probe call.
//
// Forward-pointer (post-Commit 2): the canonical
// delivery.Publisher interface (in
// internal/application/assets/delivery/publisher.go) does not expose
// ProbeFolderAccess today — a follow-up commit will lift it onto
// the canonical publisher port surface so the prober can call it
// without the composition root needing to construct an ad-hoc
// closure. Until then the probe remains honest with
// Error="drive_folder_probe_unwired".
func (p *AdminSystemProber) probeDriveFolder(ctx context.Context) artlist.ProbeResult {
	start := time.Now()

	if p.ProbeFolderAccess == nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_probe_unwired",
			Detail:    "composition root did not inject ProbeFolderAccess closure (canonical delivery.Publisher interface does not expose ProbeFolderAccess today; forward-pointer to a follow-up commit that lifts ProbeFolderAccess onto the canonical publisher port)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	rootID := strings.TrimSpace(p.ProbeFolderRootID)
	if rootID == "" {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_root_not_configured",
			Detail:    "composition root passed empty ProbeFolderRootID (cfg.Drive.ArtlistFolder() returned empty; operator must configure cfg.Drive.ArtlistRootFolder for the diagnostics endpoint to probe folder reachability)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	timeout := p.ProbeTimeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := p.ProbeFolderAccess(probeCtx, rootID); err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "drive_folder_unreachable",
			Detail:    "ProbeFolderAccess(" + rootID + ") returned: " + err.Error(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	return artlist.ProbeResult{
		OK:        true,
		Detail:    "drive folder probe ok: rootID=" + rootID + ", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
