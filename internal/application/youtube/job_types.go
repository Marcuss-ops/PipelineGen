package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobExtract is the canonical application-side alias for the
// youtube job-type identifier. Wire value lifted from
// domain/youtube.TypeExtract per godlike/02 SSOT.
const JobExtract = youtube.TypeExtract

// JobExtractHandlerFunc is the canonical JobHandlerFunc shape.
type JobExtractHandlerFunc = job.JobHandlerFunc

// MustRegister wires youtube.JobExtract into the given registry.
func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobExtract,
		Description:    "youtube clip extraction (URL -> media_assets row + outbox)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
