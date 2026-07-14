package voiceover

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

const JobGenerate = voiceover.TypeGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobGenerate,
		Description:    "voiceover generation (script + lang -> TTS audio)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
