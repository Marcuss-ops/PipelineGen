// Package voiceover — destination_resolver.go (PR-VO-AUDIT-P02-P03, June 2026).
//
// Canonical destination resolver for voiceover. Replaces the inline
// fallback in `process.go::Service.Generate` and the kind-ignoring
// implementation in `metadata.go::Service.resolveDestination` with a
// single function that:
//
//  1. Categorises the caller's intent via `DestinationRequest.Kind`
//     (typed string enum; previous code accepted any string).
//  2. Honours the explicit > group > auto precedence mandated by
//     the voiceover audit (P0.2+P0.3, June 2026). No fallback
//     decision lives outside this function so callers (legacy
//     `Service.resolveDestination`, future use-case path in
//     Blocco 4 cutover, and direct internal callers) route
//     identically.
//
// `Service.resolveDestination` (declared in metadata.go) is
// implemented as a thin wrapper that reads `cfg.Drive.VoiceoverFolder()`
// (cf. `DriveConfig.VoiceoverFolder()`, `internal/platform/config/drive.go`)
// nil-safe and delegates to this function. The legacy identifier path
// + the new use-case path collapse to a single routing decision
// surface here.
//
// Sentinel errors carry the canonical wire-string (`missing_folder_id`)
// so operational log-markers / status codes / Prometheus labels stay
// unaffected by the refactor.
package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// DestinationKind is the canonical routing-strategy selector for the
// voiceover destination surface. The audit (P0.3) requires each kind
// to be respected:
type DestinationKind string

const (
	// KindExplicit — caller-supplied FolderID is used verbatim and the
	// asset.Resolver is NOT consulted. An empty FolderID is a hard
	// error (ErrExplicitKindRequiresFolderID) so a silent drop-down to
	// auto does not occur. The handler layer validates fail-closed
	// upstream; this resolver re-asserts for internal callers that
	// bypass the handler (legacy job payloads from before validation
	// was widened).
	KindExplicit DestinationKind = "explicit"

	// KindGroup — asset.Resolver maps Group → FolderID. Empty Group
	// is a hard error (ErrGroupKindRequiresGroup). ResolvedDestination
	// carries FolderID from the resolver result + StyleGroup mirrored
	// from the input (the resolver is a folder-mapping layer and does
	// not own StyleGroup routing).
	KindGroup DestinationKind = "group"

	// KindAuto — legacy auto-detect path. If Group is set the resolver
	// is consulted (precedence over FolderID for back-compat with the
	// pre-PR-VO-C1 callers that expected Group to drive routing). If
	// FolderID is set it is used directly. Else the config-level
	// voiceover folder is the final fallback. Empty Kind literal
	// ("") maps to KindAuto so pre-PR-VO-C1 callers continue to be
	// handled identically.
	KindAuto DestinationKind = "auto"
)

// Sentinel errors returned from ResolveVoiceoverDestination. The
// `destinationStage` failure path surfaces the `missing_folder_id`
// status code so callers can correlate via BatchItem.Status; the
// wrapped message preserves the same surface for log lines / metrics.
var (
	// ErrMissingFolder — every Kind/Field combination has been
	// consulted and the configured default voiceover folder is also
	// empty. Wrapped at the ResolveVoiceoverDestination call site as
	// "missing_folder_id: …" to preserve the legacy log-marker
	// surface.
	ErrMissingFolder = fmt.Errorf("missing_folder_id: no destination resolved")

	// ErrExplicitKindRequiresFolderID — Kind=KindExplicit + empty
	// FolderID. Validated at the API layer (the handler fails fast
	// with 400) but the resolver reasserts for internal callers so a
	// misrouted legacy payload cannot bypass the gate.
	ErrExplicitKindRequiresFolderID = fmt.Errorf("destination.kind=explicit requires non-empty folder_id")

	// ErrGroupKindRequiresGroup — Kind=KindGroup + empty Group. Same
	// rationale as ErrExplicitKindRequiresFolderID.
	ErrGroupKindRequiresGroup = fmt.Errorf("destination.kind=group requires non-empty group")

	// ErrInvalidDestinationKind — Kind is non-empty and not one of
	// explicit|group|auto. Defensive — catches API drift before the
	// resolver silently degrades to a wrong routing decision.
	ErrInvalidDestinationKind = fmt.Errorf("destination.kind: unknown value (must be explicit|group|auto)")
)

