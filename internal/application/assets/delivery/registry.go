package delivery

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
)

// PathBuilder computes the folder path segments for a given PublishRequest.
// Each segment becomes a nested Drive folder under the destination's root.
// The function MUST sanitise every segment via pathutil.SafeFolderName.
// Returning an empty slice is valid only when RequireSubpath is false.
type PathBuilder func(req PublishRequest) ([]string, error)

// DestinationPolicy defines how a DestinationKey resolves to a Drive path
// AND how to handle a filename collision in the resolved folder.
//
// The DestinationRegistry holds exactly one policy per DestinationKey.
// All fields are captured eagerly at construction time; the registry is
// immutable after creation. Per-destination ConflictPolicy is set here
// — NOT threaded through PublishRequest — so callers that omit the
// field cannot accidentally trigger uploads against the wrong policy.
//
// P1.1 (July 2026): the legacy zero = ConflictOverwrite default was
// unsafe; a caller forgetting to pick a policy would silently overwrite
// any existing Drive file under that name. The publisher now consults
// this field when req.ConflictPolicy == 0 (the "caller didn't pick"
// path) so the safety contract lives in the registry, not as a hidden
// zero-value trap. Explicit PublishRequest.ConflictPolicy always wins.
type DestinationPolicy struct {
	// RootFolderID returns the Drive folder ID that serves as the root
	// for this destination. Derived from config at construction time.
	RootFolderID string

	// PathBuilder computes nested folder segments under RootFolderID.
	PathBuilder PathBuilder

	// RequireSubpath, when true, rejects uploads that would land directly
	// in the root folder (i.e. when PathBuilder returns an empty slice).
	// This prevents accidental pollution of top-level Drive folders.
	RequireSubpath bool

	// Namespace is the canonical top-level directory name for this
	// destination when the unified media_root_folder is active (see
	// config.DriveConfig.IsUsingMediaRoot). When empty, the namespace
	// is not prepended — the destination either has its own dedicated
	// root folder or does not require namespace isolation.
	//
	// Canonical namespace values:
	//   clips, stock, artlist, images, voiceovers, books, scripts,
	//   sound_effects, documents, admin
	//
	// Per godlike/06 SSOT (one canonical owner per fact): the namespace
	// is assigned ONCE at registry construction and never mutated.
	Namespace string

	// ConflictPolicy is the registry-driven default for filename
	// collisions in the resolved Drive folder. The publisher applies
	// this when PublishRequest.ConflictPolicy is the zero value (the
	// "caller didn't pick" path). MUST be one of ConflictOverwrite /
	// ConflictSkip / ConflictRename — zero is NOT a valid default and
	// would silently fall back to ConflictOverwrite via the PutFile
	// seam.
	//
	// Semantics per destination (P1.1 mapping, July 2026):
	//   - YouTube clip / Artlist / Stock / Image / Voiceover /
	//     SoundEffect → ConflictSkip  (immutable / versioned assets,
	//     collisions are content-hash dupes that should not overwrite)
	//   - Book / Script / Document → ConflictOverwrite (regenerable
	//     outputs, latest version wins)
	//
	// Operator overrides (e.g. an explicit admin reupload that wants
	// ConflictOverwrite on a normally-Skip destination) MUST thread
	// PublishRequest.ConflictPolicy explicitly; the registry default
	// only applies when the caller left it at zero.
	ConflictPolicy ConflictPolicy
}

// DestinationRegistry is the single authority that maps a DestinationKey
// to a root folder and a path structure. Adding a new capability means
// adding one policy entry here — no endpoint-level Drive logic is permitted.
type DestinationRegistry struct {
	policies map[DestinationKey]DestinationPolicy
}

