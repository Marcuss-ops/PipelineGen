package youtube

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
)

const JobExtract = youtube.TypeClipExtract

type JobExtractHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobExtract,
		Description:    "youtube clip extraction (URL -> media_assets row + outbox)",
		ExecutionClass: job.ExecutionCreatorAllowed,
		Queue:          "default",
		RequiredCapabilities: []job.Capability{
			"media.clip.extract",
		},
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
