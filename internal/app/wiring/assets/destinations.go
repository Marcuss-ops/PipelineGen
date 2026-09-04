package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	infradrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

var (
	ErrDestResolverNotWired = errors.New("drive asset destination resolver: admin not wired")
	ErrDestParentRequired   = errors.New("drive asset destination resolver: CreateSubfolder requires a parent FolderID")
	ErrDestSubfolderFailed  = errors.New("drive asset destination resolver: subfolder creation failed")
	ErrDestEmptyFolderID    = errors.New("drive asset destination resolver: subfolder resolution returned an empty folder id")
)

// DestResolver implements asset.Resolver over the canonical Drive Admin port.
type DestResolver struct {
	admin infradrive.Admin
}

// NewDestResolver constructs the resolver. A nil admin yields a nil resolver
// so the composition root can preserve the canonical nil-port contract.
func NewDestResolver(admin infradrive.Admin) *DestResolver {
	if admin == nil {
		return nil
	}
	return &DestResolver{admin: admin}
}

// Resolve materialises an optional child subfolder and returns the canonical
// Drive destination identity used by asset pipelines.
func (r *DestResolver) Resolve(ctx context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	if r == nil || r.admin == nil {
		return nil, fmt.Errorf("%w (composition gap)", ErrDestResolverNotWired)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrDestResolverNotWired)
	}

	folderID := strings.TrimSpace(req.FolderID)
	folderPath := strings.TrimSpace(req.FolderPath)
	subfolderName := strings.TrimSpace(req.SubfolderName)

	if req.CreateSubfolder && subfolderName != "" {
		if folderID == "" {
			return nil, fmt.Errorf("%w (subfolder=%q)", ErrDestParentRequired, subfolderName)
		}
		childID, err := infradrive.EnsureFolderPath(ctx, r.admin, folderID, subfolderName)
		if err != nil {
			return nil, fmt.Errorf("%w: create subfolder %q under %q: %w", ErrDestSubfolderFailed, subfolderName, folderID, err)
		}
		childID = strings.TrimSpace(childID)
		if childID == "" {
			return nil, fmt.Errorf("%w (subfolder=%q)", ErrDestEmptyFolderID, subfolderName)
		}
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

var _ asset.Resolver = (*DestResolver)(nil)
