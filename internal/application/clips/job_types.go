package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobBulkUpload is the canonical application-side alias for the
// clip bulk-upload job-type identifier. Wire value lifted from
// domain/media.TypeBulkUploadYouTubeClips per godlike/02 SSOT.
//
// The constant NAME exposes the user-facing identifier; the wire
// VALUE preserves the pre-cutover canonical so in-flight jobs
// / orchestration records continue to dispatch.
const JobBulkUpload = media.TypeBulkUploadYouTubeClips

// JobBulkUploadHandlerFunc is the canonical JobHandlerFunc shape.
type JobBulkUploadHandlerFunc = job.JobHandlerFunc

// MustRegister wires clips.JobBulkUpload into the given registry.
func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobBulkUpload,
		Description:    "bulk upload (youtube-clips -> Drive mirroring + DB upsert)",
		ExecutionClass: job.ExecutionCreatorAllowed,
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
