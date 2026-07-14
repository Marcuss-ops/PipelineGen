package scripts

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const JobGenerate = script.TypeGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.CanonicalScriptGenerate
	def.Description = "script generation (clips -> voiceover/script manifests)"
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
