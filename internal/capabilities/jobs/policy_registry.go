// Package jobs — registry.go slim orchestrator (PR-SPLIT-JOBS-REGISTRY, July 2026).
//
// Per godlike/06 SSOT (one-canonical-owner-per-fact), the 590-LoC
// monolithic registry.go has been split into 3 sibling files in the
// SAME package:
//
//   - registry.go               (THIS FILE) — slim orchestrator:
//     Registry struct lifecycle (NewRegistry,
//     Register, Freeze, Get, IsRegistered,
//     AllTypes), private applyDefaults,
//     and the canonical Compose construction
//     path that wires the 5 per-family
//     helper files + applies defaults.
//
//   - registry_definitions.go   — policy defaults SSOT:
//     DefaultQueue + DefaultConcurrency.
//
//   - registry_types.go          — completion declaration, registry entry,
//     job policy alias, and identifier SSOT.
//
//   - registry_timeout.go       — TimeoutMap + TimeoutResolver port,
//     the canonical DefaultQueue +
//     DefaultConcurrency consts, and the
//     full Type... identifier block.
//
//   - registry_capabilities.go  — all 8 typed accessor methods:
//     Timeout + JobTimeout +
//     DefaultMaxRetries + GetMaxRetries +
//     Queue + Concurrency + ProducesArtifacts
//
//   - ProducesArtifactsMap (HC-1 + P0
//     Commit 9 + Wave 19 / P1-9 accessors).
//     ALSO hosts the compile-time pin
//     `var _ TimeoutResolver = (*Registry)(nil)`
//     — the type-chain `TimeoutResolver →
//     JobTimeout() → *Registry` is canonical
//     via symbol-co-location (the pin
//     references *Registry which lives here).
//
// Lookup paths preserved: jobs.NewRegistry, jobs.Registry, all
// `jobs.Registry.Timeout(...)`, `jobs.Registry.GetMaxRetries(...)`
// accessors, every `jobs.Type*` constant, and `jobs.Compose()` all
// resolve identically pre/post split (same package, no exported
// symbol drift).
package jobs

import (
	"fmt"
	"sync"
)

// ── Registry ────────────────────────────────────────────────────────────

// Registry is the unified job type registry. Safe for concurrent use after Freeze().
type Registry struct {
	mu      sync.RWMutex
	entries map[string]RegistryEntry
	frozen  bool
}

// NewRegistry creates an empty unfrozen registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]RegistryEntry)}
}

// Register adds a job type to the registry. Returns error if frozen or duplicate.
func (r *Registry) Register(entry RegistryEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("registry is frozen: cannot register %s", entry.Completion.JobType)
	}
	if entry.Completion.JobType == "" {
		return fmt.Errorf("job type must not be empty")
	}
	if err := entry.ValidateCompletion(); err != nil {
		return fmt.Errorf("job type %s: %w", entry.Completion.JobType, err)
	}
	if _, exists := r.entries[entry.Completion.JobType]; exists {
		return fmt.Errorf("job type %s already registered", entry.Completion.JobType)
	}
	r.entries[entry.Completion.JobType] = entry
	return nil
}

// Freeze prevents further registrations. Should be called after Compose().
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// Get returns the entry for a job type, or (nil, false) if not registered.
func (r *Registry) Get(jobType string) (RegistryEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[jobType]
	return entry, ok
}

// IsRegistered returns true if the job type is registered.
func (r *Registry) IsRegistered(jobType string) bool {
	_, ok := r.Get(jobType)
	return ok
}

// AllTypes returns all registered job type strings (for ClaimNext type filters).
func (r *Registry) AllTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.entries))
	for t := range r.entries {
		types = append(types, t)
	}
	return types
}

