package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

const JobExtract = youtube.TypeExtract

type JobExtractHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobExtract,
		Description:    "youtube clip extraction (URL -> media_assets row + outbox)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
