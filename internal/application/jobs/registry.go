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

// TimeoutMap returns a frozen snapshot of per-job-type execution timeouts
// for the Worker's fast-lookup path (HC-1, June 2026).
func (r *Registry) TimeoutMap() TimeoutMap {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := make(TimeoutMap, len(r.entries))
	for _, e := range r.entries {
		if e.Timeout > 0 {
			m[e.Type] = e.Timeout
		}
	}
	return m
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
	TypeAssetsCleanup         = "assets.cleanup"
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
	// PR 5 (June 2026 — codex/clips-cleanup-job): paginated async
	// cleanup handler that replaces the pre-PR5 synchronous
	// ListClipsPaged-10K scan. Long timeout because a real cleanup
	// can span many hours (250-row batches × N sources).
	r.Register(RegistryEntry{Type: TypeAssetsCleanup, Description: "Assets cleanup (paginated async)", Timeout: 4 * time.Hour, DefaultMaxRetries: 1})

	return r
}
