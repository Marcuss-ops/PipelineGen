package mediapipeline

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/models"
)

func (s *Service) persistItem(ctx context.Context, item *WorkItem, req *PipelineRequest, dest *ResolvedDestination) error {
	if s.clipsRepo == nil {
		return fmt.Errorf("clips repository not configured")
	}

	clip := &models.Clip{
		ID:           item.ID,
		Name:         item.Name,
		Filename:     item.Name + ".mp4",
		Source:       req.Source,
		MediaType:    req.MediaType,
		Category:     req.Category,
		ExternalURL:  item.SourceURL,
		DriveLink:    item.DriveLink,
		DownloadLink: item.SourceURL,
		Tags:         item.Tags,
		FileHash:     item.FileHash,
		LocalPath:    item.ProcessedPath,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if dest != nil {
		clip.FolderID = dest.FolderID
		clip.FolderPath = dest.FolderPath
		clip.Group = dest.Group
	}

	if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
		return fmt.Errorf("failed to persist clip: %w", err)
	}

	return nil
}

func (s *Service) hashItem(item *WorkItem) error {
	path := item.ProcessedPath
	if path == "" {
		path = item.LocalPath
	}

	if path == "" {
		return fmt.Errorf("no file path to hash")
	}

	hash, err := hashutil.MD5File(path)
	if err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	item.FileHash = hash
	return nil
}
