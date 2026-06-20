package script

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// CurationJobService handles background curation jobs (script.curate).
type CurationJobService interface {
	HandleCurateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error)
}

// CatalogJobService handles background catalog-to-script generation jobs.
type CatalogJobService interface {
	HandleCatalogScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error)
}
