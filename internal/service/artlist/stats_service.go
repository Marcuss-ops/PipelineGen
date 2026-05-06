package artlist

import (
	"context"
	"strings"
)

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{OK: true}

	if s.artlistRepo != nil {
		if totalClips, err := s.artlistRepo.CountClips(ctx); err == nil {
			stats.ClipsTotal = totalClips
			stats.ArtlistClipsTotal = totalClips
		}
	}

	return stats, nil
}

func (s *Service) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	resp := &DiagnosticsResponse{
		OK:             true,
		RootFolderID:   "",
		DriveFolderID:  "",
		NodeScraperDir: s.nodeScraperDir,
		HasDriveClient: s.assetDestResolver != nil,
		HasArtlistDB:   s.artlistDB != nil,
		MainDBReady:    s.mainDB != nil,
	}

	if s.artlistRepo != nil {
		if total, err := s.artlistRepo.CountClips(ctx); err == nil {
			resp.ClipsTotal = total
			resp.ArtlistClipsTotal = total
		}
	}

	term = strings.TrimSpace(term)
	if term != "" {
		resp.SearchTerm = term
		if matches, err := s.artlistRepo.SearchClips(ctx, term); err == nil {
			resp.MatchingClips = len(matches)
			resp.EstimatedSize = len(matches)
		}
	}

	if lastProcessedAt, err := s.lastProcessedAtForTerm(ctx, term); err == nil {
		resp.LastProcessedAt = lastProcessedAt
	}

	return resp, nil
}

func (s *Service) lastProcessedAtForTerm(ctx context.Context, term string) (*string, error) {
	if s.artlistRepo == nil {
		return nil, nil
	}

	return s.artlistRepo.LastUpdatedAtForTerm(ctx, term)
}
