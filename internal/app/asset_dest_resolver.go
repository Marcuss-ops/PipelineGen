// Package app — asset_dest_resolver.go reconnects the canonical
// asset.Resolver port (internal/kernel/asset) to the real Drive
// get-or-create surface.
//
// Before this adapter, wiring.DriveBundle.DestResolver was nil because the
// previous drive.NewDestinationResolver implementation was tied to
// drive.Store, which has been retired (PR-IMAGES-REMOVE-DRIVE-STORE). The
// YouTube extraction pipeline therefore carried folder_path metadata for a
// requested subfolder but never materialised the folder — the clips landed
// in the root while media_assets.folder_path recorded the subfolder
// (silent-wrong-location, godlike/07 no-fake-availability).
//
// AssetDestResolver implements asset.Resolver over the canonical
// drive.EnsureFolderPath primitive (which walks drive.Admin.GetOrCreateFolder):
// when the request asks to create a subfolder under an explicit parent, the
// adapter materialises the folder and returns the CHILD folder id; otherwise
// it passes the explicit folder id through verbatim with zero Drive I/O.
// Fail-closed: an unwired admin, a missing parent, or a subfolder-creation
// failure surfaces a typed sentinel (probe via errors.Is) instead of
// silently landing assets in the wrong folder (godlike/07).
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	infradrive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Typed-error contract (godlike/07 NO-FAKE-AVAILABILITY) ────────────────
//
// Callers probe these sentinels via errors.Is. Each sentinel is the
// canonical SOLE owner of its error class.
var (
	// ErrAssetDestResolverNotWired surfaces when the admin port is nil
	// (Drive not configured) but a subfolder resolution is attempted.
	ErrAssetDestResolverNotWired = errors.New("drive asset destination resolver: admin not wired")

	// ErrAssetDestParentRequired surfaces when CreateSubfolder is set but
	// no parent FolderID was supplied.
	ErrAssetDestParentRequired = errors.New("drive asset destination resolver: CreateSubfolder requires a parent FolderID")

	// ErrAssetDestSubfolderFailed surfaces when the real Drive
	// EnsureFolderPath get-or-create walk fails (the subfolder cannot be
	// created). This is the fail-closed boundary the caller probes with
	// errors.Is before deciding whether to retry or abort.
	ErrAssetDestSubfolderFailed = errors.New("drive asset destination resolver: subfolder creation failed")

	// ErrAssetDestEmptyFolderID surfaces when the get-or-create walk
	// returns an empty folder id.
	ErrAssetDestEmptyFolderID = errors.New("drive asset destination resolver: subfolder resolution returned an empty folder id")
)

// AssetDestResolver implements asset.Resolver over drive.Admin. It is the
// canonical asset.Resolver binding for callers that resolve an explicit
// Drive folder and optionally materialise a child subfolder.
type AssetDestResolver struct {
	admin infradrive.Admin
}

// NewAssetDestResolver constructs the resolver. Typed-nil-safe: a nil admin
// returns nil so callers can rely on the canonical `!= nil` nil-port pattern
// (the caller owns the fail-closed decision when Drive is not configured).
func NewAssetDestResolver(admin infradrive.Admin) *AssetDestResolver {
	if admin == nil {
		return nil
	}
	return &AssetDestResolver{admin: admin}
}

// Resolve materialises the requested subfolder via the canonical
// drive.EnsureFolderPath get-or-create walk when CreateSubfolder is set and
// SubfolderName is non-empty, and otherwise returns the explicit FolderID
// verbatim.
func (r *AssetDestResolver) Resolve(ctx context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	if r == nil || r.admin == nil {
		return nil, fmt.Errorf("%w (composition gap)", ErrAssetDestResolverNotWired)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrAssetDestResolverNotWired)
	}

	folderID := strings.TrimSpace(req.FolderID)
	folderPath := strings.TrimSpace(req.FolderPath)
	subfolderName := strings.TrimSpace(req.SubfolderName)

	if req.CreateSubfolder && subfolderName != "" {
		if folderID == "" {
			return nil, fmt.Errorf("%w (subfolder=%q)", ErrAssetDestParentRequired, subfolderName)
		}
		// Canonical path: drive.EnsureFolderPath walks the segments under
		// the parent via drive.Admin.GetOrCreateFolder and returns the leaf
		// (child) folder id.
		childID, err := infradrive.EnsureFolderPath(ctx, r.admin, folderID, subfolderName)
		if err != nil {
			return nil, fmt.Errorf("%w: create subfolder %q under %q: %w", ErrAssetDestSubfolderFailed, subfolderName, folderID, err)
		}
		childID = strings.TrimSpace(childID)
		if childID == "" {
			return nil, fmt.Errorf("%w (subfolder=%q)", ErrAssetDestEmptyFolderID, subfolderName)
		}
		// The upload now targets the CHILD folder. folder_path keeps the
		// transport-computed label; when none was supplied, derive it from
		// the subfolder name so the metadata record matches the actual
		// destination.
		folderID = childID
		if folderPath == "" {
			folderPath = subfolderName
		}
	}

	return &asset.ResolveResult{
		LocationKind: "drive",
		URI:          folderID,
		FolderID:     folderID,
		FolderPath:   folderPath,
	}, nil
}

// Compile-time assertion: AssetDestResolver satisfies asset.Resolver.
var _ asset.Resolver = (*AssetDestResolver)(nil)
