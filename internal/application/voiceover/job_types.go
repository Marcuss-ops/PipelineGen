package voiceover

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobGenerate is the canonical application-side alias for the
// voiceover job-type identifier. Wire value lifted from
// domain/voiceover.TypeGenerate per godlike/02 SSOT.
const JobGenerate = voiceover.TypeGenerate

// JobGenerateHandlerFunc is the canonical JobHandlerFunc shape.
type JobGenerateHandlerFunc = job.JobHandlerFunc

// MustRegister wires voiceover.JobGenerate into the given registry.
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
