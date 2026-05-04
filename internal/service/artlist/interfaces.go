package artlist

import "context"

// CandidateSearcher defines the interface for searching clip candidates
type CandidateSearcher interface {
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// RunOrchestrator defines the interface for orchestrating run execution
type RunOrchestrator interface {
	GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error)
	RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error)
}
