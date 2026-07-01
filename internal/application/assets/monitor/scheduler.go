// Package monitor — scheduler.go: lease-aware scheduler loop + the
// ChannelMonitor struct itself.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//   - The ChannelMonitor struct (flat-field shape to keep the existing
//     test pattern `&ChannelMonitor{channelsSvc, log, ytdlp}` working
//     without forcing every test through the new CompositionDeps ctor).
//   - The NewChannelMonitor(CompositionDeps) ctor.
//   - The Start scheduler loop (claims due channels, fans out bounded
//     per-channel goroutines, persists outcomes via MarkChecked).
//   - The exponential backoff math (nextCheckTime 5min → 24h cap).
//   - parseCheckInterval (time.Duration parser; lives here because it's
//     a time-utility, no VTT / Ollama / exec coupling).
//   - Lifecycle: Start runs until the parent ctx cancels (no Stop()
//     method, no stopCh side-channel). Shutdown propagates through the
//     startCtx passed to Start — serverLifecycle.Stop orchestrates the
//     cancel, see internal/app/lifecycle.go::startBackgroundJobs.
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
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Backoff constants are now owned by MonitorRuntimePolicy
// (Commit A, P1 #10): the previous hardcoded `schedulerTick=30s +
// defaultLeaseDuration=30min + maxBackoff=24h + initialBackoff=5min`
// block lived here. They moved to policy.go so tests can drive the
// backoff curve and scheduler loop in O(seconds) without time.Sleep
// hacks. The constants are removed entirely; everything reads through
// m.policyOrDefault().Defaults match the previous literal values so
// the production behaviour is unchanged on this commit.

// ChannelMonitor handles periodic YouTube channel monitoring.
//
// Step 9 surface (June 2026):
//   - The 3 AI/VTT ports (Transcript, Analyzer, Enqueuer) are flat fields.
//     Tests inject stubs directly via struct-literal construction.
//   - The lifecycle / scheduler loop lives on this struct.
//   - The per-cycle worker (checkChannel) live in discovery.go.
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
	// the concrete *assets.YoutubeDiscoveriesRepository (declared in
	// internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go)
	// via the CompositionDeps.Discoveries field. Nil-tolerant at runtime:
	// processVideo's recordDiscoveryAndClassify classifies already_scheduled
	// defensively when m.discoveries is nil so a missing wire forces an
	// operator-visible misconfiguration rather than silently losing dedupe.
	discoveries YoutubeDiscoveriesPort

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
	// adapter (the *assets.YoutubeDiscoveriesRepository over the canonical
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
	if deps.Cfg != nil && deps.Discoveries == nil {
		panic("monitor.NewChannelMonitor: Discoveries port is required when Cfg is wired (production composition must wire *assets.YoutubeDiscoveriesRepository from internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go; the nil-port pre-Commit-1 path defeats per-video dedupe AND cycle-end MAX watermark)")
	}

	// Apply default-unbound placeholder stubs if the caller left them nil.
	// This keeps production crash-fast at the FIRST analyzer/enqueuer call
	// rather than nil-deref panicking inside the worker.
	//
	// The Transcript stub was lifted in a follow-up commit (Wave B, June 2026):
	// `internal/app/lifecycle.go::startBackgroundJobs` now wires a concrete
	// `transcripts.YTDLPSubtitleAdapter` directly into CompositionDeps.Transcript,
	// so a nil deps.Transcript can only happen in partial-deploy / test-fixture
	// paths and the analyzer's `if m.transcript != nil` discipline catches it.
	if deps.Analyzer == nil {
		deps.Analyzer = NewUnboundVideoAnalyzer()
	}
	if deps.Enqueuer == nil {
		deps.Enqueuer = NewUnboundJobEnqueuer()
	}

	return &ChannelMonitor{
		cfg:         deps.Cfg,
		channelsSvc: deps.ChannelsSvc,
		log:         deps.Log,

		ytdlp:       deps.Ytdlp,
		transcript:  deps.Transcript,
		analyzer:    deps.Analyzer,
		enqueuer:    deps.Enqueuer,
		discoveries: deps.Discoveries,
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

// Start begins the channel monitoring process.
//
// PR 5 (June 2026): job-based sync via ClaimDue/MarkChecked.
// PR 7 (June 2026): typed Channel DTO.
// Step 9 (June 2026): the previous "ListEnabled initial setup" shortcut
// is preserved → first tick goes through runSchedulerCycle → ClaimDue →
// lease → checkChannel → MarkChecked. The shortcut previously ran
// checks OUTSIDE that path (no worker_id, no lease, no
// consecutive_failures increment) which sabotaged the exponential
// backoff math in nextCheckTime.
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor (Step 9: 5-file split + ports)")

	if m.channelsSvc == nil {
		m.log.Error("Channel monitor: channels service not wired, cannot start")
		return
	}

	policy := m.policyOrDefault()
	m.log.Info("Channel monitor entering scheduling loop (first check via runSchedulerCycle)",
		zap.Duration("tick", policy.TickInterval),
		zap.Duration("lease", policy.LeaseDuration),
		zap.Int("claim_limit", policy.ClaimLimit))

	ticker := time.NewTicker(policy.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.runSchedulerCycle(ctx)
		case <-ctx.Done():
			m.log.Info("Channel monitor context cancelled")
			return
		}
	}
}

