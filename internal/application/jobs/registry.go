// Package job provides the unified job type registry — the single source of truth
// for every job type in the system.
//
// Each job type is registered once with its handler, retry policy, timeout,
// and required capabilities. The registry prevents duplicate types, handler-less
// types, and free-floating string constants.
//
// The registry is frozen after Compose() so no new types can be registered
// after startup (fail-fast on misconfiguration).
package jobs

import (
	"fmt"
	"sync"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Registry ────────────────────────────────────────────────────────────

// Wave 19 / P1-9 (June 2026) canonical policy constants. Single source
// of truth for default routing / concurrency values; the typed
// accessors below + Compose()'s applyDefaults pass both reference
// these constants so a future rename (e.g. "primary" instead of
// "default") is a one-line change.
const (
	// DefaultQueue is the canonical routing label assigned to any
	// registered job type whose Queue field is empty. The string is
	// hard-coded here so the typed accessor Queue(t), the applyDefaults
	// pass in Compose(), and any future operator-facing dashboard
	// surface agree on the same default label.
	DefaultQueue = "default"
	// DefaultConcurrency is the canonical per-worker parallel-lease
	// budget for any registered job type whose Concurrency field is
	// zero or negative. The value 1 mirrors the pre-Wave-19 in-process
	// broker semantics: a worker polls one job at a time.
	DefaultConcurrency = 1
)

// RegistryEntry defines a registered job type with its operational parameters.
//
// Wave 19 / P1-9 (June 2026): the canonical policy record. The
// Queue + Concurrency fields extend the pre-Wave-19 surface so that
// every registered job type carries operator-facing controls in ONE
// shape (Timeout + DefaultMaxRetries + Queue + Concurrency + caps).
// The pre-Wave-19 name RegistryEntry is preserved because callers
// across internal/app, internal/application/scheduler, and the
// dispatcher wiring reference it directly; the JobPolicy alias
// (declared below) is the user-facing semantic name.
//
// Job type policy contract (Wave 19 / P1-9):
//
//	JobPolicy{
//	    Type:                canonicalJobType,                  // e.g. "script.generate"
//	    Description:         human-readable string,             // operator-facing
//	    Timeout:             per-job execution cap,             // 0 = canonical 10m default
//	    DefaultMaxRetries:   retry count when caller omits,     // canonical = 3
//	    Queue:               queue label (string),              // empty -> DefaultQueue
//	    Concurrency:         per-worker parallel-leases,        // <=0 -> DefaultConcurrency
//	    RequiredCapabilities: capability tags worker must have,  // empty = any worker
//	}
//
// Field-default canon (Wave 19): every registered job type surfaces a
// non-zero Queue (DefaultQueue) and Concurrency (>=DefaultConcurrency)
// through the typed accessors after applyDefaults() applies them. The
// raw entry.Queue / entry.Concurrency MAY read "" / 0 before the
// pass; consumers MUST read through the typed `Registry.Queue(t)` and
// `Registry.Concurrency(t)` accessors rather than the raw field so
// the canonical values are observed regardless of whether the literal
// was set.
//
// Migration to Wave 19 fields is field-additive (no struct rename).
// Because JobPolicy is a Go type alias (not a new named type), all
// pre-Wave-19 `RegistryEntry{...}` literals and `RegistryEntry`-by-
// name references in production code compile unchanged. New code MUST
// prefer the JobPolicy name when declaring entries.
type RegistryEntry struct {
	// Type is the canonical job type string (e.g. "script.generate_from_clips").
	Type string
	// Description is a human-readable description for operators.
	Description string
	// Timeout is the per-job execution timeout. Zero means use the default (10 min).
	Timeout time.Duration
	// DefaultMaxRetries is the default retry count when the caller doesn't specify.
	DefaultMaxRetries int
	// Queue is the routing label for the in-process broker.
	//
	// Today the broker is in-process (Worker polls SQLite), so Queue is
	// a logical-grouping label rather than a hard routing key. Future
	// broker upgrades (e.g. a dedicated message bus with multiple queue
	// partitions) read this field to bucket workers per job family.
	// Empty value is normalised to DefaultQueue by Compose() so the
	// typed accessor `Registry.Queue(t)` returns a non-empty label for
	// ANY registered job type.
	Queue string
	// Concurrency is the per-worker parallel-lease budget for this job type.
	//
	// Today workers poll one job at a time, so this is a CLAMP rather
	// than an immediate concurrency control. Future broker upgrades
	// (e.g. an in-process semaphore that throttles expensive job types)
	// read this field to cap per-worker fan-out. Zero or negative
	// values are normalised to DefaultConcurrency by Compose() so the
	// typed accessor `Registry.Concurrency(t)` returns a value
	// >= DefaultConcurrency for ANY registered job type.
	Concurrency int
	// RequiredCapabilities are worker capabilities needed (empty = any worker).
	RequiredCapabilities []string

	// ProducesArtifacts is true when this job type produces files (videos,
	// images, documents, audio, etc.) that must be finalised through the
	// JobFinalizer spine. Jobs with ProducesArtifacts=true MUST call
	// CompleteWithArtifacts instead of the legacy Complete path.
	//
	// When true, SQLiteStore.Complete rejects completions with the canonical
	// typed sentinel domainremote.ErrCompleteJobPathViolation (godlike/06
	// SSOT: the typed sentinel at internal/domain/remote/complete_job.go is
	// the SINGLE canonical owner of the failure mode "legacy Complete path
	// attempted on artifact-producing job"; the pre-FASE-0.1 package-local
	// alias ErrArtifactJobRequiresCompleteWithArtifacts was REMOVED).
	ProducesArtifacts bool
}

// JobPolicy is the canonical alias for RegistryEntry. New code MUST
// prefer the JobPolicy name when declaring or augmenting entries;
// the RegistryEntry alias is kept for back-compat with pre-Wave-19
// callers that reference the field by name directly. Both names refer
// to the SAME type (Go type-alias semantics — not a named distinct
// type), so adapting a pre-Wave-19 RegistryEntry literal to the
// Wave-19 surface is a field-additive change, not a struct rename.
type JobPolicy = RegistryEntry

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
		return fmt.Errorf("registry is frozen: cannot register %s", entry.Type)
	}
	if entry.Type == "" {
		return fmt.Errorf("job type must not be empty")
	}
	if _, exists := r.entries[entry.Type]; exists {
		return fmt.Errorf("job type %s already registered", entry.Type)
	}
	r.entries[entry.Type] = entry
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

