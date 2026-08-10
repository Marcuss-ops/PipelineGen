// Package jobs — registry_capabilities.go (PR-SPLIT-JOBS-REGISTRY, July 2026).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): this file is the
// canonical SOLE owner of every typed accessor method on *Registry:
//
//   - Timeout + JobTimeout          (HC-1 typed-port)
//   - DefaultMaxRetries + GetMaxRetries (P0 Commit 9 typed-error chain)
//   - Queue + Concurrency           (Wave 19 / P1-9)
//   - ProducesArtifacts + ProducesArtifactsMap
//
// All 8 accessors share the canonical-default-coalesce semantics so a
// zero-or-empty entry value still produces a valid read for the
// caller. The typed-error contract (GetMaxRetries returning
// ErrRegistryRequired + ErrMaxRetriesUnknown via dual-%w) is preserved
// verbatim per the pre-split behavior.
//
// Lookup paths preserved: `reg.Timeout("foo")`, `reg.GetMaxRetries("foo")`,
// etc. all resolve identically pre/post split (same package).
package jobs

import (
	"fmt"
	"time"
)

// ── HC-1 timed accessor: Timeout() + JobTimeout() ──────────────────────

// Timeout returns the timeout for a job type, or the default (10 min).
func (r *Registry) Timeout(jobType string) time.Duration {
	if entry, ok := r.Get(jobType); ok && entry.Timeout > 0 {
		return entry.Timeout
	}
	return 10 * time.Minute
}

// JobTimeout is the canonical typed accessor for per-job-type
// execution timeouts. Naming mirrors the typed-port world; this is
// the method that satisfies the TimeoutResolver interface and
// what internal/app/clips_adapters_cfg.go::clipsCfgAdapter forwards
// to.
//
// HC-1 code-review REQUEST CHANGES rationale: JobTimeout is a typed-
// port alias for Timeout() — the dual-name surface exists because
// (a) Timeout() is the pre-HC-1 canonical method (kept for back-
// compat with any test fixture or future caller that imports the
// pre-HC-1 surface), and (b) JobTimeout() is the canonical name in
// the typed-port world (matches the adapter pattern in
// internal/app/clips_adapters_cfg.go::JobTimeout). Choice of name
// for new code: prefer JobTimeout — any reader/usecase introduced
// post-HC-1 should consume the typed-port surface.
//
// Behaviour is identical to Timeout(): returns the registered
// entry's Timeout if non-zero, else the canonical 10-minute default.
func (r *Registry) JobTimeout(jobType string) time.Duration {
	return r.Timeout(jobType)
}

// Compile-time assertion: *Registry satisfies TimeoutResolver.
// Catches signature drift at compile time (mirrors the Pattern 0
// convention used for typed config-port adapters).
//
// godlike/06 SSOT: this compile-time pin lives in
// registry_capabilities.go (the file where the JobTimeout method
// that satisfies TimeoutResolver is defined) — the type-chain
// `TimeoutResolver → JobTimeout() → *Registry` is canonical-SSOT
// one-canonical-owner-per-fact via symbol-co-location (the pin
// references *Registry which is in registry.go; same package).
var _ TimeoutResolver = (*Registry)(nil)

// ── P0 Commit 9 (July 2026): typed-error retry contract ────────────────

// DefaultMaxRetries returns the default max retries for a job type.
// Returns the canonical 3-retry safety net for unregistered jobTypes.
//
// Deprecated (PR-jobs-retry-contract, July 2026): callers that want a
// strict typed-error contract (no silent fallback) MUST migrate to
// GetMaxRetries(jobType) (int, error) below. This helper is retained
// for the *Worker*-side maxRetriesFor() retry hint (PR-JOBS-WORKER-
// MIGRATE — forward-pointer; not in scope for this PR per godlike/07
// minimum-blast-radius).
func (r *Registry) DefaultMaxRetries(jobType string) int {
	if entry, ok := r.Get(jobType); ok {
		return entry.DefaultMaxRetries
	}
	return 3
}

