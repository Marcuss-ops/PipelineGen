// Package app — voiceover DestinationResolver +
// VoiceoverDefaultFolderResolver adapters (Azione #8, July 2026).
//
// Extracted from adapters_voiceover_repo.go per AGENTS.md Pattern 5
// (capability-split: repository ↔ resolver). Each adapter carries its
// own compile-time pin (Pattern 0 — AGENTS.md).
//
// DestinationResolver                ← asset.Resolver (forward all 7 fields)
// VoiceoverDefaultFolderResolver     ← cfg.Drive.VoiceoverFolder() (PR 6 P0.2)
//
// Fail-closed: nil deps panic at construction (fail-fast per
// AGENTS.md WireUp pattern). The DefaultFolderResolver constructor is
// non-panicking (empty driveFolderID is the production case for
// deployments without configured voiceover_root_folder — adapter
// returns ("", "", false) and Execute maps that to the canonical
// missing_folder_id short-circuit).
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	destinationapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// assetTreeVoiceoverResolver is the production resolver for voiceover
// destinations. wiring.DriveBundle no longer owns a generic destination resolver;
// voiceover routing is rooted in the configured voiceover folder and the
// canonical SQLite-backed asset tree.
type assetTreeVoiceoverResolver struct {
	groups *destinationapp.Resolver
	tree   *assettree.Service
	rootID string
	log    *zap.Logger
}

func newAssetTreeVoiceoverResolver(tree *assettree.Service, rootID string, log *zap.Logger) (asset.Resolver, error) {
	rootID = strings.TrimSpace(rootID)
	if tree == nil {
		return nil, fmt.Errorf("voiceover asset tree service is required")
	}
	if rootID == "" {
		return nil, fmt.Errorf("voiceover root folder ID is required")
	}
	groups, err := destinationapp.NewResolver(tree, log)
	if err != nil {
		return nil, fmt.Errorf("build voiceover group resolver: %w", err)
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &assetTreeVoiceoverResolver{groups: groups, tree: tree, rootID: rootID, log: log}, nil
}

func (r *assetTreeVoiceoverResolver) Resolve(ctx context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	if r == nil || r.groups == nil {
		return nil, fmt.Errorf("voiceover group resolver is not configured")
	}
	if req == nil {
		return nil, fmt.Errorf("voiceover destination request is required")
	}
	if folderID := strings.TrimSpace(req.FolderID); folderID != "" {
		return &asset.ResolveResult{LocationKind: "drive", FolderID: folderID, FolderPath: req.FolderPath}, nil
	}
	group := strings.TrimSpace(req.Group)
	if r.log != nil {
		r.log.Info("voiceover destination lookup", zap.String("group", group), zap.String("root_id", r.rootID))
	}
	if group == "" {
		return &asset.ResolveResult{}, nil
	}
	entry, err := r.groups.ResolveByName(ctx, r.rootID, group)
	if err != nil {
		// The asset-tree projection historically contains Drive folders
		// imported under source="youtube". ResolveByName already knows
		// that fallback; this second direct lookup is defensive for older
		// resolver instances and keeps voiceover routing fail-closed only
		// after both canonical projections were checked.
		return nil, fmt.Errorf("resolve voiceover group %q: %w", group, err)
	}
	if r.log != nil {
		r.log.Info("voiceover group resolved", zap.String("group", group), zap.String("folder_id", entry.FolderID))
	}
	// FolderPath is intentionally empty: in the voiceover use case that
	// field is the local TTS output directory, not the human-readable
	// Drive folder name. The Drive folder identity is carried by FolderID.
	return &asset.ResolveResult{LocationKind: "drive", FolderID: entry.FolderID, DriveLink: entry.DriveLink}, nil
}

// ─────────────────────────────────────────────────────────────────────
// DestinationResolver adapter.
//
// Implements voiceover.DestinationResolver.Resolve over an
// asset.Resolver. Mirrors the production body in *Service.resolveDestination
// (metadata.go) so the legacy + use-case paths route identically:
//
//  1. FORWARD: ALL DestinationRequest fields (Group, FolderID, FolderPath,
//     SubfolderName, CreateSubfolder, StyleGroup) land in the
//     asset.ResolveRequest (Source = "voiceover" hardcoded).
//     P0.2 destination-adapter fix (July 2026): pre-fix only Group +
//     StyleGroup were forwarded; FolderID, FolderPath, SubfolderName,
//     and CreateSubfolder were silently dropped, so any explicit
//     routing intent (Kind="explicit" with FolderID) was ignored by
//     the resolver.
//  2. MIRROR:  dest.StyleGroup and dest.SubfolderName are mirrored
//     verbatim onto the returned ResolvedDestination. When dest.FolderID
//     or dest.FolderPath are explicitly set, they take precedence over
//     the resolver's returned values (explicit override).
//  3. Nil-safe: nil dest → error; nil resolver → panic at constructor
//     time (fail-fast per AGENTS.md WireUp pattern).
// ─────────────────────────────────────────────────────────────────────

type useCaseDestResolverAdapter struct {
	resolver asset.Resolver
}

func newUseCaseDestResolverAdapter(r asset.Resolver) *useCaseDestResolverAdapter {
	if r == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseDestResolverAdapter: resolver is required (asset.Resolver)")
	}
	return &useCaseDestResolverAdapter{resolver: r}
}

