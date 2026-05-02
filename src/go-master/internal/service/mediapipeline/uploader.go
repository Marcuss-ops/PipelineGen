package mediapipeline

import (
	"context"
	"fmt"
	"path/filepath"
)

func (s *Service) uploadItem(ctx context.Context, item *WorkItem, dest *ResolvedDestination) error {
	if s.driveUploader == nil {
		return fmt.Errorf("drive uploader not configured")
	}

	if item.ProcessedPath == "" {
		item.ProcessedPath = item.LocalPath
	}

	if item.ProcessedPath == "" {
		return fmt.Errorf("no file path to upload")
	}

	filename := filepath.Base(item.ProcessedPath)
	if item.Name != "" {
		ext := filepath.Ext(item.ProcessedPath)
		filename = item.Name + ext
	}

	result, err := s.driveUploader.UploadFile(ctx, item.ProcessedPath, dest.FolderID, filename)
	if err != nil {
		return fmt.Errorf("failed to upload to drive: %w", err)
	}

	item.DriveLink = result.WebViewLink
	return nil
}

func (s *Service) getOrCreateSubfolder(ctx context.Context, parentID, subfolderName string) (string, error) {
	if s.driveUploader == nil {
		return "", fmt.Errorf("drive uploader not configured")
	}

	return s.driveUploader.GetOrCreateFolder(ctx, subfolderName, parentID)
}
