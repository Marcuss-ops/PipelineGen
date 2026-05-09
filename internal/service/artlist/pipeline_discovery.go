package artlist

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
)

// ensureClips retrieves clips for term, performing live search if needed
func (s *Service) ensureClips(ctx context.Context, term string, limit int, resp *RunTagResponse) ([]*models.Clip, error) {
	clipsList, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		s.log.Error("failed to search clips in DB", zap.String("term", term), zap.Error(err))
		resp.Error = "db_search_error: " + err.Error()
	}

	// Force live search if clips have invalid URLs
	hasValidURLs := false
	hasValidDriveLinks := false
	for _, clip := range clipsList {
		if clip != nil && strings.Contains(clip.ExternalURL, "artlist") && strings.Contains(clip.ExternalURL, ".m3u8") {
			hasValidURLs = true
			break
		}
		if clip != nil && clip.DriveLink != "" && strings.Contains(clip.DriveLink, "drive.google.com") {
			hasValidDriveLinks = true
		}
	}

	// If we have clips with valid Drive links, skip live search
	if hasValidDriveLinks && len(clipsList) > 0 {
		s.log.Info("clips have valid Drive links, skipping live search", zap.String("term", term), zap.Int("count", len(clipsList)))
		
		// Recover missing hashes from Drive for these clips
		checker := &artlistChecksumChecker{driveClient: s.driveSvc}
		for _, clip := range clipsList {
			if clip != nil && clip.DriveLink != "" && clip.FileHash == "" && s.driveSvc != nil {
				if md5, err := checker.GetMD5Checksum(ctx, clip.DriveLink); err == nil && md5 != "" {
					clip.FileHash = md5
					s.log.Info("recovered missing hash from drive during pipeline", zap.String("clip_id", clip.ID), zap.String("hash", md5))
					_ = s.artlistRepo.UpsertClip(ctx, clip)
				}
			}
		}
		
		return clipsList, nil
	}

	if len(clipsList) == 0 || !hasValidURLs {
		if resp.Error != "" {
			s.log.Warn("DB error occurred, attempting live search fallback", zap.String("term", term))
		} else {
			if !hasValidURLs && len(clipsList) > 0 {
				s.log.Info("found clips but with invalid URLs, forcing live search", zap.String("term", term))
			} else {
				s.log.Info("no clips found in DB for term, performing live search discovery", zap.String("term", term))
			}
		}
		searchResp, err := s.SearchLiveAndSave(ctx, term, limit*2)
		if err != nil {
			s.log.Error("live search discovery failed", zap.String("term", term), zap.Error(err))
			if resp.Error != "" {
				resp.Error = "db_error_and_live_search_failed: " + err.Error()
			}
			// If DB failed and live search failed, return error
			if strings.HasPrefix(resp.Error, "db_search_error") {
				resp.OK = false
				resp.Status = "failed"
				return nil, fmt.Errorf("failed to get clips: %s", resp.Error)
			}
		} else if searchResp != nil {
			s.log.Info("live search discovery completed", zap.String("term", term), zap.Int("found", len(searchResp.Clips)))
			resp.Error = "" // Clear DB error if live search succeeded
		}
		// Reload from DB after search
		clipsList, err = s.artlistRepo.SearchClips(ctx, term)
		if err != nil {
			s.log.Error("failed to reload clips from DB after discovery", zap.String("term", term), zap.Error(err))
			resp.OK = false
			resp.Status = "failed"
			resp.Error = "failed to reload clips after discovery: " + err.Error()
			return nil, err
		}
	}
	return clipsList, nil
}
