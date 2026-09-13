// Package health — readyz_checkers_ytdlp.go (sister file E).
//
// PR-YTDLP-HEALTH-GUARD: the yt-dlp toolchain guard.
//
// yt-dlp is the single most failure-prone external dependency of the
// download pipeline: a stale release, or a yt-dlp that cannot reach a PO
// Token provider, is the usual cause of "The page needs to be reloaded" /
// HTTP 403 download failures (observed live: `PO Token Providers: none` →
// 403 on every player client).
//
// This check surfaces that drift. It is deliberately WARN-ONLY: a stale
// yt-dlp or a missing POT provider degrades download reliability, so /ready
// reports it as diagnostic context (ok=true + note) instead of flipping the
// service unhealthy. The checker warns in the log via the injected logger.
//
// Concurrency contract: the probes spawn a process (the POT probe also hits
// the network), so they must never run inline on the /ready request path —
// the request context is short-lived and a slow probe would both blow the
// readiness budget and time out. CheckYTDLPHealth therefore only reads a
// cached snapshot and, when it is cold/stale, triggers an asynchronous
// refresh; it never blocks. The snapshot is logged once per refresh.
package system

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	ytdlp "github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// DefaultYTDLPHealthTTL is how long a snapshot is considered fresh. The full
// probe spawns yt-dlp (and the POT probe performs a `--simulate` extraction),
// so /ready must not re-run it per request.
const DefaultYTDLPHealthTTL = 15 * time.Minute

// ytdlpRefreshTimeout bounds a single background refresh, independent of the
// caller's context (which is a short-lived HTTP request context).
const ytdlpRefreshTimeout = 2 * time.Minute

// YTDLPHealthReport is the toolchain snapshot the checker produces.
//
// VersionKnown is false when yt-dlp's version is not date-shaped (so Stale
// stays false rather than guessing). POTChecked is false until the POT probe
// has completed at least once.
type YTDLPHealthReport struct {
	Version      string
	VersionKnown bool
	AgeDays      int
	MaxAgeDays   int
	Stale        bool

	POTProviders []string
	POTChecked   bool
	POTMissing   bool

	// ProbeError accumulates non-fatal probe failures. Empty means both
	// probes completed.
	ProbeError string

	CheckedAt time.Time
}

// YTDLPHealthChecker is the ReadyChecker-facing port for the yt-dlp guard.
// CheckYTDLPHealth must be total and non-blocking (never panic, never return
// an error): it surfaces state through YTDLPHealthReport.
type YTDLPHealthChecker interface {
	CheckYTDLPHealth(ctx context.Context) YTDLPHealthReport
}

// DefaultYTDLPHealthChecker is the canonical implementation. The two probe
// functions are injected so the capability stays free of process/transport
// concerns (the composition root supplies them from internal/platform/ytdlp).
type DefaultYTDLPHealthChecker struct {
	versionFn  func(ctx context.Context) (string, error)
	potFn      func(ctx context.Context) ([]string, error)
	maxAgeDays int
	ttl        time.Duration
	log        *zap.Logger
	now        func() time.Time

	mu         sync.Mutex
	cached     YTDLPHealthReport
	hasCache   bool
	refreshing bool
}

// NewYTDLPHealthChecker wires the guard. A nil versionFn/potFn opts the
// corresponding probe out. A nil logger disables the warning log (the /ready
// note still carries the state).
func NewYTDLPHealthChecker(
	versionFn func(ctx context.Context) (string, error),
	potFn func(ctx context.Context) ([]string, error),
	maxAgeDays int,
	log *zap.Logger,
) *DefaultYTDLPHealthChecker {
	if maxAgeDays <= 0 {
		maxAgeDays = ytdlp.DefaultMaxVersionAgeDays
	}
	return &DefaultYTDLPHealthChecker{
		versionFn:  versionFn,
		potFn:      potFn,
		maxAgeDays: maxAgeDays,
		ttl:        DefaultYTDLPHealthTTL,
		log:        log,
		now:        time.Now,
	}
}

// WithTTL overrides the snapshot TTL (tests + operators tuning probe
// frequency). A non-positive TTL makes every read trigger a refresh.
func (c *DefaultYTDLPHealthChecker) WithTTL(ttl time.Duration) *DefaultYTDLPHealthChecker {
	if c != nil {
		c.ttl = ttl
	}
	return c
}

var _ YTDLPHealthChecker = (*DefaultYTDLPHealthChecker)(nil)

// CheckYTDLPHealth returns the cached snapshot without blocking. When the
// snapshot is cold or older than the TTL it schedules a background refresh and
// returns the last known state (or a "pending" report on the very first call).
func (c *DefaultYTDLPHealthChecker) CheckYTDLPHealth(_ context.Context) YTDLPHealthReport {
	if c == nil {
		return YTDLPHealthReport{}
	}
	now := c.now()

	c.mu.Lock()
	snapshot := c.cached
	hasCache := c.hasCache
	fresh := hasCache && c.ttl > 0 && now.Sub(snapshot.CheckedAt) < c.ttl
	needRefresh := !fresh && !c.refreshing
	if needRefresh {
		c.refreshing = true
	}
	c.mu.Unlock()

	if needRefresh {
		go c.refreshAsync()
	}
	if !hasCache {
		return YTDLPHealthReport{MaxAgeDays: c.maxAgeDays, CheckedAt: now}
	}
	return snapshot
}

