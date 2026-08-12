package drive

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"go.uber.org/zap"
)

// ── resolveDestination: canonical resolution pipeline ────────────────
//
// Extracted from publisher.go per AGENTS.md Pattern 5
// (PR-PUBLISHER-SPLIT, July 2026).
//
// Steps (canonical order — see ARCHITECTURE.md §6):
//  1. registry.Resolve(req.Destination)
//  2. Apply ParentFolderID (back-compat escape hatch)
//  3. Reject empty root folder (misconfiguration surface)
//  4. policy.PathBuilder(req)
//  5. RequireSubpath enforcement
//  6. FolderManager.EnsureFolder (only if PathSegments is non-empty)

func (p *Publisher) resolveDestination(ctx context.Context, req delivery.PublishRequest) (*ResolvedDriveDestination, error) {
	// An explicit DestinationFolderID is already the resolved leaf folder.
	// Do not consult the registry, path builders, catalog, or FolderManager:
	// re-resolving here would let configuration or semantic path rules move
	// an upload away from the folder selected by the application plan.
	if folderID := strings.TrimSpace(req.DestinationFolderID); folderID != "" {
		if len(req.DestinationSubpath) > 0 {
			child, err := p.folders.EnsureFolder(ctx, folderID, req.DestinationSubpath...)
			if err != nil {
				return nil, fmt.Errorf("delivery: resolve destination subpath: %w", err)
			}
			folderID = child
		}
		return &ResolvedDriveDestination{
			Destination:  req.Destination,
			RootFolderID: folderID,
			FolderID:     folderID,
			PathSegments: append([]string(nil), req.DestinationSubpath...),
		}, nil
	}

	// Step 1: Registry resolve.
	policy, err := p.registry.Resolve(req.Destination)
	if err != nil {
		return nil, err
	}

	// Step 2: Root override.
	// DestinationFolderID is the canonical application-layer way to
	// pin the target folder for sidecar destinations (e.g.
	// DestinationClipMetadata). ParentFolderID is the legacy
	// admin-CLI escape hatch kept for backward compatibility.
	rootFolderID := policy.RootFolderID
	if destFolder := strings.TrimSpace(req.DestinationFolderID); destFolder != "" {
		rootFolderID = destFolder
	} else if override := strings.TrimSpace(req.ParentFolderID); override != "" {
		rootFolderID = override
	}

	// Step 3: Empty-root rejection.
	if rootFolderID == "" {
		return nil, fmt.Errorf(
			"delivery: destination %q has no configured root folder",
			req.Destination,
		)
	}

	// Step 4: Path builder.
	var segments []string
	pathBuilt := false
	if segments, err = policy.PathBuilder(req); err == nil {
		pathBuilt = true
	} else if req.ParentFolderID != "" {
		err = fmt.Errorf("delivery: PathBuilder failed under ParentFolderID (cause: %w): %w", err, ErrPathBuilderIncompleteForParent)
		segments = nil
	} else {
		return nil, fmt.Errorf("delivery: build path for %q: %w", req.Destination, err)
	}

	// Step 5: RequireSubpath enforcement (SYMMETRIC across callers).
	if policy.RequireSubpath && len(segments) == 0 && pathBuilt && strings.TrimSpace(req.DestinationFolderID) == "" {
		return nil, fmt.Errorf(
			"delivery: direct upload into root %q is forbidden for destination %q",
			rootFolderID, req.Destination,
		)
	}

	// Step 6: Folder hierarchy creation (catalog-first, DoD item 6).
	// When the caller supplied an explicit ParentFolderID we must
	// bypass the folder catalog lookup: the override is a request-local
	// root, so reusing a cached path from another root would silently
	// route the upload into the wrong Drive tree.
	folderID := rootFolderID
	if len(segments) > 0 {
		pathKey := strings.Join(segments, "/")
		if strings.TrimSpace(req.DestinationFolderID) == "" && strings.TrimSpace(req.ParentFolderID) == "" {
			if cachedID := p.lookupCatalogFolder(ctx, req.Destination, pathKey); cachedID != "" {
				folderID = cachedID
			} else {
				folderID, err = p.folders.EnsureFolder(ctx, rootFolderID, segments...)
				if err != nil {
					return nil, fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
				}
				if p.catalogWriter != nil {
					if writeErr := p.catalogWriter.RecordFolder(ctx, string(req.Destination), pathKey, folderID, rootFolderID); writeErr != nil {
						p.log.Warn("delivery: folder resolved but catalog persistence failed", zap.String("destination", string(req.Destination)), zap.String("path", pathKey), zap.Error(writeErr))
					}
				}
			}
		} else {
			folderID, err = p.folders.EnsureFolder(ctx, rootFolderID, segments...)
			if err != nil {
				return nil, fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
			}
			if p.catalogWriter != nil {
				if writeErr := p.catalogWriter.RecordFolder(ctx, string(req.Destination), pathKey, folderID, rootFolderID); writeErr != nil {
					p.log.Warn("delivery: folder resolved but catalog persistence failed", zap.String("destination", string(req.Destination)), zap.String("path", pathKey), zap.Error(writeErr))
				}
			}
		}
	}

	return &ResolvedDriveDestination{
		Destination:  req.Destination,
		RootFolderID: rootFolderID,
		FolderID:     folderID,
		PathSegments: segments,
	}, err
}

// lookupCatalogFolder consults the local drive_folder_catalog for a
// cached folder ID. Returns "" when no active catalog entry exists,
// the catalog is not wired, or a lookup error occurs — the Publisher
// falls back to EnsureFolder in all those cases.
//
// Catalog lookups are best-effort: an infrastructure error (DB down)
// is logged at Warn and returns "" so the Publisher falls back to
// the Drive API path rather than failing the Publish call.
func (p *Publisher) lookupCatalogFolder(ctx context.Context, dest delivery.DestinationKey, path string) string {
	if p.catalogLookup == nil {
		return ""
	}
	folderID, err := p.catalogLookup.LookupFolder(ctx, string(dest), path)
	if err != nil {
		p.log.Warn("delivery: catalog lookup failed, falling back to Drive",
			zap.String("destination", string(dest)),
			zap.String("path", path),
			zap.Error(err),
		)
		return ""
	}
	if folderID != "" {
		p.log.Debug("delivery: using cached folder from catalog",
			zap.String("destination", string(dest)),
			zap.String("path", path),
			zap.String("folder_id", folderID),
		)
	}
	return folderID
}
