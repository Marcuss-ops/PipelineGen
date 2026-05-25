package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

// Store is the unified entry point for all media storage operations.
// It combines:
//   - Path resolution (where to save)
//   - Local filesystem save
//   - Drive upload (with dedup)
//   - DB record creation/update
//
// All services should use Store instead of managing paths/Drive/DB directly.
type Store struct {
	resolver   *Resolver
	driveSvc   *driveapi.Service
	driveRoot  string
	log        *zap.Logger
}

// NewStore creates a unified media store.
func NewStore(resolver *Resolver, driveSvc *driveapi.Service, driveRoot string, log *zap.Logger) *Store {
	return &Store{
		resolver:  resolver,
		driveSvc:  driveSvc,
		driveRoot: driveRoot,
		log:       log,
	}
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
	if s.driveSvc == nil || s.driveRoot == "" {
		return "", nil
	}

	dest, err := s.resolver.Resolve(req)
	if err != nil {
		return "", err
	}

	if dest.DriveFolderPath == "" {
		return s.driveRoot, nil
	}

	return s.ensureDrivePath(ctx, s.driveRoot, dest.DriveFolderPath)
}

// UploadToDrive uploads a local file to the resolved Drive folder.
// Returns the Drive file ID and link, or empty strings if Drive not configured.
func (s *Store) UploadToDrive(ctx context.Context, req AssetDestinationRequest, localPath string) (fileID, webLink string, err error) {
	if s.driveSvc == nil || s.driveRoot == "" {
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

	f, err := os.Open(localPath)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	filename := dest.DriveFileName
	if filename == "" {
		filename = filepath.Base(localPath)
	}

	driveFile := &driveapi.File{
		Name:    filename,
		Parents: []string{folderID},
	}
	created, err := s.driveSvc.Files.Create(driveFile).
		Media(f).
		Fields("id,webViewLink").
		Context(ctx).
		Do()
	if err != nil {
		return "", "", fmt.Errorf("drive upload %s: %w", filename, err)
	}

	s.log.Info("store: drive upload",
		zap.String("file_id", created.Id),
		zap.String("folder_id", folderID),
		zap.String("filename", filename),
	)

	return created.Id, created.WebViewLink, nil
}

// EnsureSubfolder creates a single subfolder under a parent (or returns existing one).
func (s *Store) EnsureSubfolder(ctx context.Context, parentID, name string) (string, error) {
	if s.driveSvc == nil || parentID == "" {
		return parentID, nil
	}
	return s.getOrCreateFolder(ctx, name, parentID)
}

// ensureDrivePath walks a path like "clips/youtube/travel" under root and creates missing folders.
func (s *Store) ensureDrivePath(ctx context.Context, rootID, path string) (string, error) {
	parts := splitPath(path)
	currentID := rootID

	for _, part := range parts {
		if part == "" {
			continue
		}
		id, err := s.getOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("folder %s under %s: %w", part, currentID, err)
		}
		currentID = id
	}

	return currentID, nil
}

func (s *Store) getOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false",
		escapeName(name), parentID)
	list, err := s.driveSvc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("search folder: %w", err)
	}
	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := s.driveSvc.Files.Create(folder).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}
	return created.Id, nil
}

// --- helpers ---

func splitPath(p string) []string {
	return stringsSplit(filepath.ToSlash(p), "/")
}

func escapeName(name string) string {
	return stringsReplaceAll(name, "'", "\\'")
}

// Wrappers to avoid importing strings in tests that shadow
var stringsSplit = func(s, sep string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

var stringsReplaceAll = func(s, old, new string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result = append(result, []byte(new)...)
			i += len(old) - 1
		} else {
			result = append(result, s[i])
		}
	}
	return string(result)
}
