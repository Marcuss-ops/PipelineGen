package voiceover

import (
	skinny "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Re-export canonical job type constants from the skinny domain-types
// leaf package so callers of this package don't need a separate import.
const (
	TypeGenerate     = skinny.TypeGenerate
	TypeBatch        = skinny.TypeBatch
	TypeGenerateItem = skinny.TypeGenerateItem
	TypePromo        = skinny.TypePromo
)

const JobGenerate = skinny.TypeGenerate

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
