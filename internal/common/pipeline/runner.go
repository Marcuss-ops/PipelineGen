package pipeline

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
)

// CandidateSearcher defines the interface for searching clip candidates
type CandidateSearcher interface {
	Search(ctx context.Context, term string, limit int) ([]ClipCandidate, error)
}

// ClipProcessor defines the interface for processing individual clips
type ClipProcessor interface {
	ProcessClip(ctx context.Context, clipID string, opts ProcessOptions) (*ClipResult, error)
}

// ClipCandidate represents a clip candidate from search
type ClipCandidate struct {
	ID       string
	Name     string
	Source   string
	Tags     []string
	Duration int
}

// ProcessOptions defines options for processing a clip
type ProcessOptions struct {
	AutoDownload bool
	AutoUpload   bool
	DryRun       bool
	Strategy     string
}

// ClipResult represents the result of processing a clip
type ClipResult struct {
	ClipID    string
	Name      string
	Status    string
	DriveLink string
	FileHash  string
	Error     string
}

// PipelineRequest represents a pipeline execution request
type PipelineRequest struct {
	Term         string
	Limit        int
	RootFolderID string
	Strategy     string
	DryRun       bool
	AutoDownload bool
	AutoUpload   bool
}

// PipelineResponse represents a pipeline execution response
type PipelineResponse struct {
	OK            bool
	Term          string
	Requested     int
	Found         int
	Processed     int
	Skipped       int
	Failed        int
	Results       []ClipResult
	Error         string
	StartedAt     *string
	EndedAt       *string
}

// PipelineRunner orchestrates the clip processing pipeline
type PipelineRunner struct {
	searcher  CandidateSearcher
	processor ClipProcessor
	log       *zap.Logger
}

// NewPipelineRunner creates a new PipelineRunner
func NewPipelineRunner(searcher CandidateSearcher, processor ClipProcessor, log *zap.Logger) *PipelineRunner {
	return &PipelineRunner{
		searcher:  searcher,
		processor: processor,
		log:       log,
	}
}

// Run executes the full pipeline: search -> process each clip
func (r *PipelineRunner) Run(ctx context.Context, req *PipelineRequest) (*PipelineResponse, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	resp := &PipelineResponse{
		OK:        true,
		Term:       req.Term,
		StartedAt:  &now,
		Results:    []ClipResult{},
	}

	// Search for candidates
	candidates, err := r.searcher.Search(ctx, req.Term, req.Limit)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	resp.Found = len(candidates)
	resp.Requested = req.Limit

	// Process each candidate
	for _, candidate := range candidates {
		result, err := r.processor.ProcessClip(ctx, candidate.ID, ProcessOptions{
			AutoDownload: req.AutoDownload,
			AutoUpload:   req.AutoUpload,
			DryRun:       req.DryRun,
			Strategy:     req.Strategy,
		})

		if err != nil || (result != nil && result.Status == "failed") {
			resp.Failed++
			r.log.Warn("failed to process clip",
				zap.String("clip_id", candidate.ID),
				zap.Error(err),
			)
			if result == nil {
				result = &ClipResult{
					ClipID: candidate.ID,
					Name:   candidate.Name,
					Status: "failed",
					Error:  err.Error(),
				}
			}
			resp.Results = append(resp.Results, *result)
			continue
		}

		if result != nil {
			if result.Status == "skipped" {
				resp.Skipped++
			} else {
				resp.Processed++
			}
			resp.Results = append(resp.Results, *result)
		}
	}

	endedAt := time.Now().UTC().Format(time.RFC3339)
	resp.EndedAt = &endedAt

	return resp, nil
}

// RunWithJob executes the pipeline and updates a Job model
func (r *PipelineRunner) RunWithJob(ctx context.Context, job *models.Job, req *PipelineRequest) (*PipelineResponse, error) {
	resp, err := r.Run(ctx, req)

	// Update job with results
	if resp != nil {
		job.Result = map[string]interface{}{
			"found":     resp.Found,
			"processed": resp.Processed,
			"skipped":   resp.Skipped,
			"failed":    resp.Failed,
		}
	}

	if err != nil {
		job.Status = models.StatusFailed
		job.Error = err.Error()
	} else if resp.OK {
		job.Status = models.StatusCompleted
	}

	return resp, err
}
