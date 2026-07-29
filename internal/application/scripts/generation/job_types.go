package generation

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