// Timeout returns the timeout for a job type, or the default (10 min).
func (r *Registry) Timeout(jobType string) time.Duration {
	if entry, ok := r.Get(jobType); ok && entry.Timeout > 0 {
		return entry.Timeout
	}
	return 10 * time.Minute
}

// ── HC-1 Typed Timeout Surface (June 2026) ─────────────────────────────
// HC-1 (June 2026) replaces the pre-HC-1 package-level
// `var jobTimeoutRegistry` global in worker.go with a type-keyed
// lookup rooted here on Registry. Three new entities:
//
//   - TimeoutMap         : a type-keyed snapshot of every registered
//                          job-type timeout (Compose()).
//   - TimeoutResolver    : the typed port both worker.go and the
//                          bulk_upload config-port consume.
//   - JobTimeout(t)      : canonical method on Registry, aliased to
//                          Timeout(t) for naming consistency with
//                          the typed-port world.
//
// Anti-reintro: Check 40 in scripts/ci-architectural-checks.sh
// blocks re-introduction of `var jobTimeoutRegistry` or
// `SetJobTimeout` callers.

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

// IsRegistered returns true if the job type is registered.
func (r *Registry) IsRegistered(jobType string) bool {
	_, ok := r.Get(jobType)
	return ok
}

// ── Wave 19 / P1-9 typed Queue + Concurrency accessors ─────────────
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
// the raw entry.Concurrency field; bypassing the accessor observes
// the pre-applyDefaults zero-value (see RegistryEntry.Concurrency doc).
func (r *Registry) Concurrency(jobType string) int {
	if entry, ok := r.Get(jobType); ok && entry.Concurrency > 0 {
		return entry.Concurrency
	}
	return DefaultConcurrency
}

// ProducesArtifacts returns true if the job type is registered as an
// artifact-producing job. Artifact-producing jobs MUST use
// CompleteWithArtifacts instead of the legacy Complete path.
func (r *Registry) ProducesArtifacts(jobType string) bool {
	if entry, ok := r.Get(jobType); ok {
		return entry.ProducesArtifacts
	}
	return false
}

// ProducesArtifactsMap returns a read-only map of job types that produce
// artifacts, keyed by type string. Used to configure the SQLiteStore gate.
func (r *Registry) ProducesArtifactsMap() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.entries))
	for t, e := range r.entries {
		if e.ProducesArtifacts {
			out[t] = true
		}
	}
	return out
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
// above.
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

// TimeoutMap is the type-keyed lookup of per-job-type execution
// timeouts. Returned by (*Registry).Compose() as a fresh snapshot
// so callers cannot mutate the underlying registry state.
//
// Usage: `reg.Compose()[j.Type]` returns the canonical timeout for
// job type j.Type, or zero if not registered (worker.go treats zero
// as the canonical 10-minute default).
type TimeoutMap map[string]time.Duration

// TimeoutResolver is the typed timeout lookup port consumed by HC-1
// worker.go (replaces the pre-HC-1 package-level global) and by the
// HC-1 bulk_upload config-port (see clips.ClipConfigPort.JobTimeout
// + internal/app/clips_adapters_cfg.go::clipsCfgAdapter).
//
// *Registry satisfies this interface directly (via JobTimeout). A
// narrow port interface lets future consumers (e.g. an admin-driven
// override layer) satisfy the contract without forcing them to also
// be a Registry.
type TimeoutResolver interface {
	JobTimeout(jobType string) time.Duration
}

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
var _ TimeoutResolver = (*Registry)(nil)

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

// ── Standard job.Job Types ──────────────────────────────────────────────────

