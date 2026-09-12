package voiceover

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Voiceover job-type constants.
//
// The wire strings are OWNED by internal/kernel/job (godlike/06 one owner per
// fact). These are compile-time re-exports of the KERNEL origin, not of the
// sibling capabilities/voiceover leaf package: an alias-of-alias chain makes a
// rename in the kernel leaf silently useless and amplifies every change across
// two capability layers. Capability packages reference the kernel constant
// directly (P2-15, September 2026).
const (
	TypeGenerate     = job.TypeVoiceoverGenerate
	TypeBatch        = job.TypeVoiceoverBatch
	TypeGenerateItem = job.TypeVoiceoverGenerateItem
	TypePromo        = job.TypeVoiceoverPromo
)

const JobGenerate = job.TypeVoiceoverGenerate

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
