package artifacts

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
)

// Wave 19 / P1-9 typed-port family:
//
//   SourceRepo      typed port (Get / GetByDriveFileID / Delete)
//   clipsSourceAdapter   wraps *assets.ClipsRepository       — used for
//                         artlist + clips + stock + sound_effect
//                         (all four share the canonical ClipsRepository)
//   voiceoverSourceAdapter wraps *assets.VoiceoversRepository — converts
//                         *assets.Record -> *asset.Asset via the
//                         existing VoiceoverRecordToClip helper
//   imagesSourceAdapter  wraps *imagesrepo.ImagesRepository      — converts
//                         *asset.ImageAsset -> *asset.Asset via
//                         ImageAssetToClip; relies on Go's automatic
//                         string->any boxing for the `id any` param
//   SourceCatalog        map asset.Source -> SourceRepo dispatcher (was SourceRegistry)
//
// The typed-port shape is intentionally MINIMAL — only the operations
// that the canonical deletion.go + future cross-capability flows need.
// Richer operations (List, Bulk Insert, etc.) stay on the concrete
// repos so the SourceRepo surface stays narrow enough that future
// drift (a fourth method, a renamed method, a signature change) is a
// single-file change at the adapter home rather than a cross-package
// rip.

// Wave 19 / P1-9 typed-port SourceRepo:
//
//	Get(ctx, id string)                  — single asset by primary id
//	GetByDriveFileID(ctx, driveFileID string) — single asset by Drive file id
//	Delete(ctx, id string)               — physical row delete (NOT soft-delete)
//
// Delete semantics (important for cross-source callers): the typed
// port's Delete methods PHYSICALLY remove the underlying row. The
// canonical soft-delete path (lifecycle_state=DELETED + Qdrant drain
// + sidecar cleanup) lives one level up the stack at
// outbox.Dispatcher.EnqueueAndDelete — the higher-level service
// composition is responsible for choosing the right path per source.
// Adapters implementing this interface MUST translate Delete to
// physical `DELETE FROM <table>` for the source they wrap; any
// lower-level repository that soft-deletes by default (Clips +
// SoftDeleteFilter) is the dispatcher's responsibility to route
// through EnqueueAndDelete, not the typed-port adapter's.
//
// The interface is the SSOT for what every canonical source's
// repository CAN must satisfy under Wave 19. Adding a method here is
// a deliberate cross-source contract change; the compile-time
// assertion var _ SourceRepo = (*<adapter>)(nil) below forces every
// adapter to keep up so drift surfaces at build time, not at the
// first Resolve call.
type SourceRepo interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetByDriveFileID(ctx context.Context, driveFileID string) (*asset.Asset, error)
	Delete(ctx context.Context, id string) error
}

// ── Per-source adapters ─────────────────────────────────────────────

// clipsSourceAdapter implements SourceRepo against *assets.ClipsRepository.
// The same adapter shape is reused for artlist+clips+stock+sound_effect
// because all four canonical sources share the canonical
// ClipsRepository concrete (column-distinguished by source discriminator).
// The SourceCatalog distinguishes them by canonical name only —
// every clips-family canonical name maps to a separate adapter
// constructed per-repository, so a future day when artlist gets its
// own shaped concrete is a single new adapter, not a reshape of the
// existing one.
type clipsSourceAdapter struct {
	inner *assets.ClipsRepository
}

func newClipsSourceAdapter(r *assets.ClipsRepository) SourceRepo {
	return &clipsSourceAdapter{inner: r}
}

func (a *clipsSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	return a.inner.Get(ctx, id)
}

func (a *clipsSourceAdapter) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	return a.inner.GetByDriveFileID(ctx, fileID)
}

// Delete dispatches to ClipsRepository.DeleteClip (PR-CLIP-RAW-MUTATIONS
// removed a public Delete on *ClipsRepository; the canonical physical
// path is DeleteClip; the typed-port surfaces it as Delete for
// symmetry with the voiceover/images SourceRepo shape).
//
// CAUTION: This call BYPASSES the outbox soft-delete gate. The
// canonical clips-source deletion path is
// outbox.Dispatcher.EnqueueAndDelete(ctx, clipID) (QDRANT-002 PR7),
// which atomically stamps index_state=DELETE_PENDING AND emits an
// outbox event before the dispatcher-side hard delete. Callers that
// route through `SourceCatalog.Resolve("artlist").Delete(...)` (or
// the clips/youtube/stock/sound_effect aliases) should FIRST check
// whether the deletion flow needs the soft-delete-then-drain
// semantics — if it does, route through the dispatcher; only call
// this typed-port adapter for direct-physical flows (admin tooling,
// offline batch backfills). Voiceover + images adapters do NOT
// have this concern because their underlying tables are not
// Qdrant-watched.
func (a *clipsSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.DeleteClip(ctx, id)
}

