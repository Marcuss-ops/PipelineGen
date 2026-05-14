package artlist

import (
	"context"
	"fmt"
	"strings"
)

// DiagnosticsService fornisce statistiche e diagnostiche sul catalogo Artlist
type DiagnosticsService struct {
	svc *Service
}

// NewDiagnosticsService crea un nuovo servizio diagnostico
func NewDiagnosticsService(svc *Service) *DiagnosticsService {
	return &DiagnosticsService{svc: svc}
}

// GetStats ottiene statistiche generali sul catalogo
func (d *DiagnosticsService) GetStats(ctx context.Context) (*Stats, error) {
	totalClips, err := d.svc.artlistRepo.CountClips(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count clips: %w", err)
	}

	return &Stats{
		OK:                true,
		ClipsTotal:        totalClips,
		ArtlistClipsTotal: totalClips,
	}, nil
}

// Diagnostics ottiene informazioni diagnostiche per un termine specifico
func (d *DiagnosticsService) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	resp := &DiagnosticsResponse{
		OK:             true,
		NodeScraperDir: d.svc.nodeScraperDir,
		HasDriveClient: d.svc.assetDestResolver != nil,
		HasArtlistDB:   d.svc.artlistDB != nil,
		MainDBReady:    d.svc.mainDB != nil,
	}

	if d.svc.artlistRepo != nil {
		if total, err := d.svc.artlistRepo.CountClips(ctx); err == nil {
			resp.ClipsTotal = total
			resp.ArtlistClipsTotal = total
		}
	}

	term = strings.TrimSpace(term)
	if term != "" {
		resp.SearchTerm = term
		if matches, err := d.svc.artlistRepo.SearchClips(ctx, term); err == nil {
			resp.MatchingClips = len(matches)
			resp.EstimatedSize = len(matches)
		}

		if lastProcessedAt, err := d.svc.artlistRepo.LastUpdatedAtForTerm(ctx, term); err == nil {
			resp.LastProcessedAt = lastProcessedAt
		}
	}

	return resp, nil
}
