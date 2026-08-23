package documents

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/document"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const JobGenerate = document.TypeGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.CanonicalDocumentGenerate
	def.Description = "document generation (request -> Google Doc)"
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