// voiceoverSourceAdapter implements SourceRepo against *assets.VoiceoversRepository.
// voiceovers uses an opaque *Record shape rather than *asset.Asset; the
// converter VoiceoverRecordToClip (defined in converters.go) bridges
// the shapes on every Read. The typed-port interface stays
// *asset.Asset-shaped so callers dispatch through a uniform return
// type.
type voiceoverSourceAdapter struct {
	inner *assets.VoiceoversRepository
}

func newVoiceoverSourceAdapter(r *assets.VoiceoversRepository) SourceRepo {
	return &voiceoverSourceAdapter{inner: r}
}

func (a *voiceoverSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	rec, err := a.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return VoiceoverRecordToClip(rec), nil
}

func (a *voiceoverSourceAdapter) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	rec, err := a.inner.GetByDriveFileID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return VoiceoverRecordToClip(rec), nil
}

func (a *voiceoverSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Delete(ctx, id)
}

// imagesSourceAdapter implements SourceRepo against *imagesrepo.ImagesRepository.
// ImagesRepository uses `id any` on its canonical methods to support
// both string slugids and int64 hash-row IDs against the same SQL
// skeleton. The SourceRepo interface keeps the call site uniform
// (typed `id string`); Go boxes string into any automatically at the
// call site, so the adapter is a thin shape-translation layer.
type imagesSourceAdapter struct {
	inner *imagesrepo.ImagesRepository
}

func newImagesSourceAdapter(r *imagesrepo.ImagesRepository) SourceRepo {
	return &imagesSourceAdapter{inner: r}
}

func (a *imagesSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	img, err := a.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, nil
	}
	return ImageAssetToClip(img), nil
}

func (a *imagesSourceAdapter) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	img, err := a.inner.GetByDriveFileID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, nil
	}
	return ImageAssetToClip(img), nil
}

func (a *imagesSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Delete(ctx, id)
}

// ── SourceCatalog ──────────────────────────────────────────────────

// SourceCatalog is the central source-metadata + typed-port dispatcher.
// Construction-time immutable: registered adapters can't be swapped at
// runtime (no Register method exposed after NewSourceCatalog returns).
// Falls back to (nil, false) for unknown sources so the call site can
// branch explicitly (zero-value-tolerant interface returns).
//
// Collapse (June 2026): SourceRegistry was renamed to SourceCatalog.
// SourceResolver + ResolveRepo + NewSourceResolver were DELETED —
// consumers that need *assets.ClipsRepository should inject it directly
// (all clip-type sources share the same concrete repo in production).
// The catalog owns source metadata (Normalize, MediaType, Names) and
// typed-port dispatch (Resolve → SourceRepo).
type SourceCatalog struct {
	byCanonical map[string]SourceRepo
}

// NewSourceCatalog wires the canonical source repositories into the
// canonical-keyed map. Canonical keys:
//
//	"artlist"        -> clipsSourceAdapter(artlist)
//	"clips"          -> clipsSourceAdapter(clips)
//	"stock"          -> clipsSourceAdapter(stock)
//	"voiceover"      -> voiceoverSourceAdapter(voiceover)
//	"images"         -> imagesSourceAdapter(images)
//	"sound_effect"   -> clipsSourceAdapter(clips)   <- alias-of-clips
//
// Nil-tolerant: a panel of nils is supported; the adapter covers nil
// fields with an explicit guard so test fixtures can wire a partial
// set of repos without panics.
func NewSourceCatalog(
	artlist, clips, stock *assets.ClipsRepository,
	voiceover *assets.VoiceoversRepository,
	images *imagesrepo.ImagesRepository,
) *SourceCatalog {
	reg := &SourceCatalog{byCanonical: make(map[string]SourceRepo, 7)}
	reg.byCanonical["artlist"] = newClipsSourceAdapter(artlist)
	reg.byCanonical["clips"] = newClipsSourceAdapter(clips)
	reg.byCanonical["stock"] = newClipsSourceAdapter(stock)
	reg.byCanonical["ai_generated"] = newClipsSourceAdapter(stock)
	reg.byCanonical["voiceover"] = newVoiceoverSourceAdapter(voiceover)
	reg.byCanonical["images"] = newImagesSourceAdapter(images)
	reg.byCanonical["sound_effect"] = newClipsSourceAdapter(clips)
	return reg
}

