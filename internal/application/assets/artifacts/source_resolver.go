package artifacts

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
//   imagesSourceAdapter  wraps *assets.ImagesRepository      — converts
//                         *asset.ImageAsset -> *asset.Asset via
//                         ImageAssetToClip; relies on Go's automatic
//                         string->any boxing for the `id any` param
//   SourceRegistry        map asset.Source -> SourceRepo dispatcher
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
//   Get(ctx, id string)                  — single asset by primary id
//   GetByDriveFileID(ctx, driveFileID string) — single asset by Drive file id
//   Delete(ctx, id string)               — physical row delete (NOT soft-delete)
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
// The SourceRegistry distinguishes them by canonical name only —
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
// route through `SourceRegistry.Resolve("artlist").Delete(...)` (or
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

// imagesSourceAdapter implements SourceRepo against *assets.ImagesRepository.
// ImagesRepository uses `id any` on its canonical methods to support
// both string slugids and int64 hash-row IDs against the same SQL
// skeleton. The SourceRepo interface keeps the call site uniform
// (typed `id string`); Go boxes string into any automatically at the
// call site, so the adapter is a thin shape-translation layer.
type imagesSourceAdapter struct {
	inner *assets.ImagesRepository
}

func newImagesSourceAdapter(r *assets.ImagesRepository) SourceRepo {
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

// ── SourceRegistry ──────────────────────────────────────────────────

// SourceRegistry is the central asset.Source -> SourceRepo dispatcher.
// Construction-time immutable: registered adapters can't be swapped at
// runtime (no Register method exposed after NewSourceRegistry returns).
// Falls back to (nil, false) for unknown sources so the call site can
// branch explicitly (zero-value-tolerant interface returns).
//
// Pre-Commit-B history (June 2026): deletion.go::FindClipByDriveFileID
// carried an inline switch case `"artlist", "clips", "stock" / "voiceover"
// / "images"`. Commit B defines the typed-port family; deletion.go's
// switch is FROZEN at this commit and migrated in a follow-up PR
// (B2) so the migration blast radius is contained to one caller + the
// composition root (build_bundles_domain.go, module_media.go,
// service_test.go — three files) per AGENTS.md Git-Lesson-2
// direct-to-main workflow rules (no force-push; one PR per concern).
type SourceRegistry struct {
	byCanonical map[string]SourceRepo
}

// NewSourceRegistry wires the five canonical source repositories
// into the canonical-keyed map. Five canonical keys are populated:
//
//   "artlist"        -> clipsSourceAdapter(artlist)
//   "clips"          -> clipsSourceAdapter(clips)
//   "stock"          -> clipsSourceAdapter(stock)
//   "voiceover"      -> voiceoverSourceAdapter(voiceover)
//   "images"         -> imagesSourceAdapter(images)
//   "sound_effect"   -> clipsSourceAdapter(clips)   <- alias-of-clips
//
// The sound_effect alias-to-clips pattern mirrors the legacy
// SourceResolver.ResolveRepo handling (`"clips", "youtube", "sound_effect"`
// mapped to the same repository) so visual-effect waveforms
// inherited from the master clips store continue to resolve under
// the new canonical surface.
//
// Nil-tolerant: a panel of nils (artlist/set/clips/nil/stock/nil
// voiceover/images) is supported; the adapter covers nil fields
// with an explicit `if a.inner == nil { return nil, nil }` guard so
// test fixtures can wire a partial set of repos without panics.
func NewSourceRegistry(
	artlist, clips, stock *assets.ClipsRepository,
	voiceover *assets.VoiceoversRepository,
	images *assets.ImagesRepository,
) *SourceRegistry {
	reg := &SourceRegistry{byCanonical: make(map[string]SourceRepo, 6)}
	reg.byCanonical["artlist"] = newClipsSourceAdapter(artlist)
	reg.byCanonical["clips"] = newClipsSourceAdapter(clips)
	reg.byCanonical["stock"] = newClipsSourceAdapter(stock)
	reg.byCanonical["voiceover"] = newVoiceoverSourceAdapter(voiceover)
	reg.byCanonical["images"] = newImagesSourceAdapter(images)
	// sound_effect aliases to the canonical clips repo per the
	// legacy SourceResolver.ResolveRepo shape; preserves the
	// pre-Commit-B behaviour (clipsRepo is the visual-effects media
	// store, sound_effect waveform rows live in the same table).
	// sound_effect aliases to the canonical `clips` repository because
	// the sound_effect source was historically an ALIAS-OF-CLIPS:
	// waveform rows for sound effects share the same media_assets
	// table discriminator as video clips (source='sound_effect';
	// NOT source='clips'). Visual-effect assets routed through the
	// `artlist` repository are NOT subject to this alias — they
	// keep artlist's shape. The legacy SourceResolver.ResolveRepo
	// also collapsed "clips", "youtube", "sound_effect" onto the
	// same clips panel, so the alias pattern is preserved.
	reg.byCanonical["sound_effect"] = newClipsSourceAdapter(clips)
	return reg
}

// Resolve returns the typed SourceRepo for any source-string
// (canonical name OR alias). Unknown / empty sources return
// (nil, false) so the call site branches explicitly. CanonicalSource
// is the SSOT for alias normalisation; Resolve is a thin shape
// dispatch.
//
// Receiver-nil-tolerant: a nil *SourceRegistry returns (nil, false)
// safely so test fixtures can wire `&artifacts.SourceRegistry{}`
// placeholders without guarding every Resolve call.
func (r *SourceRegistry) Resolve(source string) (SourceRepo, bool) {
	if r == nil {
		return nil, false
	}
	canonical := CanonicalSource(source)
	if canonical == "" {
		return nil, false
	}
	repo, ok := r.byCanonical[canonical]
	return repo, ok
}

// Names returns the registered canonical source names in sorted
// order. Used by the enumeration test (registry_completeness_test.go,
// Commit C) to assert every registered canonical source maps to a
// usable adapter. Sorting guarantees deterministic iteration order
// so tests don't flake on map-iteration nondeterminism.
func (r *SourceRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.byCanonical))
	for k := range r.byCanonical {
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
		Aliases:   []string{"stock"},
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

// SourceResolver resolves source strings to their assets.ClipsRepository.
// This replaces all hand-written resolveRepo switch statements.
type SourceResolver struct {
	artlistRepo *assets.ClipsRepository
	clipsRepo   *assets.ClipsRepository
	stockRepo   *assets.ClipsRepository
}

// NewSourceResolver creates a resolver with the three standard clip repositories.
func NewSourceResolver(artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository) *SourceResolver {
	return &SourceResolver{
		artlistRepo: artlistRepo,
		clipsRepo:   clipsRepo,
		stockRepo:   stockRepo,
	}
}

// ResolveRepo returns the assets.ClipsRepository for the given source.
// Returns nil for voiceover and images (they use different repository types).
func (r *SourceResolver) ResolveRepo(source string) *assets.ClipsRepository {
	canonical := CanonicalSource(source)
	switch canonical {
	case "artlist":
		return r.artlistRepo
	case "clips", "youtube", "sound_effect":
		return r.clipsRepo
	case "stock":
		return r.stockRepo
	case "all", "unified":
		// Return clipsRepo as the primary access point for unified media_assets
		return r.clipsRepo
	default:
		return nil
	}
}
