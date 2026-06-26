package artlist

import (
	"context"
	"fmt"
	"path"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DestinationInfo rappresenta una destinazione risolta per i clip
type DestinationInfo struct {
	FolderID   string
	FolderPath string
}

// DestinationService risolve le destinazioni Drive per i clip.
//
// PR2.7: drive is the canonical DriveFolderManager port (was
// *drive.Uploader concrete). Pre-PR2.7 this service built a narrow
// *drive.Uploader{Service: svc.driveClient, Log: svc.log} inline and
// delegated to drive.EnsureFolderPath. PR2.7 closes the loop: DestinationService
// receives the same port instance NewService wires via ServiceDeps
// (built once in module_sources.go::WireArtlist from bundle.DriveClient),
// and calls EnsureFolder directly.
//
// Folding this service onto the port completes the directive
// "inietta porte invece di concrezioni" for artlist: the only remaining
// raw-SDK reach-through in the artlist package was this struct +
// drive.EnsureFolderPath. Both are gone post-PR2.7.
type DestinationService struct {
	driveManager DriveFolderManager
	cfg          *config.Config
}

// NewDestinationService crea un nuovo servizio di destinazione.
//
// PR2.7: reads s.driveFolderManager (port) instead of constructing a
// *drive.Uploader concrete from s.driveClient. The port is wired at the
// composition root (module_sources.go::WireArtlist builds the adapter
// from bundle.DriveClient once) and threaded into ServiceDeps.
// ServicePorts.DriveFolderManager. When the port is nil (test fixtures
// without Drive), ResolveDestination returns the requested folder path
// without making any Drive calls — same nil-tolerance behaviour callers
// already depended on.
func NewDestinationService(svc *Service) *DestinationService {
	return &DestinationService{driveManager: svc.driveFolderManager, cfg: svc.cfg}
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

	if d.driveManager == nil {
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

	// PR2.7: call the port's EnsureFolder directly (was drive.EnsureFolderPath).
	// textutil.SafeName sanitises the term so it matches the canonical name
	// DriveFolderManagerAdapter.findOrCreateFolder uses (exact name match —
	// the legacy *drive.Uploader.GetOrCreateFolder fuzzy fallback is gone).
	folderID, err := d.driveManager.EnsureFolder(ctx, rootFolderID, segments...)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure folder path: %w", err)
	}

	return &DestinationInfo{
		FolderID:   folderID,
		FolderPath: folderPath,
	}, nil
}
