package drive

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// DestinationResolver is the thin wrapper that satisfies
// `core/asset.Resolver` so that callers can use a Store as the
// destination-resolver dependency without reshaping the field. The
// original (pre-Phase-7) implementation lived in
// `destinations.ResolverAdapter`; this facade reproduces the signature
// so `storage.NewDestinationResolver(s)` returns the same interface
// type the historical wiring expected.
type DestinationResolver struct {
	store *Store
}

// NewDestinationResolver returns a `asset.Resolver` built around
// the supplied Store. The wrapping ensures calls match the canonical
// Resolver interface in `internal/core/destination/types.go`.
func NewDestinationResolver(s *Store) asset.Resolver {
	return &destinationResolverAdapter{store: s}
}

// destinationResolverAdapter is the actual interface implementer. Kept
// private â€” callers only see the interface or *DestinationResolver.
type destinationResolverAdapter struct {
	store *Store
}

// Resolve maps a asset.ResolveRequest to a asset.ResolveResult.
// The pre-Phase-7 contract returned a struct with FolderID + DriveLink
// fully populated; this adapter mirrors that shape using the Store's
// configured folders and Resolver for path-only destinations.
func (d *destinationResolverAdapter) Resolve(_ context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	if d == nil || d.store == nil {
		return &asset.ResolveResult{}, nil
	}
	if req == nil {
		return &asset.ResolveResult{
			LocationKind: "drive",
			FolderID:     d.store.rootFolder,
		}, nil
	}

	// Style/subfolder routing passthrough.
	folderID := d.store.rootFolder
	switch req.AssetType {
	case "image":
		if d.store.imagesFolder != "" {
			folderID = d.store.imagesFolder
		}
	case "video", "video_ai":
		if d.store.videoAIRoot != "" {
			folderID = d.store.videoAIRoot
		}
	case "sound_effect", "audio":
		if d.store.soundEffectsRoot != "" {
			folderID = d.store.soundEffectsRoot
		}
	}

	// Path-only lookup so callers that don't need Drive still get a
	// useful LocalPath. The Resolver returns sensible paths for empty
	// subjects/exts.
	assetReq := AssetDestinationRequest{
		Source:  SourceType(req.Source),
		Subject: req.SubfolderName,
		Style:   req.Group,
		Hash:    req.AssetID,
		Group:   req.Group,
	}
	if d.store.resolver != nil {
		if resolved, err := d.store.resolver.Resolve(assetReq); err == nil && resolved != nil {
			return &asset.ResolveResult{
				LocationKind: "drive",
				URI:          folderID,
				FolderID:     folderID,
				Extra: map[string]any{
					"local_path":    resolved.LocalPath,
					"relative_path": resolved.RelativePath,
				},
			}, nil
		}
	}

	return &asset.ResolveResult{
		LocationKind: "drive",
		URI:          folderID,
		FolderID:     folderID,
	}, nil
}
