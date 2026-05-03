package artlist

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/drivedestination"
)

type DriveService struct {
	driveClient      *driveapi.Service
	driveFolderID    string
	driveDestination *drivedestination.Service
	log              *zap.Logger
}

func NewDriveService(
	driveClient *driveapi.Service,
	driveFolderID string,
	driveDestination *drivedestination.Service,
	log *zap.Logger,
) *DriveService {
	return &DriveService{
		driveClient:      driveClient,
		driveFolderID:    driveFolderID,
		driveDestination: driveDestination,
		log:              log,
	}
}

// GetDriveDestination returns the drive destination service
func (s *DriveService) GetDriveDestination() *drivedestination.Service {
	return s.driveDestination
}

// UploadFile uploads a file to the specified Drive folder
func (s *DriveService) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	if s.driveClient == nil {
		return "", fmt.Errorf("drive client not configured")
	}

	file := &driveapi.File{
		Name: filename,
	}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	// Open local file for upload
	fileHandle, err := os.Open(filePath)
	if err != nil {
		s.log.Error("failed to open file for upload", zap.String("file_path", filePath), zap.Error(err))
		return "", fmt.Errorf("failed to open local file: %w", err)
	}
	defer fileHandle.Close()

	start := time.Now()
	s.log.Info("uploading file to drive",
		zap.String("file_path", filePath),
		zap.String("folder_id", folderID),
		zap.String("filename", filename),
	)

	created, err := s.driveClient.Files.Create(file).
		Media(fileHandle).
		Context(ctx).
		Do()

	if err != nil {
		s.log.Error("failed to upload file",
			zap.String("file_path", filePath),
			zap.Error(err),
		)
		return "", err
	}

	s.log.Info("file uploaded successfully",
		zap.String("file_id", created.Id),
		zap.Duration("duration", time.Since(start)),
	)

	return created.WebViewLink, nil
}

// CreateFolder creates a folder on Drive
func (s *DriveService) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.driveClient == nil {
		return "", fmt.Errorf("drive client not configured")
	}

	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}

	created, err := s.driveClient.Files.Create(folder).
		Context(ctx).
		Do()

	if err != nil {
		s.log.Error("failed to create folder",
			zap.String("name", name),
			zap.String("parent_id", parentID),
			zap.Error(err),
		)
		return "", err
	}

	s.log.Info("folder created",
		zap.String("folder_id", created.Id),
		zap.String("name", name),
	)

	return created.Id, nil
}

// GetOrCreateFolder gets an existing folder or creates it
func (s *DriveService) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.driveClient == nil {
		return "", fmt.Errorf("drive client not configured")
	}

	// Search for existing folder
	escapedParent := escapeDriveQueryValue(parentID)
	escapedName := escapeDriveQueryValue(name)
	query := fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and '%s' in parents and name='%s' and trashed=false", escapedParent, escapedName)
	fileList, err := s.driveClient.Files.List().
		Q(query).
		Fields("files(id, name)").
		Context(ctx).
		Do()

	if err != nil {
		s.log.Error("failed to search for folder",
			zap.String("name", name),
			zap.Error(err),
		)
		return "", err
	}

	// Return existing folder if found
	if len(fileList.Files) > 0 {
		s.log.Info("found existing folder",
			zap.String("folder_id", fileList.Files[0].Id),
			zap.String("name", name),
		)
		return fileList.Files[0].Id, nil
	}

	// Create new folder
	return s.CreateFolder(ctx, name, parentID)
}

// escapeDriveQueryValue escapes a string for use in Drive API query strings
func escapeDriveQueryValue(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// DownloadFile downloads a file from Drive by file ID
func (s *DriveService) DownloadFile(ctx context.Context, fileID string) (*http.Response, error) {
	if s.driveClient == nil {
		return nil, fmt.Errorf("drive client not configured")
	}
	return s.driveClient.Files.Get(fileID).Context(ctx).Download()
}

// GetDriveClient returns the underlying Drive API client (temporary for refactoring)
func (s *DriveService) GetDriveClient() *driveapi.Service {
	return s.driveClient
}

// GetDriveFolderID returns the configured root Drive folder ID
func (s *DriveService) GetDriveFolderID() string {
	return s.driveFolderID
}