// Each constant is the canonical string identifier. These are the SSOT; no
// other package should define job type string literals.
const (
	TypeMediaExtract          = "media.extract"
	TypeMediaStock            = "media.stock"
	TypeVoiceoverBatch        = "voiceover.batch"
	TypeVoiceoverGenerate     = job.TypeVoiceoverGenerate
	TypeSubtitleGenerate      = "subtitle.generate"
	TypeRenderVideo           = "render.video"
	TypeYouTubeUpload         = "youtube.upload"
	TypeYouTubeClipExtract    = "youtube_clip.extract"
	TypeCatalogSync           = "catalog.sync"
	TypeArtlistRun            = "media.artlist"
	TypeSystemCleanup         = "system.cleanup"
	TypeMediaGenerate         = "media.generate_missing_asset"
	TypeVideoGenerate         = "video.generate"
	TypeBooksProcess          = "books.process"
	TypeLessonsProcess        = "lessons.process"
	TypeMediaReindex          = "media.reindex"
	TypeMediaEnrich           = job.TypeMediaEnrich
	TypeYouTubeRebuildST      = "youtube.rebuild_search_text"
	TypeScriptGenerate        = job.TypeScriptGenerate
	TypeBulUploadYouTubeClips = "media.bulk_upload_youtube_clips"
	TypeDriveFolderSync       = "drive.folder.sync"
	TypeMediaCurate           = job.TypeMediaCurate
	TypeVoiceoverPromo        = job.TypeVoiceoverPromo
	// TypeVoiceoverGenerateItem is the per-language child job scheduled by the
	// parent voiceover.generate handler via FanoutVoiceoversUseCase
	// (PR-VOICEOVER-PARENT-CHILD-FANOUT, June 2026). Concurrency is
	// regulated by the registry's per-job-type Concurrency field
	// (configured at compose time), NOT by goroutines inside the API.
	TypeVoiceoverGenerateItem = job.TypeVoiceoverGenerateItem
	TypeImageGenerateGoogle   = "image.generate.google"

	// P0 Commit 2 (July 2026) canonical aliases — declared in this block
	// (NOT in codec.go) so the package-level re-export surface stays in
	// ONE place. registry_codec_completeness_test.go uses these as bare
	// identifiers when wiring canonical codecs; the canonical strings
	// themselves live in internal/domain/job/job.go (per godlike/02
	// §Capability-specific constants stay in their owning domain package).
	TypeImagesGenerate   = job.TypeImagesGenerate
	TypeDocumentGenerate = job.TypeDocumentGenerate
	TypeAssetsResolve    = job.TypeAssetsResolve

	// Step 11B (July 2026) sibling-job type aliases. Canonical strings
	// ("script.spawn_voiceover", "script.spawn_images") live in
	// internal/domain/job/job.go per godlike/02 §Capability-specific
	// constants stay in their owning domain package.
	TypeScriptVoiceoverSibling = job.TypeScriptVoiceoverSibling
	TypeScriptImageSibling     = job.TypeScriptImageSibling

	// ── P0 #4 audit (audit 2026-07-03) child-job type ──
	// Audit 2026-07-03 P0 #4: per-item retry in script batches via
	// canonical child-job architecture (mirror of voiceover P0 #1
	// closure, commit 7f319edb). Each item in a multi-item batch
	// becomes a script.generate_item job with its own broker-side
	// retry envelope. Concurrency=4 per-worker per Step 11B/12B
	// sibling-fan-out budget; independent per-item retry. The
	// parent aggregator (internal/application/scripts/jobs/
	// parent_aggregator.go) ticks the children and emits FinalizeAggregateParent
	// with target_status=FAILED when aggregate=failed_terminal per
	// godlike/07 (no fake availability), otherwise SUCCEEDED.
	TypeScriptGenerateItem = job.TypeScriptGenerateItem

	// PR-BATCH-REGISTER-ASYNC (July 2026): async clip registration via
	// the /api/media/register-batch endpoint. Each clip becomes an
	// independent media.clip job; yt-dlp + cut + Drive upload + DB write
	// happen off the request thread. ProducesArtifacts=false because the
	// registration pipeline persists its own media_assets row + outbox
	// events inside a per-clip tx (mirror of youtube_clip.extract); the
	// broker's legacy Complete is the canonical mark-SUCCEEDED seam.
	TypeClipRegister = job.TypeClipRegister
)

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
//
// Each family file exports a register<Family>Entries(r *Registry)
// helper called below.
//
// Wave 19 / P1-9 (June 2026): Queue + Concurrency fields are filled
// with the canonical defaults by Compose(); the applyDefaults()
// pass at the end re-asserts normalisation so future contributors
// can omit the fields (Queue="" -> DefaultQueue, Concurrency=0 ->
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

	// Wave 19 / P1-9 normalisation pass: every registered entry
	// surfaces a non-empty Queue (DefaultQueue) and Concurrency
	// >= DefaultConcurrency from the typed accessors, regardless
	// of whether the literal set the field. Idempotent re-runs
	// are safe — the pass is a coalesce-to-canonical operation,
	// not a validation.
	r.applyDefaults()

	return r
}
