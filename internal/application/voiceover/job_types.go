package voiceover

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const JobGenerate = voiceover.TypeGenerate

type JobGenerateHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobGenerate,
		Description:    "voiceover generation (script + lang -> TTS audio)",
		ExecutionClass: job.ExecutionCreatorAllowed,
		Queue:          "default",
		RequiredCapabilities: []job.Capability{
			"voiceover.generate",
		},
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
