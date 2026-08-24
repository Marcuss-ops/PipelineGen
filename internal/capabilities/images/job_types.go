package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/image"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const JobGenerate = image.TypeImagesGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.CanonicalImagesGenerate
	def.Description = "image generation (URL/components -> PNG/SVG)"
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
