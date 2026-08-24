package images

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// Canonical image job type constants.
const (
	TypeImagesGenerate = "images.generate"
	JobGenerate        = TypeImagesGenerate
	TypeGenerateGoogle = "image.generate.google"
)

func MustRegister(reg job.MutableJobRegistry) error {
	return nil
}
