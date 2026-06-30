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
//   - Lifecycle (Stop, stopCh).
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
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

const (
	schedulerTick        = 30 * time.Second
	defaultLeaseDuration = 30 * time.Minute
	maxBackoff           = 24 * time.Hour
	initialBackoff       = 5 * time.Minute
)

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
	clipsRepo   *assets.ClipsRepository // forward-compat; currently unused
	channelsSvc *channels.Service       // canonical channel source authority
	youtubeSvc  *youtube.Service        // legacy; reserved for forward-compat
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

	// Internal primitives.
	stopCh chan struct{}
	// globalSem is the rate-limiter semaphore per monitor: it bounds the
	// number of per-channel goroutines the scheduler can spin up at once.
	// Width comes from cfg.Concurrency.MaxConcurrentChannelChecks (fallback 1).
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

	maxChannels := 1
	if deps.Cfg != nil && deps.Cfg.Concurrency.MaxConcurrentChannelChecks > 0 {
		maxChannels = deps.Cfg.Concurrency.MaxConcurrentChannelChecks
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
		clipsRepo:   deps.ClipsRepo,
		channelsSvc: deps.ChannelsSvc,
		youtubeSvc:  deps.YoutubeSvc,
		log:         deps.Log,

		ytdlp:      deps.Ytdlp,
		transcript: deps.Transcript,
		analyzer:   deps.Analyzer,
		enqueuer:   deps.Enqueuer,

		stopCh:    make(chan struct{}),
		globalSem: make(chan struct{}, maxChannels),
	}
}

// Stop ends the scheduler loop. Dereferencing a closed channel panics;
// callers must invoke Stop at most once.
func (m *ChannelMonitor) Stop() {
	close(m.stopCh)
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

	m.log.Info("Channel monitor entering scheduling loop (first check via runSchedulerCycle)",
		zap.Duration("tick", schedulerTick))

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.runSchedulerCycle(ctx)
		case <-m.stopCh:
			m.log.Info("Channel monitor stopped")
			return
		case <-ctx.Done():
			m.log.Info("Channel monitor context cancelled")
			return
		}
	}
}

// runSchedulerCycle claims due channels and dispatches them to checkDueChannels.
func (m *ChannelMonitor) runSchedulerCycle(ctx context.Context) {
	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(defaultLeaseDuration).Format(time.RFC3339)
	workerID := "monitor-" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)

	result, err := m.channelsSvc.ClaimDue(ctx, channels.ClaimDueCommand{
		Now:        nowStr,
		WorkerID:   workerID,
		LeaseUntil: leaseUntil,
		Limit:      10,
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
// which runs checkChannel + records the outcome via recordCheckOutcome.
//
// The bound is MaxConcurrentChannelChecks (governed by m.globalSem, the
// per-monitor rate-limiter semaphore). Recovery from per-channel panic
// is local to the goroutine (the worker_id-derived MarkChecked call still
// fires — losing the outer panic propagation is required: otherwise the
// oracle-style scheduler would deadlock if a single bad channel poisoned
// the whole tick).
func (m *ChannelMonitor) checkDueChannels(ctx context.Context, chs []channels.Channel) {
	for _, ch := range chs {
		ch := ch

		m.globalSem <- struct{}{}
		go func() {
			defer func() {
				<-m.globalSem
				if r := recover(); r != nil {
					m.log.Error("panic in channel check goroutine",
						zap.Any("recover", r),
						zap.String("channel_id", ch.ID))
				}
			}()

			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()

			result, checkErr := m.checkChannel(checkCtx, ch)
			m.log.Info("channel check completed",
				zap.String("channel_id", ch.ID),
				zap.Bool("success", checkErr == nil),
				zap.Int("videos_discovered", result.VideosDiscovered),
				zap.Int("videos_enqueued", result.VideosEnqueued),
				zap.Int("videos_skipped", result.VideosSkipped))

			if recErr := m.recordCheckOutcome(ctx, ch, checkErr); recErr != nil {
				m.log.Error("Failed to mark channel as checked",
					zap.String("channel_id", ch.ID),
					zap.Error(recErr))
			}
		}()
	}
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
func (m *ChannelMonitor) recordCheckOutcome(ctx context.Context, ch channels.Channel, checkErr error) error {
	success := checkErr == nil
	lastErr := ""
	if checkErr != nil {
		lastErr = checkErr.Error()
	}
	nextCheckAt := m.nextCheckTime(ch, success)
	return m.channelsSvc.MarkChecked(ctx, channels.MarkCheckedCommand{
		ID:          ch.ID,
		NextCheckAt: nextCheckAt,
		Success:     success,
		LastError:   lastErr,
	})
}

// nextCheckTime returns the RFC3339 RFC3339-format string for when
// the channel should be checked next, following the exponential
// backoff curve on failure.
func (m *ChannelMonitor) nextCheckTime(ch channels.Channel, success bool) string {
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
	backoff := initialBackoff
	for i := 1; i < failures && backoff < maxBackoff; i++ {
		backoff *= 2
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
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
