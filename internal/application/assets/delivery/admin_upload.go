package delivery

import (
	"context"
	"fmt"
	"strings"
)

// AdminUploadService is the application use case for an operator-directed
// upload into an already-resolved Drive folder. It keeps the CLI from
// choosing Drive write algorithms or calling infrastructure ports directly.
type AdminUploadService struct {
	publisher Publisher
}

// AdminUploadCommand describes one explicit-folder administrative publish.
type AdminUploadCommand struct {
	LocalPath   string
	FolderID    string
	Filename    string
	Description string
}

// NewAdminUploadService constructs the admin upload use case.
func NewAdminUploadService(publisher Publisher) (*AdminUploadService, error) {
	if publisher == nil {
		return nil, fmt.Errorf("delivery: admin upload publisher is required")
	}
	return &AdminUploadService{publisher: publisher}, nil
}

// Publish sends the file through the canonical delivery.Publisher. The
// explicit folder is already resolved by the command's operator input, so
// the publisher must not consult a configured root or invent path segments.
func (s *AdminUploadService) Publish(ctx context.Context, cmd AdminUploadCommand) (*PublishResult, error) {
	if s == nil || s.publisher == nil {
		return nil, fmt.Errorf("delivery: admin upload service is not wired")
	}
	if strings.TrimSpace(cmd.LocalPath) == "" {
		return nil, fmt.Errorf("delivery: admin upload local path is required")
	}
	if strings.TrimSpace(cmd.FolderID) == "" {
		return nil, fmt.Errorf("delivery: admin upload folder ID is required")
	}
	if strings.TrimSpace(cmd.Filename) == "" {
		return nil, fmt.Errorf("delivery: admin upload filename is required")
	}

	result, err := s.publisher.Publish(ctx, PublishRequest{
		Destination:         DestinationAdmin,
		LocalPath:           cmd.LocalPath,
		Filename:            cmd.Filename,
		Description:         cmd.Description,
		DestinationFolderID: cmd.FolderID,
		ConflictPolicy:      ConflictOverwrite,
	})
	if err != nil {
		return nil, fmt.Errorf("delivery: admin upload %q: %w", cmd.Filename, err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return nil, fmt.Errorf("delivery: admin upload %q returned no Drive file ID", cmd.Filename)
	}
	return result, nil
}
