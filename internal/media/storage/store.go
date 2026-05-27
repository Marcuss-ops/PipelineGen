package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	driveup "velox/go-master/internal/upload/drive"
)

// Store is the unified entry point for all media storage operations.
// It combines:
//   - Path resolution (where to save)
//   - Local filesystem save
//   - Drive upload (via shared drive.Uploader — single source of truth)
//   - DB record creation/update
//
// All services should use Store instead of managing paths/Drive/DB directly.
type Store struct {
	resolver         *Resolver
	driveUp          *driveup.Uploader
	driveRoot        string // default/legacy root
	imageDriveRoot   string // per-type root for MediaTypeImage
	videoDriveRoot   string // per-type root for MediaTypeImageVideo
	soundDriveRoot   string // per-type root for MediaTypeSoundEffect
	log              *zap.Logger
}

// NewStore creates a unified media store.
// driveRoot is the default root; imageDriveRoot and videoDriveRoot override for their media types.
func NewStore(resolver *Resolver, driveUp *driveup.Uploader, driveRoot, imageDriveRoot, videoDriveRoot, soundDriveRoot string, log *zap.Logger) *Store {
	return &Store{
		resolver:       resolver,
		driveUp:        driveUp,
		driveRoot:      driveRoot,
		imageDriveRoot: imageDriveRoot,
		videoDriveRoot: videoDriveRoot,
		soundDriveRoot: soundDriveRoot,
		log:            log,
	}
}

// rootForMediaType returns the Drive root folder ID for the given media type.
// Falls back to the default driveRoot if no per-type root is configured.
func (s *Store) rootForMediaType(mediaType string) string {
	switch mediaType {
	case MediaTypeImageVideo:
		if s.videoDriveRoot != "" {
			return s.videoDriveRoot
		}
	case MediaTypeImage:
		if s.imageDriveRoot != "" {
			return s.imageDriveRoot
		}
	case MediaTypeSoundEffect:
		if s.soundDriveRoot != "" {
			return s.soundDriveRoot
		}
	}
	return s.driveRoot
}

// ResolveDest returns the storage destination for an asset without saving anything.
func (s *Store) ResolveDest(req AssetDestinationRequest) (*AssetDestination, error) {
	return s.resolver.Resolve(req)
}

// SaveFile saves content to the local filesystem at the resolved path.
// Returns the absolute local path.
func (s *Store) SaveFile(ctx context.Context, req AssetDestinationRequest, data io.Reader) (*AssetDestination, error) {
	dest, err := s.resolver.Resolve(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dest.LocalPath), 0755); err != nil {
		return nil, fmt.Errorf("create dir %s: %w", filepath.Dir(dest.LocalPath), err)
	}

	f, err := os.Create(dest.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("create file %s: %w", dest.LocalPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, data); err != nil {
		return nil, fmt.Errorf("write file %s: %w", dest.LocalPath, err)
	}

	s.log.Info("store: file saved",
		zap.String("local_path", dest.LocalPath),
		zap.String("relative_path", dest.RelativePath),
		zap.String("source", req.Source),
		zap.String("media_type", req.MediaType),
	)

	return dest, nil
}

// EnsureDriveFolder creates the full Drive folder hierarchy for an asset.
// Returns the final folder ID (or empty if Drive not configured).
func (s *Store) EnsureDriveFolder(ctx context.Context, req AssetDestinationRequest) (string, error) {
	rootID := s.rootForMediaType(req.MediaType)
	if s.driveUp == nil || rootID == "" {
		return "", nil
	}

	dest, err := s.resolver.Resolve(req)
	if err != nil {
		return "", err
	}

	if dest.DriveFolderPath == "" {
		return rootID, nil
	}

	// Walk the path hierarchy under root, creating folders as needed
	parts := splitPath(dest.DriveFolderPath)
	currentID := rootID
	for _, part := range parts {
		if part == "" {
			continue
		}
		id, err := s.driveUp.GetOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("folder %s under %s: %w", part, currentID, err)
		}
		currentID = id
	}
	return currentID, nil
}

// UploadToDrive uploads a local file to the resolved Drive folder.
// Returns the Drive file ID and link, or empty strings if Drive not configured.
func (s *Store) UploadToDrive(ctx context.Context, req AssetDestinationRequest, localPath string) (fileID, webLink string, err error) {
	rootID := s.rootForMediaType(req.MediaType)
	if s.driveUp == nil || rootID == "" {
		return "", "", nil
	}

	dest, err := s.resolver.Resolve(req)
	if err != nil {
		return "", "", err
	}

	folderID, err := s.EnsureDriveFolder(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("ensure drive folder: %w", err)
	}

	filename := dest.DriveFileName
	if filename == "" {
		filename = filepath.Base(localPath)
	}

	result, err := s.driveUp.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return "", "", fmt.Errorf("drive upload %s: %w", filename, err)
	}

	s.log.Info("store: drive upload",
		zap.String("file_id", result.FileID),
		zap.String("folder_id", folderID),
		zap.String("filename", filename),
	)

	return result.FileID, result.WebViewLink, nil
}

// EnsureSubfolder creates a single subfolder under a parent (or returns existing one).
func (s *Store) EnsureSubfolder(ctx context.Context, parentID, name string) (string, error) {
	if s.driveUp == nil || parentID == "" {
		return parentID, nil
	}
	return s.driveUp.GetOrCreateFolder(ctx, name, parentID)
}

// --- helpers ---

func splitPath(p string) []string {
	var result []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' || p[i] == '\\' {
			if i > start {
				result = append(result, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		result = append(result, p[start:])
	}
	return result
}
