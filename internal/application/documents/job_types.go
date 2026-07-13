package documents

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/document"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobGenerate is the canonical application-side alias for the
// document job-type identifier. Wire value lifted from
// domain/document.TypeGenerate per godlike/02 SSOT.
const JobGenerate = document.TypeGenerate

// JobGenerateHandlerFunc is the canonical JobHandlerFunc shape.
type JobGenerateHandlerFunc = job.JobHandlerFunc

// MustRegister wires documents.JobGenerate into the given registry.
func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobGenerate,
		Description:    "document generation (request -> Google Doc)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