// Resolve returns the typed SourceRepo for any source-string
// (canonical name OR alias). Unknown / empty sources return
// (nil, false) so the call site branches explicitly.
//
// Receiver-nil-tolerant: a nil *SourceCatalog returns (nil, false).
func (c *SourceCatalog) Resolve(source string) (SourceRepo, bool) {
	if c == nil {
		return nil, false
	}
	canonical := c.Normalize(source)
	if canonical == "" {
		return nil, false
	}
	repo, ok := c.byCanonical[canonical]
	return repo, ok
}

// Normalize resolves any source alias to its canonical name.
// Returns empty string for unknown sources. Delegates to the
// package-level CanonicalSource which uses the StandardSources
// alias map.
func (c *SourceCatalog) Normalize(source string) string {
	return CanonicalSource(source)
}

// MediaType returns the media type for a canonical source name.
// Returns empty string for unknown sources.
func (c *SourceCatalog) MediaType(source string) string {
	canonical := CanonicalSource(source)
	for _, def := range StandardSources {
		if def.Canonical == canonical {
			return def.MediaType
		}
	}
	return ""
}

// Names returns the registered canonical source names in sorted
// order. Sorting guarantees deterministic iteration order.
func (c *SourceCatalog) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.byCanonical))
	for k := range c.byCanonical {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Compile-time assertions: every adapter satisfies SourceRepo. A
// future contributor adding a SourceRepo method MUST update all
// three adapters; this assertion surfaces a build error at the
// adapter home rather than a runtime panic at the first
// Resolve call (mirrors the Pattern 0 / typed-port convention
// used elsewhere in the codebase — e.g.
// internal/app/clips_adapters_cfg.go::var _ ClipConfigPort = ...).
var (
	_ SourceRepo = (*clipsSourceAdapter)(nil)
	_ SourceRepo = (*voiceoverSourceAdapter)(nil)
	_ SourceRepo = (*imagesSourceAdapter)(nil)
)

// SourceDefinition defines a canonical source with its aliases and associated repository.
type SourceDefinition struct {
	Canonical string
	Aliases   []string
	MediaType string
}

// StandardSources is the canonical list of all supported sources.
// Update this list when adding a new source — all resolvers use it.
var StandardSources = []SourceDefinition{
	{
		Canonical: "artlist",
		Aliases:   []string{"artlist"},
		MediaType: "video",
	},
	{
		Canonical: "clips",
		Aliases:   []string{"youtube", "clips"},
		MediaType: "video",
	},
	{
		Canonical: "stock",
		Aliases:   []string{"stock", "ai_generated"},
		MediaType: "video",
	},
	{
		Canonical: "voiceover",
		Aliases:   []string{"voiceover"},
		MediaType: "audio",
	},
	{
		Canonical: "images",
		Aliases:   []string{"images"},
		MediaType: "image",
	},
	{
		Canonical: "sound_effect",
		Aliases:   []string{"sound_effect", "sound_effects", "sfx"},
		MediaType: "audio",
	},
}

// sourceAliasMap is a lazy-built lookup for O(1) alias resolution.
// Built via buildSourceAliasMap() which is called lazily instead of init().
var sourceAliasMap map[string]string
var sourceAliasMapOnce sync.Once

func buildSourceAliasMap() {
	sourceAliasMapOnce.Do(func() {
		sourceAliasMap = make(map[string]string)
		for _, def := range StandardSources {
			for _, alias := range def.Aliases {
				sourceAliasMap[strings.ToLower(alias)] = def.Canonical
			}
		}
	})
}

// CanonicalSource resolves any source alias to its canonical name.
// Returns empty string if the source is unknown.
// Lazily builds the alias map on first call instead of using init().
func CanonicalSource(source string) string {
	buildSourceAliasMap()
	return sourceAliasMap[strings.ToLower(source)]
}

// IsValidSource checks if a source string (or alias) is known.
func IsValidSource(source string) bool {
	return CanonicalSource(source) != ""
}

// IsClipsSource returns true if the source maps to the clips repository.
func IsClipsSource(source string) bool {
	canonical := CanonicalSource(source)
	return canonical == "artlist" || canonical == "clips" || canonical == "stock" || canonical == "sound_effect"
}
