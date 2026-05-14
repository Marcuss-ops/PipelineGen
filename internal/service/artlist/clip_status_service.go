package artlist

import (
"context"
"fmt"
"os"
)

// ClipStatusService gestisce lo stato delle clip
type ClipStatusService struct {
svc *Service
}

// NewClipStatusService crea un nuovo servizio di stato clip
func NewClipStatusService(svc *Service) *ClipStatusService {
return &ClipStatusService{svc: svc}
}

// GetClipStatus ottiene lo stato di una clip specifica
func (s *ClipStatusService) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
if clipID == "" {
return nil, fmt.Errorf("clipID is required")
}

clip, err := s.svc.artlistRepo.GetByClipID(ctx, clipID)
if err != nil {
return nil, fmt.Errorf("failed to get clip: %w", err)
}

if clip == nil {
return nil, fmt.Errorf("clip not found: %s", clipID)
}

resp := &ClipStatusResponse{
ClipID:      clip.ClipID,
Name:        clip.Title,
Source:      clip.Source,
ExternalURL: clip.PrimaryURL,
}

if clip.LocalPath != "" {
if _, err := os.Stat(clip.LocalPath); err == nil {
resp.HasLocalFile = true
resp.LocalPath = clip.LocalPath
}
}

if clip.DriveLink != "" {
resp.HasDriveLink = true
resp.DriveLink = clip.DriveLink
}

if clip.FileHash != "" {
resp.FileHash = clip.FileHash
}

return resp, nil
}
