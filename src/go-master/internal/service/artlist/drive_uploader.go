package artlist

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"

	"velox/go-master/internal/core/interfaces"
)

// Ensure Service implements interfaces.DriveUploader
var _ interfaces.DriveUploader = (*Service)(nil)

// UploadFile uploads a file to the specified Drive folder
func (s *Service) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	if s.driveClient == nil {
		s.log.Error("drive client not configured")
		return "", nil
	}

	file := &drive.File{
		Name: filename,
	}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	start := time.Now()
	s.log.Info("uploading file to drive",
		zap.String("file_path", filePath),
		zap.String("folder_id", folderID),
		zap.String("filename", filename),
	)

	created, err := s.driveClient.Files.Create(file).
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
func (s *Service) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.driveClient == nil {
		s.log.Error("drive client not configured")
		return "", nil
	}

	folder := &drive.File{
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
func (s *Service) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.driveClient == nil {
		s.log.Error("drive client not configured")
		return "", nil
	}

	// Search for existing folder
	query := "mimeType='application/vnd.google-apps.folder' and '" + parentID + "' in parents and name='" + name + "' and trashed=false"
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
