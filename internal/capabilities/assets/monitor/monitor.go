// Package monitor — monitor.go: ChannelMonitor struct + constructor.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - The ChannelMonitor struct (flat-field shape).
//   - The NewChannelMonitor(CompositionDeps) ctor.
//   - channelSemWidth (semaphore-width fallback chain).
//   - Start (scheduler loop entry) → scheduler_loop.go.
//   - checkDueChannels + safeCheckChannel → channel_runner.go.
//   - startOutboxDrainer + drainOutboxOnce + dispatchOutboxEntry → outbox_drainer.go.
//   - validateChannelConfig + validateJSONArray → channel_validation.go.
//   - recordCheckOutcome + nextCheckTime → check_outcome.go.
//
// The scheduler never touches os/exec, OllamaClient, or VTT regex
// directly — those concerns cross the package boundary through:
//   - MonitorDownloaderPort (ListChannel) inside checkDueChannels → checkChannel.
//   - channels.Service (ClaimDue / MarkChecked) for the scheduler state machine.
//
// The TranscriptProvider / VideoAnalyzer / JobEnqueuer ports are held as
// flat fields on the struct but consumed elsewhere (analyzer.go + enqueue.go).
package monitor

import (
	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ChannelMonitor handles periodic YouTube channel monitoring.
//
// God-object split (July 2026):
//   - The 3 AI/VTT ports (Transcript, Analyzer, Enqueuer) are flat fields.
//     Tests inject stubs directly via struct-literal construction.
//   - The lifecycle / scheduler loop lives in scheduler_loop.go.
//   - The per-cycle worker (checkChannel) lives in discovery.go.
//   - The per-video AI gate lives in analyzer.go.
//   - The durable-job emission lives in enqueue.go.
type ChannelMonitor struct {
	// Domain dependencies (kept as flat fields to preserve the existing
	// `&ChannelMonitor{channelsSvc, log, ytdlp}` test pattern; the new
	// ctor copies fields from CompositionDeps).
	cfg         *config.Config
	channelsSvc *channels.Service // canonical channel source authority
	log         *zap.Logger

	// Port surfaces (Pattern 0). The 3 new ports (Transcript / Analyzer /
	// Enqueuer) are unbound stubs at this commit; the next commit installs
	// concrete adapters. The legacy ytdlp port is satisfied by
	// *downloader.YTDLPDownloader in production (compile-time assertion in
	// ports.go) and by *fakeLister in tests (also a compile-time assertion
	// in monitor_scheduler_test.go).
	ytdlp      MonitorDownloaderPort
	transcript TranscriptProvider
	analyzer   VideoAnalyzer
	enqueuer   JobEnqueuer

	// discoveries (Commit D, June 2026) is the typed port over the
	// youtube_discoveries ledger (table created in
	// migrations/sqlite/113_youtube_discoveries.sql). Composition wires
	// the concrete *youtubediscoveries.YoutubeDiscoveriesRepository (declared in
	// internal/platform/sqlite/assets/youtube_discoveries_repository.go)
	// via the CompositionDeps.Discoveries field. Nil-tolerant at runtime:
	// processVideo's recordDiscoveryAndClassify classifies already_scheduled
	// defensively when m.discoveries is nil so a missing wire forces an
	// operator-visible misconfiguration rather than silently losing dedupe.
	discoveries YoutubeDiscoveriesPort

	// metrics (FASE 3.7 Commit 2, 2026-07-04) is the typed surface for
	// emitting the channel-monitor Prometheus counters/histograms.
	// Replaces the pre-Commit-2 direct `metrics.ChannelMonitor*` calls
	// in analyzer.go + discovery.go so the monitor package has zero
	// `internal/platform/observability` imports. Composition wires
	// the concrete *observability.ObservabilityMetricsRecorder via
	// CompositionDeps.MetricsRecorder; nil-tolerant at the ctor: a
	// missing wire installs NoopMetricsRecorder (matches the partial-
	// deploy / test-fixture safety pattern of the other nil-port
	// defaults in this ctor).
	metrics MetricsRecorder

	// policy is the per-instance MonitorRuntimePolicy (Commit A, P1 #10).
	// Nil falls back to DefaultMonitorRuntimePolicy via policyOrDefault().
	// Optional so existing tests that construct the struct by literal
	// continue to compile after the constants block moved to policy.go.
	policy *MonitorRuntimePolicy

	// Internal primitives.
	// globalSem is the rate-limiter semaphore per monitor: it bounds the
	// number of per-channel goroutines the scheduler can spin up at once.
	// Width comes from cfg.Concurrency.MaxConcurrentChannelChecks (fallback 1,
	// overridden by MonitorRuntimePolicy.MaxConcurrentChannels in Policy).
	globalSem chan struct{}
}

// NewChannelMonitor wires the monitor from the deps struct. Replaces the
// pre-Step-9 7-parameter signature that exposed concrete *ollama.Client +
// *downloader's pre-port internals.
//
// Per AGENTS.md / godlike/06 §"Database and config ownership": a typed
// port surface keeps the production wiring honest (channel monitor ⇄
// OllamaClient / yt-dlp / jobs.Service via interface boundaries) and
// unlocks test-doubles that fail precisely without spawning real
// subprocesses or hitting the real broker.
//
// Panic safety: a nil Log panics immediately (cannot run a service
// without a logger). The rest of the fields are zero-value-safe IF the
// caller knows which paths to avoid (analyzer/enqueuer nil → "unbound
// stub" failure mode returned by the unbound placeholder, but only if
// the caller actually wires the unbound stubs rather than nil itself).
func NewChannelMonitor(deps CompositionDeps) *ChannelMonitor {
	if deps.Log == nil {
		panic("monitor.NewChannelMonitor: Log is required")
	}

	// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): fail-fast posture
	// for the discoveries port. Per the verdict's P0 #1 directive,
	// production composition MUST supply a concrete YoutubeDiscoveries
	// adapter (the *youtubediscoveries.YoutubeDiscoveriesRepository over the canonical
	// media.db.sqlite). A missing wiring collapses every video's
	// outcome classification to OutcomeAlreadyScheduled in
	// discovery.go::recordDiscoveryAndClassify (the defensive nil-port
	// path). That defeats the per-video dedupe ledger AND the cycle-end
	// MAX(discovered_at) → category_channels.last_cursor watermark.
	//
	// Fail-fast proxy: `deps.Cfg != nil` distinguishes production
	// composition (cfg always injected by lifecycle.go) from the
	// test-fixture path (constructed by struct-literal with bare
	// CompositionDeps in scheduler_test.go, monitor_scheduler_test.go,
	// and similar fixtures). Panicking on production composition is
	// the right signal; tolerating nil in tests preserves the test
	// pattern that PR1 / PR2 / PR3 were built on.
	if deps.Cfg != nil && deps.Ports.Discoveries == nil {
		panic("monitor.NewChannelMonitor: Discoveries port is required when Cfg is wired (production composition must wire *youtubediscoveries.YoutubeDiscoveriesRepository from internal/platform/sqlite/assets/youtube_discoveries_repository.go; the nil-port pre-Commit-1 path defeats per-video dedupe AND cycle-end MAX watermark)")
	}

	// Apply default-unbound placeholder stubs if the caller left them nil.
	// This keeps production crash-fast at the FIRST analyzer/enqueuer call
	// rather than nil-deref panicking inside the worker.
	//
	// The Transcript stub was lifted in a follow-up commit (Wave B, June 2026):
	// `internal/app/lifecycle.go::startBackgroundJobs` now wires the concrete
	// `youtube.YTDLPSubtitleAdapter` (which satisfies the application
	// `transcripts.SubtitleSource` port) into CompositionDeps.Transcript,
	// so a nil deps.Transcript can only happen in partial-deploy / test-fixture
	// paths and the analyzer's `if m.transcript != nil` discipline catches it.

	// NOTE: Enqueuer may remain nil — dispatchOutboxEntry already
	// handles nil m.enqueuer gracefully (logs warning + marks outbox
	// entry as failed). The pre-Fase-8 nil-guard that created a
	// NewExtractionEnqueuer(nil, nil, ...) stub was removed when the
	// concrete ExtractionEnqueuer adapter + its constructor were
	// retired (commit 57212f1a). Production composition (lifecycle.go)
	// wires the concrete adapter; nil is safe for test fixtures and
	// partial-deploy paths.

	// FASE 3.7 Commit 2: MetricsRecorder nil-guard. A nil
	// deps.MetricsRecorder installs NoopMetricsRecorder so analyzer +
	// discovery call sites (m.metrics.IncVideosChecked etc.) never
	// panic on a missing wire. Production composition always
	// supplies *observability.ObservabilityMetricsRecorder; the no-op
	// default exists ONLY for test-fixture paths that construct the
	// monitor by bare CompositionDeps (without MetricsRecorder) —
	// preserving the existing "struct-literal + bare ctor"
	// patterns in monitor_scheduler_test.go / monitor_policy_test.go.
	recorder := deps.MetricsRecorder
	if recorder == nil {
		recorder = NoopMetricsRecorder{}
	}

	return &ChannelMonitor{
		cfg:         deps.Cfg,
		channelsSvc: deps.ChannelsSvc,
		log:         deps.Log,

		ytdlp:       deps.Ports.Ytdlp,
		transcript:  deps.Ports.Transcript,
		analyzer:    deps.Ports.Analyzer,
		enqueuer:    deps.Ports.Enqueuer,
		discoveries: deps.Ports.Discoveries,
		metrics:     recorder,
		policy:      deps.Policy,

		// globalSem width comes from the typed policy first (Commit A,
		// P1 #10), then cfg.Concurrency.MaxConcurrentChannelChecks
		// (back-compat with pre-Policy composition), then the default
		// of 1. channelSemWidth encodes the fallback chain.
		globalSem: make(chan struct{}, channelSemWidth(deps)),
	}
}

// channelSemWidth resolves the per-monitor per-channel semaphore width
// falling back in priority order: explicit Policy > cfg-level
// MaxConcurrentChannelChecks > default of 1. Extracted from
// NewChannelMonitor so tests can drive the fallback paths by passing
// nil/zero values without hitting an inline literal.
//
// Note on the missing `maxChannels` local var: the pre-Policy ctor
// declared `maxChannels := 1` and overrode it via cfg. That var was
// removed by the typed Policy extraction (Commit A); the
// channelSemWidth helper folds the same precedence into a typed
// return.
//
// Note on Stop() / stopCh: origin/main's Wave C partial drop replaced
// the explicit Stop()/stopCh side-channel with ctx-only shutdown (see
// package doc). Commit A intentionally does NOT reintroduce Stop()/
// stopCh — the monitor's lifecycle is owned by the parent ctx that
// serverLifecycle.Stop cancels. The startup plan Stop closure in
// internal/app/lifecycle.go::startBackgroundJobs already returns nil
// for this reason (no need to update there).
func channelSemWidth(deps CompositionDeps) int {
	if deps.Policy != nil && deps.Policy.MaxConcurrentChannels > 0 {
		return deps.Policy.MaxConcurrentChannels
	}
	if deps.Cfg != nil && deps.Cfg.Concurrency.MaxConcurrentChannelChecks > 0 {
		return deps.Cfg.Concurrency.MaxConcurrentChannelChecks
	}
	return 1
}

// Compile-time guard: ChannelMonitor still satisfies the same shape
// for backward compatibility (struct-literal construction in tests).
var _ = (*ChannelMonitor)(nil)
