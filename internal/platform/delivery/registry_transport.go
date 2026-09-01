// registry_transport.go owns the TRANSPORT concern of the destination
// registry: how to compute the destination folder given a PublishRequest.
//
// This file is the single canonical home of every PathBuilder
// implementation in the package, plus the namespace-wrapping helpers
// (withNamespace, maybeWrapNamespace) that the lifecycle policy table
// (registry_lifecycle.go) stitches together via PathBuilder closures
// captured at construction time.
//
// Companion files in this package:
//
//	registry.go            — type surface + lookup methods (Has/Resolve/Keys)
//	registry_lifecycle.go  — the per-DestinationKey policy table
//	registry_transport.go  — the PathBuilder implementations (THIS FILE)
//
// godlike/06 SSOT (one canonical owner per fact, July 2026): each
// PathBuilder below is the SOLE canonical owner of its path shape.
// Adding a new destination means defining a builder here AND a mapping
// entry in registry_lifecycle.go, AND a DestinationKey constant in
// types.go. The three together form a closed triple — no endpoint
// outside this package may import or re-derive these paths.
//
// ── Path builders ──────────────────────────────────────────────────────
//
// Each builder returns []string segments that the Publisher will pass
// to FolderManager.EnsurePath(rootID, segments...). Every segment is
// sanitised via pathutil.SafeFolderName to prevent path traversal,
// OS-unsafe characters, and empty folder names.
package delivery

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// maybeWrapNamespace wraps the PathBuilder with the given namespace when
// the destination is effectively using the unified media_root_folder. When
// the destination has its own dedicated root folder, the inner PathBuilder
// is returned unchanged — no double-namespace risk.
//
// Use specificRoot="" for destinations that have no dedicated root field
// (e.g. DestinationAdmin always resolves to MediaRootFolder).
//
// godlike/06 SSOT: this helper is the single canonical owner of "should
// this destination's PathBuilder emit a leading namespace segment". Any
// future caller that wants to opt out of the wrapping MUST add a new
// DestinationKey + a non-wrapped inline builder in
// buildDestinationPolicies — must not bypass this seam.
func maybeWrapNamespace(cfg *config.Config, namespace, specificRoot string, inner PathBuilder) PathBuilder {
	if cfg.Drive.IsUsingMediaRoot(specificRoot) {
		return withNamespace(namespace, inner)
	}
	return inner
}

// withNamespace wraps a PathBuilder to prepend a canonical namespace
// segment. Used when the destination falls back to the unified
// media_root_folder (see config.DriveConfig.IsUsingMediaRoot). The
// namespace ensures the unified root stays organized with canonical
// subdirectories (clips, stock, artlist, etc.) instead of having every
// destination write directly into the root.
func withNamespace(namespace string, inner PathBuilder) PathBuilder {
	return func(req PublishRequest) ([]string, error) {
		if strings.TrimSpace(req.DestinationFolderID) != "" {
			return nil, nil
		}
		segs, err := inner(req)
		if err != nil {
			return nil, err
		}
		return append([]string{namespace}, segs...), nil
	}
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

// ── YouTube paths ──────────────────────────────────────────────────────

// YouTubeAssetPath (PR-CLIPINGEST-PIPELINE step 9, July 2026) builds
// the path for the new YouTube asset layout per user spec:
//
//	youtube/{channel_id}/{video_id}/clips/{asset_id}
//
// Required PublishRequest fields:
//   - ChannelID (YouTube channel_id; the user-spec segment 2)
//   - Subject   (YouTube video_id; the user-spec segment 3)
//   - AssetID   (media_assets.id; the user-spec segment 5)
//
// Returns ErrYouTubeAssetPathMissingField for any missing required
// field (godlike/07 typed-error contract — callers can probe via
// errors.Is). The asset_id is the per-asset folder, not a leaf file
// — the per-asset files (`{asset_id}__master.mp4` + `__preview.mp4`
// + `__manifest.json`) are uploaded into the resolved folder by the
// caller (the canonical processor.processRenditions function names
// them; the publisher only resolves the folder).
//
// godlike/06 SSOT: YouTubeAssetPath is the SOLE canonical owner of
// the new scheme's path shape. Distinct from YouTubeClipPath (the
// legacy `clips/{group}/{video_id}` scheme) — the two coexist per
// godlike/06 additive-evolution principle. Future cutover (when all
// callers migrate to the new scheme) collapses the two by retiring
// YouTubeClipPath; until then both builders live side-by-side.
func YouTubeAssetPath(req PublishRequest) ([]string, error) {
	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" {
		return nil, fmt.Errorf("%w: channel_id is required for youtube_asset destination", ErrYouTubeAssetPathMissingField)
	}
	videoID := strings.TrimSpace(req.Subject)
	if videoID == "" {
		return nil, fmt.Errorf("%w: subject (video_id) is required for youtube_asset destination", ErrYouTubeAssetPathMissingField)
	}
	assetID := strings.TrimSpace(req.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("%w: asset_id is required for youtube_asset destination", ErrYouTubeAssetPathMissingField)
	}
	return []string{
		pathutil.SafeFolderName(channelID),
		pathutil.SafeFolderName(videoID),
		"clips",
		pathutil.SafeFolderName(assetID),
	}, nil
}

// ErrYouTubeAssetPathMissingField (PR-CLIPINGEST-PIPELINE step 9,
// July 2026) is the typed sentinel raised by YouTubeAssetPath when a
// required field (ChannelID / Subject / AssetID) is empty. Callers
// probe via errors.Is to surface a per-field hint to the operator.
// Pre-step-9 a missing field would have produced a downstream
// `pathutil.SafeFolderName("")` panic — godlike/07 fail-fast moves
// the check to the seam.
var ErrYouTubeAssetPathMissingField = errors.New(
	"delivery: YouTubeAssetPath: required field missing",
)

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
	if strings.TrimSpace(req.DestinationFolderID) != "" {
		// DestinationFolderID is already the resolved leaf folder. Do not
		// append a group, video ID, or namespace beneath it.
		return nil, nil
	}
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

// ── Domain paths ───────────────────────────────────────────────────────

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

// ── Special / sidecar paths ────────────────────────────────────────────

// AdminPath (P1, July 2026) builds the path for admin CLI uploads.
// Returns an empty slice — admin uploads land directly in the root
// folder. RequireSubpath is false for this destination.
func AdminPath(_ PublishRequest) ([]string, error) {
	return nil, nil
}

// ClipMetadataPath (P0-#1 atomic-RMW cutover, July 2026) builds the
// path for the per-folder metadata.json sidecar. Returns an empty slice
// to signal that the Publisher should use DestinationFolderID verbatim
// (the caller is updating an existing per-folder sidecar whose Drive
// folder is already known — there's no canonical sub-path to nest
// under). The destination is registered with RequireSubpath=false so
// the empty-slice return does NOT trigger the publisher's
// "no-subpath" rejection.
//
// godlike/06 SSOT: this builder is the single canonical owner of the
// "use DestinationFolderID verbatim" decision for clip metadata. Any future
// caller that wants a per-clip subfolder (e.g. a future
// "per-clip metadata.json" requirement) would either change this
// builder to return a non-empty slice OR introduce a new
// DestinationKey rather than mutating the DestinationFolderID semantics here.
func ClipMetadataPath(_ PublishRequest) ([]string, error) {
	return nil, nil
}

// RenderedClipPath keeps a caller-resolved rendered-clip folder as the final
// destination. The outbox publisher supplies DestinationFolderID explicitly;
// no additional guessed hierarchy is allowed here.
func RenderedClipPath(_ PublishRequest) ([]string, error) {
	return nil, nil
}
