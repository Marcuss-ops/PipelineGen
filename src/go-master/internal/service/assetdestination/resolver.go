package assetdestination

import (
	"context"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/pkg/config"
)

// Resolver handles unified destination resolution for all asset types.
type Resolver struct {
	cfg              *config.Config
	log              *zap.Logger
	driveSvc         *driveapi.Service
	driveDestination *drivedestination.Service
}

// NewResolver creates a new asset destination resolver.
func NewResolver(cfg *config.Config, log *zap.Logger, driveSvc *driveapi.Service) *Resolver {
	return &Resolver{
		cfg:              cfg,
		log:              log,
		driveSvc:         driveSvc,
		driveDestination: drivedestination.NewService(cfg, log, driveSvc),
	}
}

// ResolveRequest holds the unified destination resolution request.
type ResolveRequest struct {
	Source         string // e.g. "youtube", "artlist", "voiceover"
	Group          string // e.g. "boxe", "wwe", "wnba"
	FolderID       string // explicit folder ID (overrides group)
	FolderPath     string // optional path info
	SubfolderName  string // e.g. "Mike Tyson" or video ID
	CreateSubfolder bool  // whether to create subfolder if not exists
}

// Resolved holds the unified destination resolution result.
type Resolved struct {
	Source    string
	Group     string
	FolderID  string
	FolderPath string
	DriveLink string
}

// Resolve determines the final Drive folder for an asset.
// Priority: FolderID > Group > default.
func (r *Resolver) Resolve(ctx context.Context, req *ResolveRequest) (*Resolved, error) {
	r.log.Info("resolving asset destination",
		zap.String("source", req.Source),
		zap.String("group", req.Group),
		zap.String("folder_id", req.FolderID),
		zap.String("subfolder", req.SubfolderName),
		zap.Bool("create_subfolder", req.CreateSubfolder),
	)

	// Use drivedestination service for core resolution
	destReq := &drivedestination.Request{
		Group:          req.Group,
		FolderID:       req.FolderID,
		FolderPath:     req.FolderPath,
		SubfolderName:  req.SubfolderName,
		CreateSubfolder: req.CreateSubfolder,
	}

	resolved, err := r.driveDestination.Resolve(ctx, destReq)
	if err != nil {
		return nil, err
	}

	return &Resolved{
		Source:    req.Source,
		Group:     resolved.Group,
		FolderID:  resolved.FolderID,
		FolderPath: resolved.FolderPath,
		DriveLink: resolved.DriveLink,
	}, nil
}
