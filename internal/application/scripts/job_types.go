package scripts

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobGenerate is the canonical application-side alias for the
// script job-type identifier. Wire value lifted from
// domain/script.TypeGenerate per godlike/02 SSOT.
const JobGenerate = script.TypeGenerate

// JobGenerateHandlerFunc is the canonical JobHandlerFunc shape.
type JobGenerateHandlerFunc = job.JobHandlerFunc

// MustRegister wires scripts.JobGenerate into the given registry.
func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobGenerate,
		Description:    "script generation (clips -> voiceover/script manifests)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
