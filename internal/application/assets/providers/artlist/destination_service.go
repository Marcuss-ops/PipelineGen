package artlist

import (
	"context"
	"fmt"
	"path"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
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
// PublisherPort is the narrow port for folder-only resolution via the
// canonical Publisher. Satisfied by delivery.Publisher (via an adapter
// in the composition root). Used by ResolveDestination to replace the
// legacy driveManager.EnsureFolder call (FASE 8).
type PublisherPort interface {
	ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error)
}

type DestinationService struct {
	driveManager DriveFolderManager // deprecated: kept for legacy fallback
	publisher    PublisherPort      // canonical folder resolution (FASE 8)
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
	return &DestinationService{driveManager: svc.driveFolderManager, publisher: svc.publisher, cfg: svc.cfg}
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

	// FASE 8: use canonical Publisher.ResolveFolder when available.
	// The DestinationArtlist registry policy maps to cfg.Drive.ArtlistFolder()
	// as root + [term] as path segments. When rootFolderID is the media root
	// or empty, let the registry resolve the Artlist root naturally. When a
	// specific rootFolderID is provided, pass it as RootFolderOverride.
	var folderID string
	if d.publisher != nil {
		pubReq := delivery.PublishRequest{
			Destination: delivery.DestinationArtlist,
			Group:       folderName,
		}
		isMediaRoot := d.cfg != nil && (rootFolderID == d.cfg.Drive.MediaRootFolder || rootFolderID == "")
		if !isMediaRoot {
			pubReq.RootFolderOverride = rootFolderID
		}
		fid, err := d.publisher.ResolveFolder(ctx, pubReq)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve folder via Publisher: %w", err)
		}
		folderID = fid
	} else if d.driveManager != nil {
		// Legacy fallback
		segments := []string{folderName}
		isMediaRoot := d.cfg != nil && (rootFolderID == d.cfg.Drive.MediaRootFolder || rootFolderID == "")
		if isMediaRoot {
			segments = append([]string{"Artlist"}, segments...)
		}
		fid, err := d.driveManager.EnsureFolder(ctx, rootFolderID, segments...)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure folder path: %w", err)
		}
		folderID = fid
	} else {
		folderID = rootFolderID
	}

	return &DestinationInfo{
		FolderID:   folderID,
		FolderPath: folderPath,
	}, nil
}
