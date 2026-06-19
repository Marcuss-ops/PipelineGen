package artlist

import (
	"context"
	"fmt"
	"path"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// DestinationInfo rappresenta una destinazione risolta per i clip
type DestinationInfo struct {
	FolderID   string
	FolderPath string
}

// DestinationService risolve le destinazioni Drive per i clip
type DestinationService struct {
	uploader *drive.Uploader
	cfg      *config.Config
}

// NewDestinationService crea un nuovo servizio di destinazione
func NewDestinationService(svc *Service) *DestinationService {
	var uploader *drive.Uploader
	if svc.driveSvc != nil {
		uploader = &drive.Uploader{Service: svc.driveSvc, Log: svc.log}
	}
	return &DestinationService{uploader: uploader, cfg: svc.cfg}
}

// ResolveDestination risolve la cartella Drive per un termine
func (d *DestinationService) ResolveDestination(ctx context.Context, term, rootFolderID string) (*DestinationInfo, error) {
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}

	if rootFolderID == "" {
		return nil, fmt.Errorf("root folder ID is required")
	}

	folderName := textutil.SafeName(term)
	folderPath := path.Join("/Artlist", folderName)

	if d.uploader == nil {
		return &DestinationInfo{
			FolderID:   rootFolderID,
			FolderPath: folderPath,
		}, nil
	}

	// Build segment list: only prepend "Artlist" when resolving from the media root.
	segments := []string{folderName}
	isMediaRoot := d.cfg != nil && (rootFolderID == d.cfg.Drive.MediaRootFolder || rootFolderID == "")
	if isMediaRoot {
		segments = append([]string{"Artlist"}, segments...)
	}

	folderID, err := drive.EnsureFolderPath(ctx, d.uploader, rootFolderID, segments...)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure folder path: %w", err)
	}

	return &DestinationInfo{
		FolderID:   folderID,
		FolderPath: folderPath,
	}, nil
}
