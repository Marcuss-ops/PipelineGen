package artlist

import (
	"context"
	"fmt"
	"path"
	"strings"

	"velox/go-master/internal/upload/drive"
)

// DestinationInfo rappresenta una destinazione risolta per i clip
type DestinationInfo struct {
	FolderID   string
	FolderPath string
}

// DestinationService risolve le destinazioni Drive per i clip
type DestinationService struct {
	uploader *drive.Uploader
}

// NewDestinationService crea un nuovo servizio di destinazione
func NewDestinationService(svc *Service) *DestinationService {
	var uploader *drive.Uploader
	if svc.driveSvc != nil {
		uploader = &drive.Uploader{Service: svc.driveSvc, Log: svc.log}
	}
	return &DestinationService{uploader: uploader}
}

// ResolveDestination risolve la cartella Drive per un termine
func (d *DestinationService) ResolveDestination(ctx context.Context, term, rootFolderID string) (*DestinationInfo, error) {
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}

	if rootFolderID == "" {
		return nil, fmt.Errorf("root folder ID is required")
	}

	folderName := sanitizeFolderName(term)
	folderPath := path.Join("/Artlist", folderName)

	if d.uploader == nil {
		return &DestinationInfo{
			FolderID:   rootFolderID,
			FolderPath: folderPath,
		}, nil
	}

	folderID, err := d.uploader.GetOrCreateFolder(ctx, folderName, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create folder: %w", err)
	}

	return &DestinationInfo{
		FolderID:   folderID,
		FolderPath: folderPath,
	}, nil
}

func sanitizeFolderName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "*", "-")
	name = strings.ReplaceAll(name, "?", "-")
	name = strings.ReplaceAll(name, "\"", "-")
	name = strings.ReplaceAll(name, "<", "-")
	name = strings.ReplaceAll(name, ">", "-")
	name = strings.ReplaceAll(name, "|", "-")
	return strings.TrimSpace(name)
}