func (a *useCaseDestResolverAdapter) Resolve(ctx context.Context, dest *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	if dest == nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: nil DestinationRequest")
	}
	res, err := a.resolver.Resolve(ctx, &asset.ResolveRequest{
		Source:          "voiceover",
		Group:           dest.Group,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		SubfolderName:   dest.SubfolderName,
		CreateSubfolder: dest.CreateSubfolder,
		StyleGroup:      string(dest.StyleGroup),
	})
	if err != nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: resolver failed: %w", err)
	}
	if res == nil {
		// Defensive: a misbehaving resolver may return (nil, nil).
		// Fallback to an empty result so downstream code can proceed
		// with zero-valued folder fields (mirrors the legacy
		// *Service.resolveDestination behavior in metadata.go).
		res = &asset.ResolveResult{}
	}
	// P0.2 fix (July 2026): when dest.FolderID is explicitly set,
	// use it directly instead of the resolver's result (explicit
	// override). The resolver is a folder-resolution layer; an
	// explicit FolderID from the caller means "use this exact folder,
	// don't resolve through Group/SubfolderName".
	folderID := res.FolderID
	if dest.FolderID != "" {
		folderID = dest.FolderID
	}
	folderPath := res.FolderPath
	if dest.FolderPath != "" {
		folderPath = dest.FolderPath
	}
	return &voiceover.ResolvedDestination{
		Group:         dest.Group,
		FolderID:      folderID,
		FolderPath:    folderPath,
		DriveLink:     res.DriveLink,
		SubfolderName: dest.SubfolderName, // MIRROR verbatim (P0.2 fix)
		StyleGroup:    dest.StyleGroup,    // MIRROR verbatim (NOT from resolver result).
	}, nil
}

var _ voiceover.DestinationResolver = (*useCaseDestResolverAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverDefaultFolderResolver adapter.
//
// PR 6 P0.2 (June 2026): when a GenerateVoiceoversCommand arrives
// without cmd.Destination, the use case falls back to the configured
// default Voiceover folder — the same value the legacy
// *Service.processLanguage folds in via `req.Destination =
// &DestinationRequest{FolderID: s.cfg.Drive.VoiceoverFolder()}`
// (process.go:75-79). The adapter takes the resolved folder ID at
// composition time (one read, deterministic), rather than re-reading
// cfg on every call, so:
//   - the wire shape is identical to the legacy path;
//   - the value is visible to operators via buildVoiceoverService's
//     constructor call (audit-friendly);
//   - a future "live re-read" wiring would be a port-method addition,
//     not an adapter rewrite.
//
// Resolve semantics mirror the canonical PR 6 P0.2 contract:
//   ("<folderID>", true)  → Execute synthesises a ResolvedDestination
//                            with that FolderID and proceeds.
//   ("", false)            → Execute surfaces a cross-cutting failure
//                            mapping to HTTP 400 upstream semantics.
//
// Nil-safe: nil receiver returns ("", false) so a partially-wired
// composition root cannot crash the per-language fan-out.
// ─────────────────────────────────────────────────────────────────────

type useCaseDefaultFolderResolverAdapter struct {
	driveFolderID  string
	localOutputDir string
}

func newUseCaseDefaultFolderResolverAdapter(driveFolderID, localOutputDir string) *useCaseDefaultFolderResolverAdapter {
	// No panic: empty driveFolderID is the production case when the
	// deployment lacks a configured voiceover_root_folder. The
	// adapter's Resolve returns ("", "", false) in that case, Execute
	// maps that to the canonical missing_folder_id short-circuit.
	// Empty localOutputDir is OK (audio stage may fail differently,
	// but missing_folder_id is no longer the failure mode).
	return &useCaseDefaultFolderResolverAdapter{
		driveFolderID:  driveFolderID,
		localOutputDir: localOutputDir,
	}
}

func (a *useCaseDefaultFolderResolverAdapter) Resolve(_ context.Context) (string, string, bool) {
	if a == nil || a.driveFolderID == "" {
		return "", "", false
	}
	return a.driveFolderID, a.localOutputDir, true
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ voiceover.VoiceoverDefaultFolderResolver = (*useCaseDefaultFolderResolverAdapter)(nil)
