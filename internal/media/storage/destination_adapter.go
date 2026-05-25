package storage

import (
	"context"
	"fmt"

	"velox/go-master/internal/core/destination"
)

// NewDestinationResolver creates a core destination.Resolver backed by a Store.
func NewDestinationResolver(s *Store) destination.Resolver {
	return &storeDestinationAdapter{store: s}
}

type storeDestinationAdapter struct {
	store *Store
}

func (a *storeDestinationAdapter) Resolve(ctx context.Context, req *destination.ResolveRequest) (*destination.ResolveResult, error) {
	if req == nil {
		return &destination.ResolveResult{}, nil
	}

	folderID := req.FolderID

	if folderID == "" {
		adReq := toAssetRequest(req)
		var err error
		folderID, err = a.store.EnsureDriveFolder(ctx, adReq)
		if err != nil {
			return nil, fmt.Errorf("ensure drive folder: %w", err)
		}
	}

	if req.SubfolderName != "" && req.CreateSubfolder && folderID != "" {
		subID, err := a.store.EnsureSubfolder(ctx, folderID, req.SubfolderName)
		if err != nil {
			return nil, fmt.Errorf("create subfolder: %w", err)
		}
		folderID = subID
	}

	result := &destination.ResolveResult{
		LocationKind: "drive",
		URI:          folderID,
		FolderID:     folderID,
		FolderPath:   req.FolderPath,
	}
	if folderID != "" {
		result.DriveLink = "https://drive.google.com/drive/folders/" + folderID
	}
	return result, nil
}

func toAssetRequest(req *destination.ResolveRequest) AssetDestinationRequest {
	ad := AssetDestinationRequest{
		Source:  nonEmptySource(req.Source),
		Group:   req.Group,
		Subject: req.SubfolderName,
	}
	ad.MediaType = sourceToMediaType(ad.Source)
	return ad
}

func nonEmptySource(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func sourceToMediaType(source string) string {
	switch source {
	case "youtube", "artlist", "stock":
		return MediaTypeClip
	case "voiceover":
		return MediaTypeVoiceover
	case "image":
		return MediaTypeImage
	default:
		return MediaTypeClip
	}
}
