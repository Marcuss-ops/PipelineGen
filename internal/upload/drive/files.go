package drive

import (
	"context"
	"fmt"

	"google.golang.org/api/drive/v3"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// ShareFile condivide un file
func (c *Client) ShareFile(ctx context.Context, fileID, email string, role string) error {
	permission := &drive.Permission{
		Type:         "user",
		Role:         role,
		EmailAddress: email,
	}

	_, err := c.service.Permissions.Create(fileID, permission).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to share file: %w", err)
	}

	logger.Info("Shared Drive file", zap.String("file_id", fileID), zap.String("email", email))
	return nil
}

// DeleteFile elimina un file
func (c *Client) DeleteFile(ctx context.Context, fileID string) error {
	err := c.service.Files.Delete(fileID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	logger.Info("Deleted Drive file", zap.String("file_id", fileID))
	return nil
}

// SearchFiles cerca file per nome all'interno di una cartella
func (c *Client) SearchFiles(ctx context.Context, namePattern, folderID string, videoOnly bool) ([]File, error) {
	query := fmt.Sprintf("name contains '%s' and '%s' in parents and trashed=false", namePattern, folderID)
	if videoOnly {
		query += " and (mimeType contains 'video' or mimeType contains 'mp4' or mimeType contains 'quicktime')"
	}

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, mimeType, webViewLink, size, modifiedTime, videoMediaMetadata, createdTime)").
		PageSize(20).
		OrderBy("modifiedTime desc").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to search files: %w", err)
	}

	var files []File
	for _, f := range result.Files {
		file := File{
			ID:           f.Id,
			Name:         f.Name,
			MimeType:     f.MimeType,
			Link:         f.WebViewLink,
			Size:         f.Size,
			ModifiedTime: parseTime(f.ModifiedTime),
			CreatedTime:  parseTime(f.CreatedTime),
		}

		if f.VideoMediaMetadata != nil {
			file.DurationMs = f.VideoMediaMetadata.DurationMillis
			file.Width = f.VideoMediaMetadata.Width
			file.Height = f.VideoMediaMetadata.Height
		}

		files = append(files, file)
	}

	return files, nil
}

// GetFile ottiene informazioni su un file
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	result, err := c.service.Files.Get(fileID).
		Fields("id, name, mimeType, webViewLink, size, modifiedTime, parents").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return &File{
		ID:           result.Id,
		Name:         result.Name,
		MimeType:     result.MimeType,
		Link:         result.WebViewLink,
		Size:         result.Size,
		ModifiedTime: parseTime(result.ModifiedTime),
		Parents:      result.Parents,
	}, nil
}
