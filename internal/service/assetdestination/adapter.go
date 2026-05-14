package assetdestination

import (
	"context"

	"velox/go-master/internal/core/destination"
)

// ToCoreResolver adapts an assetdestination.Resolver to core/destination.Resolver.
func ToCoreResolver(r *Resolver) destination.Resolver {
	return &coreAdapter{resolver: r}
}

type coreAdapter struct {
	resolver *Resolver
}

func (a *coreAdapter) Resolve(ctx context.Context, req *destination.ResolveRequest) (*destination.ResolveResult, error) {
	// Convert core request to assetdestination request
	adReq := &ResolveRequest{
		Source:          req.Source,
		Group:           req.Group,
		FolderID:        req.FolderID,
		FolderPath:      req.FolderPath,
		SubfolderName:   req.SubfolderName,
		CreateSubfolder: req.CreateSubfolder,
	}

	result, err := a.resolver.Resolve(ctx, adReq)
	if err != nil {
		return nil, err
	}

	return &destination.ResolveResult{
		LocationKind: "drive", // assetdestination always resolves to Drive
		URI:          result.FolderID,
		FolderID:     result.FolderID,
		FolderPath:   result.FolderPath,
		DriveLink:    result.DriveLink,
		Extra:        req.Metadata,
	}, nil
}
