package assetdestination

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/storage/drive"
)

// Resolver handles unified destination resolution for all asset types.
type Resolver struct {
	cfg      *config.Config
	log      *zap.Logger
	driveSvc *driveapi.Service
}

// NewResolver creates a new asset destination resolver.
func NewResolver(cfg *config.Config, log *zap.Logger, driveSvc *driveapi.Service) *Resolver {
	return &Resolver{
		cfg:      cfg,
		log:      log,
		driveSvc: driveSvc,
	}
}

// ResolveRequest holds the unified destination resolution request.
type ResolveRequest struct {
	Source          string // e.g. "youtube", "artlist", "voiceover"
	Group           string // Name of the group folder
	FolderID        string // explicit folder ID (overrides group)
	FolderPath      string // optional path info
	SubfolderName   string // Name of the subfolder or video ID
	CreateSubfolder bool   // whether to create subfolder if not exists
}

// Resolved holds the unified destination resolution result.
type Resolved struct {
	Source     string
	Group      string
	FolderID   string
	FolderPath string
	DriveLink  string
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

	if r.driveSvc == nil {
		return nil, fmt.Errorf("drive service not configured")
	}

	result := &Resolved{
		Source:     req.Source,
		Group:      req.Group,
		FolderPath: req.FolderPath,
	}

	// Step 1: If explicit FolderID provided, use it as the base
	if req.FolderID != "" {
		result.FolderID = req.FolderID
		r.log.Info("using explicit folder_id as base",
			zap.String("folder_id", req.FolderID),
		)
	} else if req.Group != "" {
		// Step 2: Resolve by group name
		folderID, err := r.resolveGroupFolder(ctx, req.Group)
		if err != nil {
			r.log.Warn("failed to resolve group folder, searching for 'Artlist' fallback",
				zap.String("group", req.Group),
				zap.Error(err),
			)
			// Fallback: search for a general "Artlist" folder in root
			fallbackID, fbErr := r.getOrCreateSubfolder(ctx, "root", "Artlist")
			if fbErr == nil {
				folderID = fallbackID
			} else {
				// Final fallback: use root
				folderID = "root"
			}
		}
		result.FolderID = folderID
	}

	// Step 3: Handle subfolder if requested
	if req.SubfolderName != "" && req.CreateSubfolder && result.FolderID != "" {
		subfolderID, err := r.getOrCreateSubfolder(ctx, result.FolderID, req.SubfolderName)
		if err != nil {
			r.log.Warn("failed to get/create subfolder",
				zap.String("subfolder", req.SubfolderName),
				zap.Error(err),
			)
		} else {
			result.FolderID = subfolderID
			// Prepend parent path if available, or just use subfolder name
			if result.FolderPath != "" {
				result.FolderPath = result.FolderPath + "/" + req.SubfolderName
			} else {
				result.FolderPath = req.SubfolderName
			}
		}
	}

	if result.FolderID != "" {
		result.DriveLink = "https://drive.google.com/drive/folders/" + result.FolderID
	}

	return result, nil
}

// resolveGroupFolder looks up the Drive folder ID for a group name.
func (r *Resolver) resolveGroupFolder(ctx context.Context, group string) (string, error) {
	// Check config for group mapping (case-insensitive)
	if r.cfg.Drive.ClipRootFolders != nil {
		// Try exact match first
		if folderID, ok := r.cfg.Drive.ClipRootFolders[group]; ok && folderID != "" {
			return folderID, nil
		}
		// Try case-insensitive match
		for cfgGroup, folderID := range r.cfg.Drive.ClipRootFolders {
			if strings.EqualFold(cfgGroup, group) && folderID != "" {
				return folderID, nil
			}
		}
	}

	// Fallback: search in Drive by folder name
	searchNames := []string{group, strings.Title(strings.ToLower(group)), strings.ToUpper(group), strings.ToLower(group)}
	seen := make(map[string]bool)
	for _, name := range searchNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		query := drive.BuildNameQuery("root", name, "application/vnd.google-apps.folder")
		list, err := r.driveSvc.Files.List().
			Q(query).
			Fields("files(id, name)").
			Context(ctx).
			Do()
		if err != nil {
			continue
		}
		if len(list.Files) > 0 {
			return list.Files[0].Id, nil
		}
	}

	return "", fmt.Errorf("group folder not found: %s", group)
}

// getOrCreateSubfolder gets or creates a subfolder within a parent folder.
func (r *Resolver) getOrCreateSubfolder(ctx context.Context, parentID, name string) (string, error) {
	// Search for existing subfolder
	query := drive.BuildNameQuery(parentID, name, "application/vnd.google-apps.folder")
	list, err := r.driveSvc.Files.List().
		Q(query).
		Fields("files(id, name)").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to search subfolder: %w", err)
	}

	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	// Create new subfolder
	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := r.driveSvc.Files.Create(folder).
		Fields("id").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to create subfolder: %w", err)
	}

	return created.Id, nil
}