// Refresh runs both probes synchronously, stores the snapshot and logs
// warnings. It is exported for tests and for a caller that wants to prime the
// cache deterministically; /ready uses the asynchronous path.
func (c *DefaultYTDLPHealthChecker) Refresh(ctx context.Context) YTDLPHealthReport {
	if c == nil {
		return YTDLPHealthReport{}
	}
	now := c.now()
	report := c.probe(ctx, now)

	c.mu.Lock()
	c.cached = report
	c.hasCache = true
	c.mu.Unlock()
	return report
}

// refreshAsync runs Refresh against a private, bounded context so the
// short-lived request context cannot cancel a background probe.
func (c *DefaultYTDLPHealthChecker) refreshAsync() {
	ctx, cancel := context.WithTimeout(context.Background(), ytdlpRefreshTimeout)
	defer cancel()
	report := c.Refresh(ctx)

	c.mu.Lock()
	c.refreshing = false
	c.mu.Unlock()

	if c.log != nil && report.ProbeError != "" {
		c.log.Warn("yt-dlp health guard: probe incomplete", zap.String("probe_error", report.ProbeError))
	}
}

// probe runs both probes and assembles the report (uncached).
func (c *DefaultYTDLPHealthChecker) probe(ctx context.Context, now time.Time) YTDLPHealthReport {
	report := YTDLPHealthReport{MaxAgeDays: c.maxAgeDays, CheckedAt: now}

	if c.versionFn != nil {
		version, err := c.versionFn(ctx)
		if err != nil {
			report.ProbeError = appendProbeError(report.ProbeError, "version: "+err.Error())
		} else {
			report.Version = strings.TrimSpace(version)
			if age, ok := ytdlp.ParseVersionAge(report.Version, now); ok {
				report.VersionKnown = true
				report.AgeDays = age
				report.Stale = ytdlp.VersionIsStale(age, c.maxAgeDays)
			}
		}
	}

	if c.potFn != nil {
		providers, err := c.potFn(ctx)
		report.POTChecked = true
		if err != nil {
			report.ProbeError = appendProbeError(report.ProbeError, "pot providers: "+err.Error())
			report.POTMissing = true
		} else {
			report.POTProviders = providers
			report.POTMissing = len(providers) == 0
		}
	}

	if c.log != nil {
		for _, warning := range warningsFor(report) {
			c.log.Warn("yt-dlp health guard", zap.String("warning", warning))
		}
	}
	return report
}

// StartupWarnings runs the offline version-staleness probe only and returns
// operator-facing warnings. It never touches the network, so it is safe to
// call at composition time. A missing/unprobeable yt-dlp returns nil: the
// canonical `tools` readiness check already owns "yt-dlp not installed".
func (c *DefaultYTDLPHealthChecker) StartupWarnings(ctx context.Context) []string {
	if c == nil || c.versionFn == nil {
		return nil
	}
	version, err := c.versionFn(ctx)
	if err != nil {
		return nil
	}
	age, ok := ytdlp.ParseVersionAge(strings.TrimSpace(version), c.now())
	if !ok || !ytdlp.VersionIsStale(age, c.maxAgeDays) {
		return nil
	}
	return []string{staleWarning(strings.TrimSpace(version), age, c.maxAgeDays)}
}

// warningsFor is the single canonical warning derivation (godlike/06 SSOT) so
// the log lines and the /ready note can never drift.
func warningsFor(report YTDLPHealthReport) []string {
	var warnings []string
	if report.Stale {
		warnings = append(warnings, staleWarning(report.Version, report.AgeDays, report.MaxAgeDays))
	}
	if report.POTChecked && report.POTMissing {
		warnings = append(warnings, fmt.Sprintf(
			"no PO Token provider detected — YouTube downloads may fail with 403/\"The page needs to be reloaded\" (providers=%s)",
			describeProviders(report.POTProviders)))
	}
	if report.ProbeError != "" {
		warnings = append(warnings, "yt-dlp probe error: "+report.ProbeError)
	}
	return warnings
}

func staleWarning(version string, ageDays, maxAgeDays int) string {
	return fmt.Sprintf(
		"resolved yt-dlp %s is %d days old (threshold %d) — run `yt-dlp -U` to pick up YouTube extractor fixes",
		version, ageDays, maxAgeDays)
}

func describeProviders(providers []string) string {
	if len(providers) == 0 {
		return "none"
	}
	return strings.Join(providers, ", ")
}

func appendProbeError(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}
