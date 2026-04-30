package artlist

import (
	"context"

	"velox/go-master/internal/common/pipeline"
)

// ArtlistPipelineAdapter adapts the Artlist Service to the pipeline interfaces
type ArtlistPipelineAdapter struct {
	service *Service
}

// NewArtlistPipelineAdapter creates a new adapter for the Artlist Service
func NewArtlistPipelineAdapter(service *Service) *ArtlistPipelineAdapter {
	return &ArtlistPipelineAdapter{service: service}
}

// Search implements pipeline.CandidateSearcher
func (a *ArtlistPipelineAdapter) Search(ctx context.Context, term string, limit int) ([]pipeline.ClipCandidate, error) {
	resp, err := a.service.Search(ctx, &SearchRequest{
		Term:    term,
		Limit:   limit,
		PreferDB: true,
		SaveDB:   true,
	})
	if err != nil {
		return nil, err
	}

	candidates := make([]pipeline.ClipCandidate, 0, len(resp.Clips))
	for _, clip := range resp.Clips {
		candidates = append(candidates, pipeline.ClipCandidate{
			ID:       clip.ID,
			Name:     clip.Name,
			Source:   clip.Source,
			Tags:     clip.Tags,
			Duration: clip.Duration,
		})
	}

	return candidates, nil
}

// ProcessClip implements pipeline.ClipProcessor
func (a *ArtlistPipelineAdapter) ProcessClip(ctx context.Context, clipID string, opts pipeline.ProcessOptions) (*pipeline.ClipResult, error) {
	resp, err := a.service.ProcessClip(ctx, &ProcessClipRequest{
		ClipID:       clipID,
		AutoDownload: opts.AutoDownload,
		AutoUpload:   opts.AutoUpload,
	})
	if err != nil {
		return &pipeline.ClipResult{
			ClipID: clipID,
			Status: "failed",
			Error:  err.Error(),
		}, err
	}

	if resp == nil {
		return &pipeline.ClipResult{
			ClipID: clipID,
			Status: "failed",
		}, nil
	}

	status := resp.Status
	if !resp.OK {
		status = "failed"
	}

	return &pipeline.ClipResult{
		ClipID:    resp.ClipID,
		Name:      "",
		Status:    status,
		DriveLink: "",
		FileHash:  "",
		Error:     resp.Error,
	}, nil
}

// NewPipelineRunner creates a new PipelineRunner using the Artlist Service
func (s *Service) NewPipelineRunner() *pipeline.PipelineRunner {
	adapter := NewArtlistPipelineAdapter(s)
	return pipeline.NewPipelineRunner(adapter, adapter, s.log)
}

// RunTagWithPipeline runs the tag pipeline using the generic pipeline runner
func (s *Service) RunTagWithPipeline(ctx context.Context, req *RunTagRequest) (*pipeline.PipelineResponse, error) {
	runner := s.NewPipelineRunner()

	pipelineReq := &pipeline.PipelineRequest{
		Term:         req.Term,
		Limit:        req.Limit,
		RootFolderID: req.RootFolderID,
		Strategy:     req.Strategy,
		DryRun:       req.DryRun,
		AutoDownload: true,
		AutoUpload:   true,
	}

	return runner.Run(ctx, pipelineReq)
}

// ConvertPipelineResponse converts a pipeline response to a RunTagResponse
func ConvertPipelineResponse(pipelineResp *pipeline.PipelineResponse, runID string) *RunTagResponse {
	items := make([]RunTagItem, 0, len(pipelineResp.Results))
	for _, r := range pipelineResp.Results {
		items = append(items, RunTagItem{
			ClipID:    r.ClipID,
			Name:      r.Name,
			Status:    r.Status,
			DriveLink: r.DriveLink,
			FileHash:  r.FileHash,
			Error:     r.Error,
		})
	}

	return &RunTagResponse{
		OK:            pipelineResp.OK,
		RunID:         runID,
		Term:           pipelineResp.Term,
		Status:         "completed",
		Found:          pipelineResp.Found,
		Processed:      pipelineResp.Processed,
		Skipped:        pipelineResp.Skipped,
		Failed:         pipelineResp.Failed,
		Items:          items,
		StartedAt:      pipelineResp.StartedAt,
		EndedAt:        pipelineResp.EndedAt,
	}
}
