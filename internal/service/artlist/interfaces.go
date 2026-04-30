package artlist

import "context"

// CandidateSearcher defines the interface for searching clip candidates
type CandidateSearcher interface {
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// ClipProcessor defines the interface for processing individual clips
type ClipProcessor interface {
	ProcessClip(ctx context.Context, req *ProcessClipRequest) (*ProcessClipResponse, error)
	DownloadClip(ctx context.Context, clipID string, req *DownloadClipRequest) (*DownloadClipResponse, error)
	UploadClipToDrive(ctx context.Context, clipID string, req *UploadClipToDriveRequest) (*UploadClipToDriveResponse, error)
}

// RunOrchestrator defines the interface for orchestrating run execution
type RunOrchestrator interface {
	StartRunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error)
	GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error)
	RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error)
}