// runSchedulerCycle claims due channels and dispatches them to checkDueChannels.
// Reads TickInterval/LeaseDuration/ClaimLimit/WorkerIDPrefix from the policy.
func (m *ChannelMonitor) runSchedulerCycle(ctx context.Context) {
	policy := m.policyOrDefault()
	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(policy.LeaseDuration).Format(time.RFC3339)
	// workerID = WorkerIDPrefix + "-" + nanos-mod-100000. The prefix is
	// a knob (multi-tenant deployments may want a custom prefix to
	// disambiguate lease_owner rows across instances). The modulo
	// keeps the ID short enough to fit comfortably in DIAG
	// spreadsheets; raise the modulus when more workers are expected
	// (future PR).
	workerID := fmt.Sprintf("%s-%d", policy.WorkerIDPrefix, time.Now().UnixNano()%100000)

	result, err := m.channelsSvc.ClaimDue(ctx, channels.ClaimDueCommand{
		Now:        nowStr,
		WorkerID:   workerID,
		LeaseUntil: leaseUntil,
		Limit:      policy.ClaimLimit,
	})
	if err != nil {
		m.log.Error("Failed to claim due channels", zap.Error(err))
		return
	}

	if len(result.Channels) == 0 {
		return
	}

	m.log.Info("Claimed due channels for checking", zap.Int("count", len(result.Channels)))

	m.checkDueChannels(ctx, result.Channels)
}

// checkDueChannels spawns bounded goroutines (one per channel), each of
// which runs safeCheckChannel + records the outcome via recordCheckOutcome.
//
// Commit A (June 2026, P1 #9): the per-goroutine recover-and-log defer
// previously LOGGED the panic but did NOT call recordCheckOutcome. The
// lease was held until expiry (typically 30 min). The fix is to route
// every per-channel panic through safeCheckChannel, which converts the
// panic into a typed error that recordCheckOutcome always sees — so the
// channel ends up Success=false with a synthesized panic message and the
// exponential backoff can apply. The bound is policy.MaxConcurrentChannels
// from MonitorRuntimePolicy (governed by m.globalSem, the per-monitor
// rate-limiter semaphore). The per-channel ctx timeout is
// policy.PerChannelTimeout.
func (m *ChannelMonitor) checkDueChannels(ctx context.Context, chs []channels.Channel) {
	policy := m.policyOrDefault()
	for _, ch := range chs {
		ch := ch

		m.globalSem <- struct{}{}
		go func() {
			defer func() { <-m.globalSem }()

			checkCtx, cancel := context.WithTimeout(ctx, policy.PerChannelTimeout)
			defer cancel()

			result, checkErr := m.safeCheckChannel(checkCtx, ch)
			m.log.Info("channel check completed",
				zap.String("channel_id", ch.ID),
				zap.Bool("success", checkErr == nil),
				zap.Int("videos_discovered", result.VideosDiscovered),
				zap.Int("videos_enqueued", result.VideosEnqueued),
				zap.Int("videos_skipped", result.VideosSkipped),
				zap.Int("infra_failures", result.InfraFailures))

			if recErr := m.recordCheckOutcome(ctx, ch, checkErr); recErr != nil {
				m.log.Error("Failed to mark channel as checked",
					zap.String("channel_id", ch.ID),
					zap.Error(recErr))
			}
		}()
	}
}

// safeCheckChannel wraps checkChannel with panic-recovery. The
// previous in-goroutine `defer recover()` swallowed panics into a
// log line and let the lease sit idle until expiry; safeCheckChannel
// instead converts the panic into a regular Go error so the caller's
// recordCheckOutcome always fires and the backoff path is taken.
//
// Return shape is identical to checkChannel: (ChannelCheckResult,
// error). On panic the result is the zero ChannelCheckResult so a
// panic in processVideo doesn't pollute the per-channel counters.
func (m *ChannelMonitor) safeCheckChannel(ctx context.Context, ch channels.Channel) (result ChannelCheckResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("panic in channel check goroutine (safeCheckChannel)",
				zap.Any("recover", r),
				zap.String("channel_id", ch.ID))
			// Go's panic payload is `any`; %w requires an error.
			// Two cases: (1) panic was raised with an error value —
			// propagate it as-is via %w so callers can errors.Is.
			// (2) panic was raised with a non-error value (string,
			// int, struct) — fall back to %v rendering so the operator
			// still sees the payload in the wrapped error message.
			if e, ok := r.(error); ok {
				err = fmt.Errorf("channel check panicked for %s: %w", ch.ID, e)
			} else {
				err = fmt.Errorf("channel check panicked for %s: %v", ch.ID, r)
			}
		}
	}()
	return m.checkChannel(ctx, ch)
}

