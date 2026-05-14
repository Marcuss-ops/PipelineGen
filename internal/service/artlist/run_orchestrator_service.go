package artlist

import (
"context"
"fmt"
"strings"
"time"

"velox/go-master/pkg/models"
)

// RunOrchestratorService coordina l'esecuzione dei run Artlist
type RunOrchestratorService struct {
svc *Service
}

// NewRunOrchestratorService crea un nuovo orchestratore di run
func NewRunOrchestratorService(svc *Service) *RunOrchestratorService {
return &RunOrchestratorService{svc: svc}
}

// GetRunTag ottiene lo stato di un run esistente
func (o *RunOrchestratorService) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
if runID == "" {
return nil, fmt.Errorf("runID is required")
}

job, err := o.svc.jobAdapter.GetJobByRunID(ctx, runID)
if err != nil {
return nil, fmt.Errorf("failed to get job for run %s: %w", runID, err)
}

if job == nil {
return nil, fmt.Errorf("job not found for run %s", runID)
}

resp := &RunTagResponse{
OK:       true,
RunID:    runID,
Term:     job.ActiveKey,
Status:   string(job.Status),
Metadata: job.Metadata,
}

return resp, nil
}

// RunTag esegue la pipeline Artlist per un termine di ricerca
func (o *RunOrchestratorService) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
resp := &RunTagResponse{
OK:        true,
Term:      strings.TrimSpace(req.Term),
StartedAt: func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }(),
}

if resp.Term == "" {
resp.OK = false
resp.Error = "term is required"
return resp, fmt.Errorf("term is required")
}

rootFolderID := req.RootFolderID
if rootFolderID == "" && o.svc.cfg != nil {
rootFolderID = strings.TrimSpace(o.svc.cfg.Harvester.DriveFolderID)
}

dest, err := o.svc.destinationService.ResolveDestination(ctx, resp.Term, rootFolderID)
if err != nil {
resp.OK = false
resp.Error = fmt.Sprintf("failed to resolve destination: %v", err)
return resp, err
}
resp.DestinationFolderID = dest.FolderID
resp.DestinationFolderPath = dest.FolderPath

discoveryResp, err := o.svc.searchService.DiscoverClipsForTerm(ctx, resp.Term, req.Limit)
if err != nil {
resp.OK = false
resp.Error = fmt.Sprintf("discovery failed: %v", err)
return resp, err
}
resp.CandidatesDiscovered = discoveryResp.Count
resp.Candidates = discoveryResp.Clips

if len(resp.Candidates) == 0 {
resp.OK = false
resp.Error = "no candidates found"
return resp, nil
}

processedCount := 0
for _, clip := range resp.Candidates {
if err := o.svc.clipProcessor.ProcessClip(ctx, clip, dest); err != nil {
o.svc.log.Warn("clip processing failed", zap.String("clip_id", clip.ClipID), zap.Error(err))
continue
}
processedCount++
}
resp.ClipsProcessed = processedCount

resp.CompletedAt = func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }()

return resp, nil
}
