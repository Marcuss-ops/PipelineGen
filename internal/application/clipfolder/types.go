// Package clipfolder — canonical alias resolver for Drive clip folders.
//
// godlike/06 SSOT (one canonical owner per fact): this package is the
// SOLE owner of the typed-tag surface (ClipFolderRef), the alias table
// loader (FolderAliasResolver), and the typed sentinel
// (ErrUnknownFolderAlias). No other layer is permitted to instantiate
// the same conditional logic — every caller that needs to translate a
// user-supplied folder name into a canonical reference MUST go through
// FolderAliasResolver.Resolve(input).
//
// Drift policy:
//
//   - The resolver is constructed once with config/folder_aliases.yaml
//     and is immutable after (concurrent-reads-safe, no locks).
//   - The resolver does NOT create Drive folders, does NOT mutate the
//     database, does NOT publish. Its single responsibility is to map
//     a user string to a typed ClipFolderRef.
//   - When the user input maps to no entry, the resolver returns
//     ErrUnknownFolderAlias — godlike/07 NO-FAKE-AVAILABILITY: never
//     silently fallback to a "general" / "uncategorized" /
//     "youtube_uncategorized" surface.
package clipfolder

import "errors"

// ClipFolderRef is the canonical typed-tag for a resolved Drive clip
// folder. Use this struct everywhere a caller needs the canonical
// trio of (drive folder id, canonical display path, lowercase
// routing key). DO NOT pass three separate strings around — the
// struct makes "forgot to update one of the three" impossible to
// express in code at the type level.
//
// Fields:
//
//	ID, Path, NormalizedGroup — exported, plain strings.
//	No JSON tags — this is a pure Go domain type. Callers that marshal
//	it to JSON for HTTP payloads handle the wire shape; the domain
//	struct stays yaml/gorm/etc.-agnostic.
type ClipFolderRef struct {
	// ID is the resolved Drive folder ID. The YAML entry may supply
	// this directly when the operator knows the exact folder ID
	// (rare; initial seed leaves this empty to honour the
	// "Niente nuove cartelle Drive" constraint). When empty,
	// callers MUST resolve it via:
	//
	//   delivery.Publisher.ResolveFolder(ctx, delivery.PublishRequest{
	//       Destination: delivery.DestinationYouTubeClip,
	//       Group:       ref.Path,
	//   })
	//
	// The existing YouTubeClipPath builder
	// (internal/platform/delivery/registry.go) emits the
	// `[group, subject]` segments, so the resolved folder ID is
	// what Drive returns + the path-builder segments. No new folder
	// creation, no new DB write.
	//
	// Why not always-resolve: the resolver is a pure lookup service
	// and must not couple to the Drive SDK at construction time.
	// Adding `ID` as a runtime-resolved field would force every
	// caller to either re-resolve or accept stale cache.
	ID string

	// Path is the canonical, human-readable Drive folder name
	// (e.g. "Boxe", "HipHop"). What operators see in Drive and the
	// first segment of the YouTubeClipPath hierarchy
	// (`clips/{Path}/{video_id}`).
	Path string

	// NormalizedGroup is the lowercase, kebab-/spaces-safe routing
	// key (e.g. "boxe", "hiphop"). The canonical surface for:
	//
	//   - media_assets.metadata_json.normalized_group
	//   - Qdrant filter payload
	//   - IndexDocument.NormalizedGroup field
	//
	// Forward-pointer: future waves that wire the Qdrant
	// folder-scoped search filter consume this value verbatim — no
	// normalisation happens downstream.
	NormalizedGroup string
}

// ErrUnknownFolderAlias is the typed-sentinel failure for an
// unmapped user input. Callers probe via errors.Is.
//
// godlike/07 NO-FAKE-AVAILABILITY: this sentinel is the SOLE failure
// mode for the resolver. Callers MUST handle it explicitly — silent
// fallback to "general" or "uncategorized" is forbidden at this
// seam (any future fallback would need a forward-prevention gate
// like the existing archcheck gates).
var ErrUnknownFolderAlias = errors.New(
	"clipfolder: unknown folder alias (no entry in config/folder_aliases.yaml)",
)
