package artlist

import (
"context"
"fmt"
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
totalClips, err := d.svc.artlistRepo.CountAll(ctx)
if err != nil {
return nil, fmt.Errorf("failed to count clips: %w", err)
}

clipsWithFiles, err := d.svc.artlistRepo.CountWithLocalFiles(ctx)
if err != nil {
return nil, fmt.Errorf("failed to count clips with files: %w", err)
}

clipsWithDrive, err := d.svc.artlistRepo.CountWithDriveLinks(ctx)
if err != nil {
return nil, fmt.Errorf("failed to count clips with drive links: %w", err)
}

categories, err := d.svc.artlistRepo.GetCategories(ctx)
if err != nil {
return nil, fmt.Errorf("failed to get categories: %w", err)
}

return &Stats{
TotalClips:      totalClips,
ClipsWithFiles:  clipsWithFiles,
ClipsWithDrive:  clipsWithDrive,
CategoriesCount: len(categories),
}, nil
}

// Diagnostics ottiene informazioni diagnostiche per un termine specifico
func (d *DiagnosticsService) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
if term == "" {
return nil, fmt.Errorf("term is required")
}

clips := d.svc.searchService.SearchClips(ctx, term)

var clipsWithFiles, clipsWithDrive int
for _, clip := range clips {
if clip.LocalPath != "" {
clipsWithFiles++
}
if clip.DriveLink != "" {
clipsWithDrive++
}
}

return &DiagnosticsResponse{
Term:           term,
ClipsFound:     len(clips),
ClipsWithFiles: clipsWithFiles,
ClipsWithDrive: clipsWithDrive,
}, nil
}
