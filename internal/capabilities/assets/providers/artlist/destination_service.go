package artlist

import (
	"context"
	"fmt"
	"path"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DestinationInfo rappresenta una destinazione risolta per i clip
type DestinationInfo struct {
	FolderID   string
	FolderPath string
}

// DestinationService risolve le destinazioni Drive per i clip.
//
// F2.11 (June 2026): driveManager field + DriveFolderManager port +
// the legacy `else if driveManager != nil` branch + the silent
// `folderID = rootFolderID` fallback were RETIRED entirely (override
// brutal). The service is now Publisher-only: ResolveDestination
// always calls d.publisher.ResolveFolder, and a nil publisher
// panics at construction time because Publisher is mandatory at
// Service.NewService composition (ErrPublisherUnavailable guard).
//
// PublisherPort is the narrow port for folder-only resolution via the
// canonical Publisher. Satisfied by delivery.Publisher (via an adapter
// in the composition root). The folder resolution is canonicalised via
// the DestinationRegistry's destination_artlist policy (root =
// cfg.Drive.ArtlistFolder(), path segments = [term]); the publisher
// owns the folder creation + ID resolution.
type PublisherPort interface {
	ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error)
}

type DestinationService struct {
	publisher PublisherPort // mandatory (F2.11): Publisher is the only Drive-write canal
	cfg       *config.Config
}

// NewDestinationService crea un nuovo servizio di destinazione.
//
// F2.11 (June 2026): the driveManager field is gone; resolve
// destination is publisher-only. The publisher is captured once at
// construction from the parent Service (which gates nil at NewService
// time via ErrPublisherUnavailable). When the optional publisher
// narrowing (PublisherPort) is nil at this constructor — only possible
// when the parent Service was constructed via a test fixture that
// bypassed the nil-check via a typed-nil inference — this constructor
// still panics to surface the wiring defect at the first opportunity.
func NewDestinationService(svc *Service) *DestinationService {
	if svc.publisher == nil {
		panic("artlist.DestinationService: publisher is required (F2.11: legacy DriveFolderManager fallback removed; silent folderID = rootFolderID branch removed; nil publisher is a wiring defect)")
	}
	return &DestinationService{publisher: svc.publisher, cfg: svc.cfg}
}

// ResolveDestination risolve la cartella Drive per un termine.
//
// F2.11 (June 2026): the only Drive folder-resolution path is
// d.publisher.ResolveFolder (canonical delivery.Publisher). The
// legacy `else if d.driveManager != nil { EnsureFolder(...) }` branch
// and the silent `folderID = rootFolderID` fallback are gone (override
// brutal; per the user spec: "Se Publisher manca nel wired bundle →
// errore di wiring al boot, non fallback silenzioso").
//
// The DestinationArtlist destination policy in the canonical
// DestinationRegistry maps the (Destination, Group=term) tuple to a
// folder rooted at cfg.Drive.ArtlistFolder() with [term] as the path
// segment. When rootFolderID equals the media root or is empty, the
// registry resolves the Artlist root naturally (no ParentFolderID);
// when a specific rootFolderID is provided, it's threaded through
// as ParentFolderID so callers can pin a non-natural root for
// back-compat with operator-deployed setups.
func (d *DestinationService) ResolveDestination(ctx context.Context, term, rootFolderID string) (*DestinationInfo, error) {
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}

	if rootFolderID == "" {
		return nil, fmt.Errorf("root folder ID is required")
	}

	folderName := textutil.SafeName(term)
	folderPath := path.Join("/Artlist", folderName)

	pubReq := delivery.PublishRequest{
		Destination: delivery.DestinationArtlist,
		Group:       folderName,
	}
	isMediaRoot := d.cfg != nil && (rootFolderID == d.cfg.Drive.MediaRootFolder || rootFolderID == "")
	if !isMediaRoot {
		pubReq.ParentFolderID = rootFolderID
	}
	folderID, err := d.publisher.ResolveFolder(ctx, pubReq)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve folder via Publisher: %w", err)
	}

	return &DestinationInfo{
		FolderID:   folderID,
		FolderPath: folderPath,
	}, nil
}
