// Package monitor — policy.go: typed runtime policy for the
// ChannelMonitor. Lifted out of scheduler.go (Commit A, June 2026,
// fix P1 #10) because the previous hardcoded constants
// (schedulerTick=30s, defaultLeaseDuration=30min, claimLimit=10,
// MaxConcurrentVideos=5, perChannelTimeout=30min,
// workerIDPrefix="monitor") made runtime tuning require a code change
// + rebuild + restart.
//
// MonitorRuntimePolicy is the per-instance copy of those knobs. The
// composition root can wire a custom policy (e.g. for tests that need
// millisecond ticks to exercise backoff paths in O(seconds)); the
// production wiring (lifecycle.go::startBackgroundJobs) uses
// DefaultMonitorRuntimePolicy() unless cfg.Jobs.MonitorRuntimePolicy
// is set in a future PR.
//
// Pre-Wave 9 split (Step 9 / Blocco 6), this file doesn't exist and
// the scheduler.go constants block is the equivalent. The split
// follows the same logic as port extraction (Pattern 0): cross-cutting
// runtime knobs cross the package boundary through a typed struct
// rather than buried constants, so callers can override them without
// patching the file. The constructor NewChannelMonitor(CompositionDeps)
// accepts a *MonitorRuntimePolicy on CompositionDeps.Policy.
//
// Safe to add fields later because CompositionDeps already exposes
// the optional `*MonitorRuntimePolicy` shape (nil → defaults).
package assets

import "time"

// MonitorRuntimePolicy is the typed runtime configuration for the
// ChannelMonitor. All duration knobs come from here; none are
// hardcoded anymore.
//
//   - TickInterval: how often the outer scheduler re-claims due
//     channels. The previous hardcoded value was 30s.
//   - LeaseDuration: how long a ClaimDue lease is held before it
//     expires (and another worker can re-claim). The previous
//     hardcoded value was 30min.
//   - ClaimLimit: max channels claimed per scheduler tick. Default 10.
//   - MaxConcurrentChannels: max parallel per-channel goroutines the
//     monitor can spin up inside one tick. The previous value was 1
//     (with cfg.Concurrency.MaxConcurrentChannelChecks overriding at
//     composition). Kept separate from MaxConcurrentVideos (which is
//     the per-channel inner fan-out) because the two limits protect
//     different contention points — channels contend at the scheduler
//     level, videos contend at the yt-dlp subprocess level.
//   - MaxConcurrentVideos: max parallel per-channel per-video
//     goroutines inside checkChannel. The previous hardcoded value
//     was 5. This drives the inner `sync/atomic` budget slot pool.
//   - PerChannelTimeout: ctx-WithTimeout budget for a single channel
//     run (covers video listing + per-video AI gate + enqueue). The
//     previous hardcoded value was 30min. Detaching this from the
//     scheduler TickInterval is intentional: a long yt-dlp subprocess
//     should not be killed by the next-scheduler-tick firing.
//   - WorkerIDPrefix: the "monitor-" prefix used in the worker_id
//     stored on lease_owner. Kept as a knob so multi-tenant
//     deployments can disambiguate their worker IDs without colliding.
//   - BackoffInitial: starting backoff for the per-channel
//     exponential curve (5min → 10min → 20min → ...).
//   - BackoffCap: ceiling for the backoff curve (24h by default;
//     matches the production target of "channel rechecked daily after
//     long failure stretch").
//
// Zero-value Policy is INVALID: callers must use DefaultMonitorRuntimePolicy.
// The constructor NewChannelMonitor applies nil → Default for tests that
// pass the bare CompositionDeps.
type MonitorRuntimePolicy struct {
	TickInterval          time.Duration
	LeaseDuration         time.Duration
	ClaimLimit            int
	MaxConcurrentChannels int
	MaxConcurrentVideos   int
	PerChannelTimeout     time.Duration
	WorkerIDPrefix        string
	BackoffInitial        time.Duration
	BackoffCap            time.Duration
}

// defaultMonitorRuntimePolicy is the canonical, package-cached
// MonitorRuntimePolicy used by DefaultMonitorRuntimePolicy(). Cached
// (vs. allocated per call) so tests can assert pointer-identity in
// policyOrDefault's nil-fallback path (TestPolicyOrDefault_NilPolicy
// ReturnsDefault) AND production callers don't pay a per-tick
// allocation cost.
var defaultMonitorRuntimePolicy = &MonitorRuntimePolicy{
	TickInterval:          30 * time.Second,
	LeaseDuration:         30 * time.Minute,
	ClaimLimit:            10,
	MaxConcurrentChannels: 1,
	MaxConcurrentVideos:   5,
	PerChannelTimeout:     30 * time.Minute,
	WorkerIDPrefix:        "monitor",
	BackoffInitial:        5 * time.Minute,
	BackoffCap:            24 * time.Hour,
}

// DefaultMonitorRuntimePolicy is the canonical set of runtime knobs.
// Matches the previous hardcoded constants exactly so this commit is
// behavior-preserving for production callers (lifecycle.go still
// passes TickInterval-driven playback in O(seconds)).
//
// WorkerIDPrefix is intentionally non-empty so a zero-value policy
// doesn't slip past the constructor's missing-defaults guard.
//
// Returns the cached package-level pointer so repeated calls are
// allocation-free and pointer-comparable.
func DefaultMonitorRuntimePolicy() *MonitorRuntimePolicy {
	return defaultMonitorRuntimePolicy
}

// policyOrDefault returns the monitor's policy, falling back to the
// DefaultMonitorRuntimePolicy when nil. The fallback makes the split
// backward-compatible with existing tests that construct the struct by
// literal (`&ChannelMonitor{channelsSvc, log}`) without going through
// the CompositionDeps ctor.
func (m *ChannelMonitor) policyOrDefault() *MonitorRuntimePolicy {
	if m.policy != nil {
		return m.policy
	}
	return DefaultMonitorRuntimePolicy()
}
