// registry_lifecycle.go owns the LIFECYCLE of the destination table:
// which DestinationKey exists, what its default RootFolderID /
// Namespace / PathBuilder / RequireSubpath / ConflictPolicy is, and
// how it is captured eagerly at registry construction time.
//
// godlike/06 SSOT (one canonical owner per fact, July 2026): this file
// is the SINGLE canonical owner of "which DestinationKey → which Drive
// root + path-builder + collision handling". Adding a new destination
// key means adding exactly one entry to buildDestinationPolicies below
// and adding the DestinationKey constant in types.go — nowhere else.
//
// Companion files in this package:
//
//	registry.go            — type surface + lookup methods (Has/Resolve/Keys)
//	registry_lifecycle.go  — the policy table (THIS FILE)
//	registry_transport.go  — the per-destination path-builder implementations
//
// Public surface (split-delivery-registry, July 2026): the public
// gateway is NewDestinationRegistry in registry.go. The body of the
// ctor delegates the map assembly to buildDestinationPolicies here so
// that callers see one canonical constructor while internal
// responsibility for the policy table stays tightly scoped.
package delivery

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildDestinationPolicies assembles the per-destination policies from
// the application config. Pure data — no I/O, no validation, no logging.
// Called ONCE by NewDestinationRegistry; the resulting map is captured
// eagerly into DestinationRegistry.policies so the registry is immutable
// after construction.
//
// Per-destination ConflictPolicy mapping (P1.1, July 2026) — see
// DestinationPolicy.ConflictPolicy for the rationale. The map below is
// alphabetised to make the canonical key-set visible at a glance; the
// resulting map is otherwise unordered, so reordering for readability
// is safe. Adding a new DestinationKey requires adding one entry here
// AND constant in types.go AND a completeness test pin in
// registry_test.go::TestRegistry_AllKeysPresent.
func buildDestinationPolicies(cfg *config.Config) map[DestinationKey]DestinationPolicy {
	return map[DestinationKey]DestinationPolicy{
		// DestinationAdmin (P1, July 2026): canonical destination for
		// admin CLI uploads. Always resolves to MediaRootFolder with
		// no namespace wrapper (specificRoot="" forces the unified
		// media-root branch).
		DestinationAdmin: {
			RootFolderID:   cfg.Drive.RootFolder(),
			Namespace:      "admin",
			PathBuilder:    maybeWrapNamespace(cfg, "admin", "", AdminPath),
			RequireSubpath: false,
			ConflictPolicy: ConflictOverwrite, // P1: admin CLI always overwrites
		},
		// DestinationArtlist: curated artlist assets. Immutable versioned
		// uploads — collisions are content-hash dupes that should not
		// overwrite.
		DestinationArtlist: {
			RootFolderID:   cfg.Drive.ArtlistFolder(),
			Namespace:      "artlist",
			PathBuilder:    maybeWrapNamespace(cfg, "artlist", cfg.Drive.ArtlistRootFolder, ArtlistPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // curated artlist asset
		},
		// DestinationBook: regenerable book-processing outputs.
		// Latest version wins (per P1.1 semantics mapping).
		DestinationBook: {
			RootFolderID:   cfg.Drive.BooksFolder(),
			Namespace:      "books",
			PathBuilder:    maybeWrapNamespace(cfg, "books", cfg.Drive.BooksRootFolder, BookPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictOverwrite, // regenerable summary
		},
		// DestinationClipMetadata (P0-#1 atomic-RMW cutover, July 2026):
		// canonical policy for the per-folder metadata.json sidecar
		// that backs UpdateCumulativeMetadataJSON. The sidecar lives
		// in the clip's already-resolved folder (no path-builder
		// nesting — the caller threads folderID via DestinationFolderID
		// so the publisher uses it directly), and the
		// ConflictPolicy is ConflictOverwrite because the sidecar is a
		// regenerable ledger (the latest merged entries win). RequireSubpath
		// is false because the DestinationFolderID path is the entire destination
		// (no further nesting). godlike/06 SSOT: the PathBuilder returns
		// an empty slice (mirrors DestinationAdmin) so the publisher
		// uses DestinationFolderID verbatim without trying to nest
		// under a default root.
		DestinationClipMetadata: {
			RootFolderID:   "",               // caller-provided via DestinationFolderID
			Namespace:      "",               // no namespace — lives in the resolved clip folder
			PathBuilder:    ClipMetadataPath, // returns [] to signal "use DestinationFolderID verbatim"
			RequireSubpath: false,            // DestinationFolderID IS the destination folder
			ConflictPolicy: ConflictOverwrite,
		},
		// DestinationDocument: regenerable PDF/DOCX outputs. Latest
		// version wins.
		DestinationDocument: {
			RootFolderID:   cfg.Drive.DocumentsFolder(),
			Namespace:      "documents",
			PathBuilder:    maybeWrapNamespace(cfg, "documents", cfg.Drive.ScriptsRootFolder, DocumentPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictOverwrite, // latest PDF/DOCX wins
		},
		// DestinationImage: P1 image-dedupe surface — skip on content
		// hash match (vs. the immutability-only ConflictSkip used
		// elsewhere). The publisher resolves ConflictSkipByHash to
		// ConflictSkip at the PutFile seam pending the full DRAM sidecar
		// implementation; the typed enum is captured here so the
		// intent is explicit at construction time.
		DestinationImage: {
			RootFolderID:   cfg.Drive.ImagesFolder(),
			Namespace:      "images",
			PathBuilder:    maybeWrapNamespace(cfg, "images", cfg.Drive.ImagesRootFolder, ImagePath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkipByHash, // P1: skip when content hash matches
		},
		// DestinationScript: regenerable script outputs. Latest
		// version wins.
		DestinationScript: {
			RootFolderID:   cfg.Drive.ScriptsFolder(),
			Namespace:      "scripts",
			PathBuilder:    maybeWrapNamespace(cfg, "scripts", cfg.Drive.ScriptsRootFolder, ScriptPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictOverwrite, // regenerable script output
		},
		// DestinationSoundEffect: licensed sound-effect audio.
		// Immutable versioned uploads.
		DestinationSoundEffect: {
			RootFolderID:   cfg.Drive.SoundEffectsFolder(),
			Namespace:      "sound_effects",
			PathBuilder:    maybeWrapNamespace(cfg, "sound_effects", cfg.Drive.SoundEffectsRootFolder, SoundEffectPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // licensed sound effect
		},
		// DestinationSoundEffectSidecar (PR-P12-SOUND-EFFECT-SIDECAR,
		// July 2026): metadata.json sidecar that co-locates with the
		// audio file. Shares the audio's root folder + namespace +
		// PathBuilder but carries ConflictOverwrite (regenerable
		// sidecar — the latest metadata.json wins) instead of
		// ConflictSkip (immutable audio). godlike/06 SSOT: one
		// canonical owner per fact — the sidecar's overwrite policy is
		// a separate concern from the audio's skip policy and lives
		// in the registry, not at the handler.
		DestinationSoundEffectSidecar: {
			RootFolderID:   cfg.Drive.SoundEffectsFolder(),
			Namespace:      "sound_effects",
			PathBuilder:    maybeWrapNamespace(cfg, "sound_effects", cfg.Drive.SoundEffectsRootFolder, SoundEffectPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictOverwrite, // latest metadata.json wins
		},
		// DestinationStock: licensed stock footage. Immutable
		// versioned uploads.
		DestinationStock: {
			RootFolderID:   cfg.Drive.StockFolder(),
			Namespace:      "stock",
			PathBuilder:    maybeWrapNamespace(cfg, "stock", cfg.Drive.StockRootFolder, StockPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // licensed stock asset
		},
		// DestinationVoiceover: voiceover audio. Immutable versioned
		// uploads — skip-or-create-version (hash-based), see
		// VoiceoverPath for the per-asset version suffix.
		DestinationVoiceover: {
			RootFolderID:   cfg.Drive.VoiceoverFolder(),
			Namespace:      "voiceovers",
			PathBuilder:    maybeWrapNamespace(cfg, "voiceovers", cfg.Drive.VoiceoverRootFolder, VoiceoverPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // P1: skip-or-create-version (hash-based)
		},
		// DestinationYouTubeAsset (PR-CLIPINGEST-PIPELINE step 9,
		// July 2026): canonical destination for the new YouTube asset
		// layout per user spec — `PipelineGen Assets/youtube/{channel_id}/
		// {video_id}/clips/{asset_id}/{asset_id}__master.mp4 +
		// __preview.mp4 + __manifest.json`. Shares the
		// ClipsRootFolder with DestinationYouTubeClip but emits
		// `youtube/{channel_id}/{video_id}/clips/{asset_id}` segments
		// via YouTubeAssetPath. The `youtube/` namespace is added by
		// maybeWrapNamespace so the new scheme nests under the same
		// root without spilling into the legacy `clips/` root.
		// godlike/06 SSOT: YouTubeAssetPath is the SOLE canonical
		// owner of the new scheme's path shape. ChannelID + Subject +
		// AssetID flow from PublishRequest; the builder is pure (no
		// side effects).
		DestinationYouTubeAsset: {
			RootFolderID:   cfg.Drive.ClipsFolder(),
			Namespace:      "youtube",
			PathBuilder:    maybeWrapNamespace(cfg, "youtube", cfg.Drive.ClipsRootFolder, YouTubeAssetPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // immutable uploaded master (per-asset canonical)
		},
		// DestinationYouTubeClip: legacy `clips/{group}/{video_id}`
		// scheme. Retained alongside DestinationYouTubeAsset per
		// godlike/06 additive-evolution principle — future cutover
		// (when all callers migrate to the new scheme) collapses the
		// two by retiring YouTubeClipPath; until then both builders
		// live side-by-side.
		DestinationYouTubeClip: {
			RootFolderID:   cfg.Drive.ClipsFolder(),
			Namespace:      "clips",
			PathBuilder:    maybeWrapNamespace(cfg, "clips", cfg.Drive.ClipsRootFolder, YouTubeClipPath),
			RequireSubpath: true,
			ConflictPolicy: ConflictSkip, // immutable uploaded clip
		},
	}
}