// NewDestinationRegistry builds the registry from application config.
// Every DestinationKey has exactly one policy. The root folder IDs and
// per-destination ConflictPolicy are captured eagerly (at construction
// time) so the registry is immutable after creation.
//
// Per-destination ConflictPolicy mapping (P1.1, July 2026) — see
// DestinationPolicy.ConflictPolicy for the rationale. Pure data:
//
//	Skip             → immutable / versioned asset (do not overwrite
//	                    silently when an existing Drive file under the
//	                    same name is found)
//	Overwrite        → regenerable artefact where the latest version
//	                    wins (caller can override per request, e.g. an
//	                    explicit admin reupload wants Overwrite on a
//	                    normally-Skip destination — PublishRequest is
//	                    the surface for that override)
func NewDestinationRegistry(cfg *config.Config) *DestinationRegistry {
	return &DestinationRegistry{
		policies: map[DestinationKey]DestinationPolicy{
			DestinationYouTubeClip: {
				RootFolderID:   cfg.Drive.ClipsFolder(),
				Namespace:      "clips",
				PathBuilder:    maybeWrapNamespace(cfg, "clips", cfg.Drive.ClipsRootFolder, YouTubeClipPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkip, // immutable uploaded clip
			},
			DestinationArtlist: {
				RootFolderID:   cfg.Drive.ArtlistFolder(),
				Namespace:      "artlist",
				PathBuilder:    maybeWrapNamespace(cfg, "artlist", cfg.Drive.ArtlistRootFolder, ArtlistPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkip, // curated artlist asset
			},
			DestinationStock: {
				RootFolderID:   cfg.Drive.StockFolder(),
				Namespace:      "stock",
				PathBuilder:    maybeWrapNamespace(cfg, "stock", cfg.Drive.StockRootFolder, StockPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkip, // licensed stock asset
			},
			DestinationImage: {
				RootFolderID:   cfg.Drive.ImagesFolder(),
				Namespace:      "images",
				PathBuilder:    maybeWrapNamespace(cfg, "images", cfg.Drive.ImagesRootFolder, ImagePath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkipByHash, // P1: skip when content hash matches
			},
			DestinationVoiceover: {
				RootFolderID:   cfg.Drive.VoiceoverFolder(),
				Namespace:      "voiceovers",
				PathBuilder:    maybeWrapNamespace(cfg, "voiceovers", cfg.Drive.VoiceoverRootFolder, VoiceoverPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkip, // P1: skip-or-create-version (hash-based)
			},
			DestinationBook: {
				RootFolderID:   cfg.Drive.BooksFolder(),
				Namespace:      "books",
				PathBuilder:    maybeWrapNamespace(cfg, "books", cfg.Drive.BooksRootFolder, BookPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictOverwrite, // regenerable summary
			},
			DestinationScript: {
				RootFolderID:   cfg.Drive.ScriptsFolder(),
				Namespace:      "scripts",
				PathBuilder:    maybeWrapNamespace(cfg, "scripts", cfg.Drive.ScriptsRootFolder, ScriptPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictOverwrite, // regenerable script output
			},
			DestinationSoundEffect: {
				RootFolderID:   cfg.Drive.SoundEffectsFolder(),
				Namespace:      "sound_effects",
				PathBuilder:    maybeWrapNamespace(cfg, "sound_effects", cfg.Drive.SoundEffectsRootFolder, SoundEffectPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictSkip, // licensed sound effect
			},
			// PR-P12-SOUND-EFFECT-SIDECAR (July 2026): the canonical
			// destination for sound-effect metadata.json sidecars. Shares
			// the audio's root folder + namespace + PathBuilder so the
			// sidecar co-locates with the .mp3 in the same
			// <root>/<name>/ folder, but carries ConflictOverwrite
			// (regenerable sidecar — the latest metadata.json wins)
			// instead of ConflictSkip (immutable audio). godlike/06 SSOT:
			// one canonical owner per fact — the sidecar's overwrite
			// policy is a separate concern from the audio's skip
			// policy and lives in the registry, not at the handler.
			DestinationSoundEffectSidecar: {
				RootFolderID:   cfg.Drive.SoundEffectsFolder(),
				Namespace:      "sound_effects",
				PathBuilder:    maybeWrapNamespace(cfg, "sound_effects", cfg.Drive.SoundEffectsRootFolder, SoundEffectPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictOverwrite, // latest metadata.json wins
			},
			DestinationDocument: {
				RootFolderID:   cfg.Drive.DocumentsFolder(),
				Namespace:      "documents",
				PathBuilder:    maybeWrapNamespace(cfg, "documents", cfg.Drive.ScriptsRootFolder, DocumentPath),
				RequireSubpath: true,
				ConflictPolicy: ConflictOverwrite, // latest PDF/DOCX wins
			},
			DestinationAdmin: {
				RootFolderID:   cfg.Drive.RootFolder(),
				Namespace:      "admin",
				PathBuilder:    maybeWrapNamespace(cfg, "admin", "", AdminPath),
				RequireSubpath: false,
				ConflictPolicy: ConflictOverwrite, // P1: admin CLI always overwrites
			},
		},
	}
}

// maybeWrapNamespace wraps the PathBuilder with the given namespace when
// the destination is effectively using the unified media_root_folder. When
// the destination has its own dedicated root folder, the inner PathBuilder
// is returned unchanged — no double-namespace risk.
//
// Use specificRoot="" for destinations that have no dedicated root field
// (e.g. DestinationAdmin always resolves to MediaRootFolder).
func maybeWrapNamespace(cfg *config.Config, namespace, specificRoot string, inner PathBuilder) PathBuilder {
	if cfg.Drive.IsUsingMediaRoot(specificRoot) {
		return withNamespace(namespace, inner)
	}
	return inner
}

// Has reports whether the registry contains a policy for the given key.
func (r *DestinationRegistry) Has(key DestinationKey) bool {
	_, ok := r.policies[key]
	return ok
}

// Resolve returns the policy for the given key, or an error if the key
// is not registered. Callers MUST check Has() first when iterating over
// a known set of keys (e.g. in tests).
func (r *DestinationRegistry) Resolve(key DestinationKey) (DestinationPolicy, error) {
	p, ok := r.policies[key]
	if !ok {
		return DestinationPolicy{}, fmt.Errorf("delivery: unknown destination key %q", key)
	}
	return p, nil
}

// Keys returns all registered destination keys. Useful for diagnostics
// and completeness tests.
func (r *DestinationRegistry) Keys() []DestinationKey {
	keys := make([]DestinationKey, 0, len(r.policies))
	for k := range r.policies {
		keys = append(keys, k)
	}
	return keys
}

// ── Path builders ──────────────────────────────────────────────────────
//
// Each builder returns []string segments that the Publisher will pass
// to FolderManager.EnsurePath(rootID, segments...). Every segment is
// sanitised via pathutil.SafeFolderName to prevent path traversal,
// OS-unsafe characters, and empty folder names.

// withNamespace wraps a PathBuilder to prepend a canonical namespace
// segment. Used when the destination falls back to the unified
// media_root_folder (see config.DriveConfig.IsUsingMediaRoot). The
// namespace ensures the unified root stays organized with canonical
// subdirectories (clips, stock, artlist, etc.) instead of having every
// destination write directly into the root.
func withNamespace(namespace string, inner PathBuilder) PathBuilder {
	return func(req PublishRequest) ([]string, error) {
		segs, err := inner(req)
		if err != nil {
			return nil, err
		}
		return append([]string{namespace}, segs...), nil
	}
}

// YouTubeClipPath builds the path for YouTube clips:
//
//	clips/{group}/{video_id}
//
// Group fallback chain (July 2026 — PR-YT-PATH-FALLBACK):
//  1. req.Group (caller-supplied channel/category name)
//  2. req.Category (semantic-location metadata, e.g. "Boxe")
//  3. "youtube_uncategorized" (technical fallback — prevents
//     RETRY_WAIT loops when callers lack semantic metadata.
//     The handler SHOULD validate upfront with 400 Bad Request
//     for new POSTs; this fallback is for legacy/stuck jobs.)
//
// godlike/07 NO-FAKE-AVAILABILITY: a zero-value Group no longer
// fails-closed (was: "group is required"). An empty group now
// routes to "youtube_uncategorized" so existing jobs with
// incomplete semantic metadata don't loop forever. New callers
// SHOULD be validated at the handler boundary.
func YouTubeClipPath(req PublishRequest) ([]string, error) {
	group := firstNonEmpty(
		strings.TrimSpace(req.Group),
		strings.TrimSpace(req.Category),
		"youtube_uncategorized",
	)
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return nil, fmt.Errorf("delivery: YouTubeClipPath: subject (video ID) is required")
	}
	return []string{
		pathutil.SafeFolderName(group),
		pathutil.SafeFolderName(subject),
	}, nil
}

// firstNonEmpty returns the first non-empty string from the provided
// candidates. If all are empty, returns "".
func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// ArtlistPath builds the path for Artlist assets:
//
//	artlist/{term}/{asset_id}
func ArtlistPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	subject := strings.TrimSpace(req.Subject)
	if group == "" {
		return nil, fmt.Errorf("delivery: ArtlistPath: group (search term) is required")
	}
	seg := []string{pathutil.SafeFolderName(group)}
	if subject != "" {
		seg = append(seg, pathutil.SafeFolderName(subject))
	}
	return seg, nil
}

// StockPath builds the path for stock footage (DoD item 4, July 2026):
//
//	stock/{category}/{subject}
//
// Category is read from req.Category (primary) or req.Group (legacy fallback).
// Subject is required. Provider is retained as an optional third segment for
// legacy callers that still want an extra namespace layer.
func StockPath(req PublishRequest) ([]string, error) {
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = strings.TrimSpace(req.Group) // backward-compat: legacy callers use Group
	}
	provider := strings.TrimSpace(req.Provider)
	subject := strings.TrimSpace(req.Subject)
	if category == "" {
		return nil, fmt.Errorf("delivery: StockPath: category is required (set req.Category or req.Group)")
	}
	if subject == "" {
		return nil, fmt.Errorf("delivery: StockPath: subject is required")
	}
	segs := []string{
		pathutil.SafeFolderName(category),
	}
	if provider != "" {
		segs = append(segs, pathutil.SafeFolderName(provider))
	}
	segs = append(segs, pathutil.SafeFolderName(subject))
	return segs, nil
}

// ImagePath builds the path for generated images:
//
//	images/{style}/{subject}
func ImagePath(req PublishRequest) ([]string, error) {
	style := strings.TrimSpace(req.Style)
	subject := strings.TrimSpace(req.Subject)
	if style == "" {
		return nil, fmt.Errorf("delivery: ImagePath: style is required")
	}
	if subject == "" {
		return nil, fmt.Errorf("delivery: ImagePath: subject is required")
	}
	return []string{
		pathutil.SafeFolderName(style),
		pathutil.SafeFolderName(subject),
	}, nil
}

// VoiceoverPath builds the path for voiceover audio:
//
//	voiceovers/{project}/{language}
func VoiceoverPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	language := strings.TrimSpace(req.Language)
	if project == "" {
		return nil, fmt.Errorf("delivery: VoiceoverPath: project_id is required")
	}
	if language == "" {
		return nil, fmt.Errorf("delivery: VoiceoverPath: language is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
		pathutil.SafeFolderName(language),
	}, nil
}

// BookPath builds the path for book processing outputs:
//
//	books/{project}
func BookPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	if project == "" {
		return nil, fmt.Errorf("delivery: BookPath: project_id is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
	}, nil
}

// ScriptPath builds the path for generated scripts/documents:
//
//	scripts/{project}/{language}
func ScriptPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	language := strings.TrimSpace(req.Language)
	if project == "" {
		return nil, fmt.Errorf("delivery: ScriptPath: project_id is required")
	}
	if language == "" {
		return nil, fmt.Errorf("delivery: ScriptPath: language is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
		pathutil.SafeFolderName(language),
	}, nil
}

// SoundEffectPath builds the path for generated sound effects:
//
//	sound-effects/{category}
func SoundEffectPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	if group == "" {
		return nil, fmt.Errorf("delivery: SoundEffectPath: group (category) is required")
	}
	return []string{
		pathutil.SafeFolderName(group),
	}, nil
}

// DocumentPath builds the path for generated documents (PDFs, DOCX):
//
//	documents/{asset_id_slug}
//
// AssetID is used as the subfolder to group document artifacts.
func DocumentPath(req PublishRequest) ([]string, error) {
	assetID := strings.TrimSpace(req.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("delivery: DocumentPath: asset_id is required")
	}
	return []string{
		pathutil.SafeFolderName(assetID),
	}, nil
}

// AdminPath (P1, July 2026) builds the path for admin CLI uploads.
// Returns an empty slice — admin uploads land directly in the root
// folder. RequireSubpath is false for this destination.
func AdminPath(_ PublishRequest) ([]string, error) {
	return nil, nil
}