// applyDefaults is invoked by Compose() at the end of the canonical
// construction path. It mutates every entry in-place so the typed
// accessors are NOT the only path to canonical values; the raw
// entry's Queue / Concurrency also reads the canonical value after
// the pass. The pass is idempotent: re-running it on a Compose()-built
// registry produces the same final state.
//
// Design intent (vs. "validateConsistency" naming): this helper
// MUTATES, it does not validate. A future contributor who reads
// the name and expects a bool/error predicate would be surprised.
// The mutation semantics are intentional and documented; a
// rename to `validate` would be misleading.
//
// Failure mode: an internally-inconsistent override (e.g.
// Concurrency: -1 AND Queue: "") is silently normalised rather
// than rejected. The composition-time invariant is "every entry's
// observable shape is valid", not "every entry was authored
// correctly". Invalid integer values are corrected to
// DefaultConcurrency; empty queue strings are corrected to
// DefaultQueue. Both are documented in the typed accessor blocks
// in registry_capabilities.go.
//
// Caller scope: this helper is ONLY called from Compose(). It is
// not part of the public Registry API — manual Register() calls
// (during tests, for example) MUST supply a fully-populated
// RegistryEntry or rely on the typed accessors for the canonical
// lookups. The unexported name is the seam marker.
func (r *Registry) applyDefaults() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for t, e := range r.entries {
		if e.Queue == "" {
			e.Queue = DefaultQueue
		}
		if e.Concurrency <= 0 {
			e.Concurrency = DefaultConcurrency
		}
		r.entries[t] = e
	}
}

// ── Compose: canonical construction path ──────────────────────────────

// Compose returns a fresh type-keyed snapshot of every registered
// job-type timeout. Mirrors the per-call shape used in worker.go HC-1:
// `w.reg.Compose()[j.Type]`. The MU read-lock keeps the snapshot
// consistent across the iteration; the returned map is an independent
// copy safe for caller-side mutation.
//
// Zero-filter semantics (HC-1 code-review DISCUSS): entries with
// `Timeout == 0` (the canonical "use the default" shape) are filtered
// out of the snapshot. The complementary accessor `JobTimeout(t)`
// returns the canonical 10-minute default for entries with a zero
// Timeout. Worker.go's `jobTimeoutFor(t)` adds a `&& d > 0` guard so
// the two paths agree on the default.
//
// Rationale: an entry with Timeout = 0 is ambiguous ("explicit 0
// timeout" vs "default"); the conservative interpretation is "default"
// and we surface that consistently. Future contributors iterating
// `for t, d := range timeouts { ... }` MUST treat a missing key as
// "use the canonical default", not as "deliberately 0 timeout" —
// see Worker.jobTimeoutFor for the canonical guard pattern.
func (r *Registry) Compose() TimeoutMap {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(TimeoutMap, len(r.entries))
	for t, e := range r.entries {
		if e.Timeout > 0 {
			out[t] = e.Timeout
		}
	}
	return out
}

// Compose builds the standard registry with all known job types.
// Callers wire handlers via the Dispatcher; the registry only holds
// operational parameters (timeout, retries, queue, concurrency, capabilities).
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: job-type registration has
// been decomposed into 5 per-family files per AGENTS.md Pattern 5:
//
//	registry_voiceover.go  — Voiceover + subtitles
//	registry_script.go     — Script generation + curation
//	registry_extraction.go — Extraction + YouTube
//	registry_stock.go      — Stock media pipeline
//	registry_media.go      — Video, catalog, content, system, AI images
//	registry_texttracks.go  — Text-track materialization
//	registry_integrity.go   — Integrity and cleanup jobs
//
// Each family file exports a register<Family>Entries(r *Registry)
// helper called below.
//
// Wave 19 / P1-9 (June 2026): Queue + Concurrency fields are filled
// with the canonical defaults by Compose(); the applyDefaults() pass
// at the end re-asserts normalisation so future contributors can
// omit the fields (Queue="" -> DefaultQueue, Concurrency=0 ->
// DefaultConcurrency) without breaking the typed accessors. New code
// SHOULD prefer the JobPolicy literal name when registering entries
// (the type alias `JobPolicy = RegistryEntry` makes this a name-style
// preference, not a structural rename).
func Compose() *Registry {
	r := NewRegistry()

	registerScriptEntries(r)
	registerExtractionEntries(r)
	registerStockEntries(r)
	registerMediaEntries(r)
	registerVoiceoverEntries(r)
	registerTextTrackEntries(r)
	registerIntegrityEntries(r)
	registerAssemblyEntries(r)

	// Wave 19 / P1-9 normalisation pass: every registered entry
	// surfaces a non-empty Queue (DefaultQueue) and Concurrency
	// >= DefaultConcurrency from the typed accessors, regardless
	// of whether the literal set the field. Idempotent re-runs
	// are safe — the pass is a coalesce-to-canonical operation,
	// not a validation.
	r.applyDefaults()

	return r
}