// recordCheckOutcome translates a checkChannel error into the
// MarkChecked success/failure contract and persists the outcome.
//
// Blocco 1 (channel-monitor hardening): extracted from checkDueChannels
// so the success=false backoff propagation path can be unit-tested
// without spinning up a real yt-dlp subprocess (use a fake
// MonitorDownloaderPort; see monitor_scheduler_test.go).
//
// On checkErr != nil:
//   - Success = false
//   - LastError = checkErr.Error()
//   - nextCheckTime follows the exponential backoff curve
//     (5min → 10min → 20min → … → 24h cap).
//
// On checkErr == nil:
//   - Success = true
//   - LastError = ""
//   - nextCheckTime = channel.CheckInterval ahead of now
//     (fallback to 24h on parse error).
//
// Uses the PARENT ctx (not checkCtx) by design: the MarkChecked write
// must persist even after the per-check 30-min deadline trips, so a
// long yt-dlp run that times out still records Success=false + the
// timeout error + backoff-driven NextCheckAt. Detaching from checkCtx
// is intentional; detaching from `ctx` would break the outcome write
// on workspace shutdown.
// recordCheckOutcome translates a checkChannel error into the
// MarkChecked success/failure contract and persists the outcome.
// Commit A (P1 #8): forwards ch.LeaseOwner as cmd.LeaseToken so the
// SQLite MarkChecked UPDATE is fenced on lease_owner (see
// channels_repository.go MarkChecked). Empty ch.LeaseOwner falls
// back to an un-fenced UPDATE — but the monitor always writes
// lease_owner=workerID via ClaimDue, so the fence is always active
// in production.
func (m *ChannelMonitor) recordCheckOutcome(ctx context.Context, ch channels.Channel, checkErr error) error {
	success := checkErr == nil
	lastErr := ""
	if checkErr != nil {
		lastErr = checkErr.Error()
	}
	nextCheckAt := m.nextCheckTime(ch, success)
	return m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
		ID:          ch.ID,
		LeaseToken:  ch.LeaseOwner,
		NextCheckAt: nextCheckAt,
		Success:     success,
		LastError:   lastErr,
	})
}

// nextCheckTime returns the RFC3339 RFC3339-format string for when
// the channel should be checked next, following the exponential
// backoff curve on failure. Commit A (P1 #10): reads backoff
// initial/cap from MonitorRuntimePolicy (was previously hardcoded
// to 5min / 24h in scheduler.go const block).
func (m *ChannelMonitor) nextCheckTime(ch channels.Channel, success bool) string {
	policy := m.policyOrDefault()
	if success {
		interval, err := parseCheckInterval(ch.CheckInterval)
		if err != nil {
			interval = 24 * time.Hour
		}
		return time.Now().Add(interval).Format(time.RFC3339)
	}

	failures := ch.ConsecutiveFailures + 1
	if failures < 1 {
		failures = 1
	}
	backoff := policy.BackoffInitial
	for i := 1; i < failures && backoff < policy.BackoffCap; i++ {
		backoff *= 2
	}
	if backoff > policy.BackoffCap {
		backoff = policy.BackoffCap
	}
	return time.Now().Add(backoff).Format(time.RFC3339)
}

// parseCheckInterval parses a duration string like "1h" / "30m" / "7d" /
// "5s" into time.Duration. Lives here (not in vtt_helpers.go, which was
// deleted) because it's a time-utility with no VTT / Ollama / exec
// coupling, so scheduler.go is the right owner.
func parseCheckInterval(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 7 * 24 * time.Hour, nil // default 7 days
	}
	switch s[len(s)-1] {
	case 'd':
		days := 0
		if _, err := fmt.Sscanf(s, "%dd", &days); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	case 'h':
		hours := 0
		if _, err := fmt.Sscanf(s, "%dh", &hours); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(hours) * time.Hour, nil
	case 'm':
		mins := 0
		if _, err := fmt.Sscanf(s, "%dm", &mins); err != nil {
			return 0, fmt.Errorf("invalid check_interval: %s", s)
		}
		return time.Duration(mins) * time.Minute, nil
	default:
		return time.ParseDuration(s)
	}
}

// extractChannelHandle derives the @-prefixed handle from a YouTube
// channel URL. Used by analyzer.go + enqueue.go for Prometheus labels.
// Lives here (not in enqueue.go, which would force analyzer.go to
// re-implement the regex) because both files need it.
func extractChannelHandle(url string) string {
	if url == "" {
		return ""
	}
	if idx := strings.LastIndex(url, "@"); idx >= 0 {
		handle := url[idx+1:]
		handle = strings.TrimRight(handle, "/")
		return handle
	}
	return ""
}