// ResolveVoiceoverDestination is the canonical destination resolver
// for voiceover. Single source of truth for the explicit > group >
// auto precedence mandated by the audit (P0.2 + P0.3, June 2026).
//
// Nil `dest` is treated as KindAuto with no caller-supplied info.
// This preserves the legacy "no destination → use
// cfg.Drive.VoiceoverFolder()" behaviour that used to live inline in
// `Service.Generate` (process.go:73-77); the inline branch is now
// removed and the fallback is centralised here.
//
// Precedence (audit P0.3; nil dest behaves like KindAuto without
// caller fields, falling straight through to defaultFolderID):
//
//  1. Kind=KindExplicit + non-empty FolderID → use verbatim (no
//     resolver call; resolver MAY be nil).
//  2. Kind=KindGroup + non-empty Group → call asset.Resolver with
//     Source="voiceover" + Group + StyleGroup; mirror StyleGroup
//     verbatim on the ResolvedDestination.
//  3. Kind=KindAuto (or "") + Group → call resolver (legacy).
//  4. Kind=KindAuto (or "") + FolderID → use verbatim (legacy).
//  5. Kind=KindAuto (or "") + defaultFolderID → use it.
//  6. Otherwise → ErrMissingFolder.
//
// This function does NOT validate subfolder_name or any other path-
// traversal surface — that contract is owned by
// `DestinationRequest.Validate` (called earlier in `GenerateBatch`
// at `stages.go::GenerateBatch` gate). The resolver is exclusively
// responsible for the routing decision surface.
func ResolveVoiceoverDestination(
	ctx context.Context,
	dest *DestinationRequest,
	resolver asset.Resolver,
	defaultFolderID string,
) (*ResolvedDestination, error) {
	// Nil dest → treat as KindAuto with no caller-supplied info.
	// Falls straight through to defaultFolderID; surfaces
	// ErrMissingFolder when no default folder is configured (i.e. the
	// canonical missing_folder_id short-circuit from audit P0.2).
	if dest == nil {
		if defaultFolderID == "" {
			return nil, ErrMissingFolder
		}
		return &ResolvedDestination{FolderID: defaultFolderID}, nil
	}

	// direct assembles a ResolvedDestination from caller-supplied
	// fields without consulting the asset.Resolver. Used by both
	// Kind=Explicit and Kind=Auto+FolderID-default branches.
	direct := func(folderID, folderPath string) (*ResolvedDestination, error) {
		return &ResolvedDestination{
			Group:         dest.Group,
			FolderID:      folderID,
			FolderPath:    folderPath,
			SubfolderName: dest.SubfolderName,
			StyleGroup:    dest.StyleGroup,
		}, nil
	}

	// groupResolve calls the asset.Resolver with the canonical
	// voiceover Source marker + forwarded Group + mirrored StyleGroup.
	// Mirroring is the canonical PR-VO-B2 behaviour: the resolver is
	// a folder-mapping layer and does NOT echo StyleGroup back.
	groupResolve := func() (*ResolvedDestination, error) {
		if resolver == nil {
			return nil, fmt.Errorf("destination.resolver: nil asset.Resolver (composition root did not wire it)")
		}
		result, err := resolver.Resolve(ctx, &asset.ResolveRequest{
			Source: "voiceover",
			Group:  dest.Group,
			// PR-VO-TYPED-PRIMITIVES (July 2026): asset.ResolveRequest.StyleGroup
			// is raw string; convert at the typed→string boundary.
			StyleGroup: string(dest.StyleGroup),
		})
		if err != nil {
			return nil, fmt.Errorf("destination.resolver: %w", err)
		}
		if result == nil {
			// Defensive: a misbehaving resolver might return (nil, nil).
			// Fallback to an empty result so the rest of the merge
			// proceeds with zero-valued folder fields.
			result = &asset.ResolveResult{}
		}
		return &ResolvedDestination{
			Group:         dest.Group,
			FolderID:      result.FolderID,
			FolderPath:    result.FolderPath,
			DriveLink:     result.DriveLink,
			SubfolderName: dest.SubfolderName,
			// PR-VO-TYPED-PRIMITIVES (July 2026): StyleGroup field is
			// the typed envelope; explicit string() conversion at the
			// resolver→dto boundary (dto is also typed but explicit
			// conversion is godlike/07 safer for future field-type
			// changes). MIRROR verbatim (NOT from resolver result).
			StyleGroup: dest.StyleGroup,
		}, nil
	}

	switch DestinationKind(dest.Kind) {
	case KindExplicit:
		if dest.FolderID == "" {
			return nil, ErrExplicitKindRequiresFolderID
		}
		return direct(dest.FolderID, dest.FolderPath)

	case KindGroup:
		if dest.Group == "" {
			return nil, ErrGroupKindRequiresGroup
		}
		return groupResolve()

	case KindAuto, "":
		// Legacy auto-detect. Respect caller-supplied Group/FolderID
		// before falling back to the configured default voiceover
		// folder. Group has precedence over FolderID for back-compat
		// with the pre-PR-VO-C1 callers that built DestinationRequest
		// without setting Kind.
		if dest.Group != "" {
			return groupResolve()
		}
		if dest.FolderID != "" {
			return direct(dest.FolderID, dest.FolderPath)
		}
		if defaultFolderID != "" {
			return direct(defaultFolderID, "")
		}
		return nil, ErrMissingFolder

	default:
		return nil, ErrInvalidDestinationKind
	}
}
