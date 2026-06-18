package jobs

import "github.com/Marcuss-ops/PipelineGen/internal/media/models"

type JobType = models.JobType

const (
	JobTypeBatchScriptGenerate   JobType = models.JobTypeBatchScriptGenerate
	JobTypeClipScriptGenerate    JobType = models.JobTypeClipScriptGenerate
	JobTypeCatalogScriptGenerate JobType = models.JobTypeCatalogScriptGenerate
	JobTypeMediaCurate           JobType = "media.curate"
)