// GetMaxRetries is the typed lookup port consumed by
// *Service.resolveMaxRetries (PR-jobs-retry-contract, July 2026).
// Returns ErrMaxRetriesUnknown wrapped with %w on the typed jobType
// when the jobType is not registered — callers MUST propagate the error
// (NOT silently default to a legacy fallback per godlike/07
// no-fake-availability).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the typed-error
// contract here supersedes the pre-PR s.registry.DefaultMaxRetries
// helper which silently returned 3 for unknown types. GetMaxRetries is
// the load-bearing assertion for the *Service* resolution path. The
// DefaultMaxRetries helper still exists for *Worker*.maxRetriesFor
// (future migration tracked separately).
//
// nil-receiver guard: a nil Registry returns ErrRegistryRequired
// (defense-in-depth — Service.resolveMaxRetries callers MUST have a
// non-nil registry attached per the 4-arg NewService fail-closed
// constructor; this guard hardens the surface for future migration).
func (r *Registry) GetMaxRetries(jobType string) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("%w: nil registry", ErrRegistryRequired)
	}
	entry, ok := r.Get(jobType)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMaxRetriesUnknown, jobType)
	}
	return entry.DefaultMaxRetries, nil
}

// ── Wave 19 / P1-9 typed Queue + Concurrency accessors (June 2026) ─────
//
// Symmetric with the existing Timeout / DefaultMaxRetries accessors
// above. Each accessor applies the canonical defaults so consumers
// (worker.go, scheduler, ops dashboards) always observe a non-zero
// value for ANY registered job type:
//   - Queue(""):            → "default"
//   - Concurrency(N <= 0):  → 1
// The normalisation lives in Compose() so the underlying
// RegistryEntry CAN carry the zero value safely (e.g. while a
// per-job-type override is being staged in a feature branch);
// lookups are tolerant until override lands.

// Queue returns the canonical routing label for a job type.
// Registered entries with an empty Queue are reported under
// DefaultQueue so unresolved / legacy entries appear alongside
// the modern routing-shaped set under the same canonical key.
// Consumers MUST read Queue through this accessor rather than the
// raw entry.Queue field; bypassing the accessor observes the
// pre-applyDefaults zero-value (see RegistryEntry.Queue doc).
func (r *Registry) Queue(jobType string) string {
	if entry, ok := r.Get(jobType); ok && entry.Queue != "" {
		return entry.Queue
	}
	return DefaultQueue
}

// Concurrency returns the canonical concurrency budget for a job
// type. Registered entries with zero or negative Concurrency are
// reported as DefaultConcurrency — the per-worker "poll one at a
// time" canonical. Negative values (e.g. a misconfigured -1) are
// tolerated rather than rejected because Compose() must NEVER panic
// across a feature-branch override.
// Consumers MUST read Concurrency through this accessor rather than
// the raw entry.Concurrency field; bypassing the accessor observes the
// pre-applyDefaults zero-value (see RegistryEntry.Concurrency doc).
func (r *Registry) Concurrency(jobType string) int {
	if entry, ok := r.Get(jobType); ok && entry.Concurrency > 0 {
		return entry.Concurrency
	}
	return DefaultConcurrency
}

// ── Completion declaration accessors ───────────────────────────────────

// ArtifactOwnership returns the validated artifact owner for a registered
// job type. Unknown types return ArtifactOwnershipNone.
func (r *Registry) ArtifactOwnership(jobType string) ArtifactOwnership {
	if entry, ok := r.Get(jobType); ok {
		return entry.Completion.ArtifactOwnership
	}
	return ArtifactOwnershipNone
}

// FinalizationStrategy returns the validated terminal strategy for a
// registered job type. Unknown types use the safe legacy completion value.
func (r *Registry) FinalizationStrategy(jobType string) FinalizationStrategy {
	if entry, ok := r.Get(jobType); ok {
		return entry.Completion.FinalizationStrategy
	}
	return FinalizationStrategyLegacyComplete
}

// ProducesArtifacts is a compatibility projection for the SQLite completion
// gate and worker wiring. It is derived exclusively from ArtifactOwnership;
// there is no independent boolean declaration to drift from the policy.
func (r *Registry) ProducesArtifacts(jobType string) bool {
	return r.ArtifactOwnership(jobType) == ArtifactOwnershipWorkerSpine
}

// ProducesArtifactsMap returns the derived projection consumed by the
// SQLiteStore gate. Only worker-spine ownership requires the
// CompleteWithArtifacts path.
func (r *Registry) ProducesArtifactsMap() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.entries))
	for t, e := range r.entries {
		if e.Completion.ArtifactOwnership == ArtifactOwnershipWorkerSpine {
			out[t] = true
		}
	}
	return out
}
