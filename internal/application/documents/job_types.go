package documents

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/document"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

const JobGenerate = document.TypeGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

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
