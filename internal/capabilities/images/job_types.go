package images

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// Canonical image job type constants.
//
// The wire strings are OWNED by internal/kernel/job (godlike/06 one owner
// per fact); these are compile-time re-exports shared with
// internal/capabilities/jobs (which cannot import this package without a
// cycle, so both alias the kernel owner instead).
const (
	TypeImagesGenerate = job.TypeImagesGenerate
	JobGenerate        = TypeImagesGenerate
	TypeGenerateGoogle = job.TypeImageGenerateGoogle
)

func MustRegister(reg job.MutableJobRegistry) error {
	return nil
}
