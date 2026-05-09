package artlist

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/pkg/models"
)

// processDryRun simulates processing without real downloads
func (s *Service) processDryRun(ctx context.Context, candidates []*models.Clip, resp *RunTagResponse) {
	s.log.Info("dry-run mode, simulating pipeline", zap.String("term", resp.Term), zap.Int("candidates", len(candidates)))
	for _, clip := range candidates {
		if clip == nil {
			continue
		}
		status := "would_process"
		if s.lifecycleService != nil {
			input := &lifecycle.FinalizeInput{
				ID:           clip.ID,
				Name:         clip.Name,
				Filename:     clip.Filename,
				Kind:         lifecycle.AssetKindVideo,
				Source:       "artlist",
				LocalPath:    clip.LocalPath,
				DriveLink:    clip.DriveLink,
				FileHash:     clip.FileHash,
				RequireLocal: true,
				RequireHash:  true,
				RequireDrive: clip.DriveLink != "",
				VerifyDB:     true,
			}
			result, err := s.lifecycleService.CheckDuplicate(ctx, input, clip.FileHash)
			if err != nil {
				status = "would_skip"
				resp.WouldSkip++
				s.log.Info("would skip clip", zap.String("clip_id", clip.ID), zap.Error(err))
			} else if result.Status == "would_skip_duplicate" {
				status = "would_skip_duplicate"
				resp.WouldSkip++
			} else {
				resp.WouldProcess++
			}
		} else {
			resp.WouldProcess++
		}
		resp.Items = append(resp.Items, RunTagItem{
			ClipID: clip.ID,
			Name:   clip.Name,
			Status: status,
			DriveFileID:  clip.DriveFileID,
		})
	}
	resp.Status = "completed_dry_run"
	resp.OK = true
}
