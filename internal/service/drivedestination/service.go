package drivedestination

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/drive"
)

// Service handles Drive destination resolution (groups, folders, subfolders).
type Service struct {
	cfg        *config.Config
	log        *zap.Logger
	driveSvc   *driveapi.Service
}

// NewService creates a new drive destination service.
func NewService(cfg *config.Config, log *zap.Logger, driveSvc *driveapi.Service) *Service {
	return &Service{
		cfg:      cfg,
		log:      log,
		driveSvc: driveSvc,
	}
}

// Request holds the destination resolution request.
type Request struct {
	Group           string // e.g. "boxe", "wwe", "wnba"
	FolderID        string // explicit folder ID (overrides group)
	FolderPath      string // optional path info
	SubfolderName   string // e.g. "Mike Tyson"
	CreateSubfolder bool   // whether to create subfolder if not exists
}

// Resolved holds the resolved destination info.
type Resolved struct {
	Group      string
	FolderID   string
	FolderPath string
	DriveLink  string
}

// Resolve determines the final Drive folder for a clip.
// Priority: FolderID > Group > default.
func (s *Service) Resolve(ctx context.Context, req *Request) (*Resolved, error) {
	s.log.Info("Resolve called",
		zap.String("group", req.Group),
		zap.String("folder_id", req.FolderID),
		zap.String("subfolder", req.SubfolderName),
		zap.Bool("create_subfolder", req.CreateSubfolder),
	)

	if s.driveSvc == nil {
		return nil, fmt.Errorf("drive service not configured")
	}

	result := &Resolved{
		Group:      req.Group,
		FolderPath: req.FolderPath,
	}

	// Step 1: If explicit FolderID provided, use it as the base
	if req.FolderID != "" {
		result.FolderID = req.FolderID
		s.log.Info("using explicit folder_id as base",
			zap.String("folder_id", req.FolderID),
		)
	} else if req.Group != "" {
		// Step 2: Resolve by group name
		folderID, err := s.resolveGroupFolder(ctx, req.Group)
		if err != nil {
			s.log.Warn("failed to resolve group folder, searching for 'Artlist' fallback",
				zap.String("group", req.Group),
				zap.Error(err),
			)
			// Fallback: search for a general "Artlist" folder in root
			fallbackID, fbErr := s.getOrCreateSubfolder(ctx, "root", "Artlist")
			if fbErr == nil {
				folderID = fallbackID
			} else {
				// Final fallback: use root (dangerous but gives us a link)
				folderID = "root"
			}
		}
		result.FolderID = folderID
	}

	// Step 3: Handle subfolder if requested
	if req.SubfolderName != "" && req.CreateSubfolder && result.FolderID != "" {
		subfolderID, err := s.getOrCreateSubfolder(ctx, result.FolderID, req.SubfolderName)
		if err != nil {
			s.log.Warn("failed to get/create subfolder",
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

	return result, nil
}

// resolveGroupFolder looks up the Drive folder ID for a group name.
func (s *Service) resolveGroupFolder(ctx context.Context, group string) (string, error) {
	// Check config for group mapping (case-insensitive)
	if s.cfg.Drive.ClipRootFolders != nil {
		s.log.Info("checking config for group",
			zap.String("requested_group", group),
			zap.Int("config_entries", len(s.cfg.Drive.ClipRootFolders)),
		)
		// Try exact match first
		if folderID, ok := s.cfg.Drive.ClipRootFolders[group]; ok && folderID != "" {
			s.log.Info("found group folder in config",
				zap.String("group", group),
				zap.String("folder_id", folderID),
			)
			return folderID, nil
		}
		// Try case-insensitive match
		for cfgGroup, folderID := range s.cfg.Drive.ClipRootFolders {
			if strings.EqualFold(cfgGroup, group) && folderID != "" {
				s.log.Info("found group folder in config (case-insensitive)",
					zap.String("requested_group", group),
					zap.String("config_group", cfgGroup),
					zap.String("folder_id", folderID),
				)
				return folderID, nil
			}
		}
	}

	// Fallback: search in Drive by folder name (try multiple case variations)
	searchNames := []string{group, strings.Title(strings.ToLower(group)), strings.ToUpper(group), strings.ToLower(group)}
	seen := make(map[string]bool)
	for _, name := range searchNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		query := drive.BuildNameQuery("root", name, "application/vnd.google-apps.folder")
		list, err := s.driveSvc.Files.List().
			Q(query).
			Fields("files(id, name)").
			Context(ctx).
			Do()
		if err != nil {
			s.log.Warn("failed to search group folder",
				zap.String("name", name),
				zap.Error(err),
			)
			continue
		}
		if len(list.Files) > 0 {
			s.log.Info("found group folder in Drive",
				zap.String("requested", group),
				zap.String("found_name", list.Files[0].Name),
				zap.String("folder_id", list.Files[0].Id),
			)
			return list.Files[0].Id, nil
		}
	}

	return "", fmt.Errorf("group folder not found: %s", group)
}

// getOrCreateSubfolder gets or creates a subfolder within a parent folder.
func (s *Service) getOrCreateSubfolder(ctx context.Context, parentID, name string) (string, error) {
	// Search for existing subfolder
	query := drive.BuildNameQuery(parentID, name, "application/vnd.google-apps.folder")
	list, err := s.driveSvc.Files.List().
		Q(query).
		Fields("files(id, name)").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to search subfolder: %w", err)
	}

	if len(list.Files) > 0 {
		s.log.Info("found existing subfolder",
			zap.String("name", name),
			zap.String("folder_id", list.Files[0].Id),
		)
		return list.Files[0].Id, nil
	}

	// Create new subfolder
	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := s.driveSvc.Files.Create(folder).
		Fields("id", "webViewLink").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to create subfolder: %w", err)
	}

	s.log.Info("created subfolder",
		zap.String("name", name),
		zap.String("folder_id", created.Id),
	)

	return created.Id, nil
}
