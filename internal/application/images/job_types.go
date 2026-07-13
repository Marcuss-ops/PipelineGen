package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/image"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobGenerate is the canonical application-side alias for the
// image job-type identifier. The wire value is held by the
// domain package (image.TypeImagesGenerate = "images.generate");
// this alias exposes the user-facing identifier on the
// application surface (per PR-JOB-TYPE-OWNER-LOCKS, July 2026).
//
// godlike/06 SSOT: image.TypeImagesGenerate is the SOLE canonical
// home of the "images.generate" wire value; this file carries a
// single-name typed alias. Both names share identity at compile
// time (Go const alias rules).
const JobGenerate = image.TypeImagesGenerate

// JobGenerateHandlerFunc is the canonical JobHandlerFunc shape
// for image-generation jobs.
type JobGenerateHandlerFunc = job.JobHandlerFunc

// MustRegister wires images.JobGenerate into the given registry.
func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobGenerate,
		Description:    "image generation (URL/components -> PNG/SVG)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
