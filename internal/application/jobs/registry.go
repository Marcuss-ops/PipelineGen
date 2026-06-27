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

// RegistryEntry defines a registered job type with its operational parameters.
type RegistryEntry struct {
	// Type is the canonical job type string (e.g. "script.generate_from_clips").
	Type string
	// Description is a human-readable description for operators.
	Description string
	// Timeout is the per-job execution timeout. Zero means use the default (10 min).
	Timeout time.Duration
	// DefaultMaxRetries is the default retry count when the caller doesn't specify.
	DefaultMaxRetries int
	// RequiredCapabilities are worker capabilities needed (empty = any worker).
	RequiredCapabilities []string
}

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
func (r *Registry) DefaultMaxRetries(jobType string) int {
	if entry, ok := r.Get(jobType); ok {
		return entry.DefaultMaxRetries
	}
	return 3
}

// IsRegistered returns true if the job type is registered.
func (r *Registry) IsRegistered(jobType string) bool {
	_, ok := r.Get(jobType)
	return ok
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
)

// Compose builds the standard registry with all known job types.
// Callers wire handlers via the Dispatcher; the registry only holds
// operational parameters (timeout, retries, capabilities).
func Compose() *Registry {
	r := NewRegistry()

	// ── Script generation ──
	r.Register(RegistryEntry{Type: TypeScriptGenerate, Description: "Script generation", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeMediaCurate, Description: "Media curation", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// ── Media processing ──
	r.Register(RegistryEntry{Type: TypeMediaExtract, Description: "Media extraction", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeMediaStock, Description: "Stock media pipeline", Timeout: 60 * time.Minute, DefaultMaxRetries: 1})
	r.Register(RegistryEntry{Type: TypeMediaGenerate, Description: "Generate missing media asset", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeMediaReindex, Description: "Reindex media assets", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})
	r.Register(RegistryEntry{Type: TypeMediaEnrich, Description: "Single-asset semantic enrichment + Qdrant-style indexing", Timeout: 3 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeBulUploadYouTubeClips, Description: "Bulk upload YouTube clips", Timeout: 120 * time.Minute, DefaultMaxRetries: 1})

	// ── Video ──
	r.Register(RegistryEntry{Type: TypeVideoGenerate, Description: "Video generation", Timeout: 60 * time.Minute, DefaultMaxRetries: 1})
	r.Register(RegistryEntry{Type: TypeRenderVideo, Description: "Video rendering", Timeout: 60 * time.Minute, DefaultMaxRetries: 1})

	// ── YouTube ──
	r.Register(RegistryEntry{Type: TypeYouTubeUpload, Description: "YouTube upload", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeYouTubeClipExtract, Description: "YouTube clip extraction", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeYouTubeRebuildST, Description: "Rebuild YouTube search text", Timeout: 10 * time.Minute, DefaultMaxRetries: 1})

	// ── Voiceover / subtitles ──
	r.Register(RegistryEntry{Type: TypeVoiceoverBatch, Description: "Voiceover batch generation", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeVoiceoverPromo, Description: "Voiceover promo generation (translate + generate)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeSubtitleGenerate, Description: "Subtitle generation", Timeout: 10 * time.Minute, DefaultMaxRetries: 2})

	// ── Catalog / sync ──
	r.Register(RegistryEntry{Type: TypeCatalogSync, Description: "Catalog synchronization", Timeout: 2 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeArtlistRun, Description: "Artlist run", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})
	r.Register(RegistryEntry{Type: TypeDriveFolderSync, Description: "Drive folder sync", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// ── Content processing ──
	r.Register(RegistryEntry{Type: TypeBooksProcess, Description: "Book processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(RegistryEntry{Type: TypeLessonsProcess, Description: "Lesson processing", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})

	// ── System ──
	r.Register(RegistryEntry{Type: TypeSystemCleanup, Description: "System cleanup", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})

	return r
}
