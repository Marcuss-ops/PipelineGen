package assetops

import (
	"context"

	"velox/go-master/internal/service/drivedestination"
)

// ResolveDestination resolves the destination for an asset using drivedestination.Service
func ResolveDestination(ctx context.Context, svc *drivedestination.Service, spec DestinationSpec) (*ResolvedDestination, error) {
	req := &drivedestination.Request{
		Group:         spec.FolderName, // Map FolderName to Group
		FolderID:      spec.FolderID,
		SubfolderName: spec.FileName, // Map FileName to SubfolderName
	}
	resolved, err := svc.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	return &ResolvedDestination{
		Path:     resolved.FolderPath,
		FolderID: resolved.FolderID,
		FileName: spec.FileName,
	}, nil
}
