package script

import (
	"context"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// CurationJobService handles background curation jobs (script.curate).
type CurationJobService interface {
	HandleCurateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error)
}

// CatalogJobService handles background catalog-to-script generation jobs.
type CatalogJobService interface {
	HandleCatalogScriptGenerateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error)
}
