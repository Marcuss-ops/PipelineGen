package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const JobBulkUpload = media.TypeBulkUploadYouTubeClips

type JobBulkUploadHandlerFunc = job.JobHandlerFunc

func MustRegister(reg job.MutableJobRegistry) error {
	def := job.JobDefinition{
		Type:           JobBulkUpload,
		Description:    "bulk upload (youtube-clips -> Drive mirroring + DB upsert)",
		ExecutionClass: job.ExecutionCreatorAllowed,
		Queue:          "default",
		RequiredCapabilities: []job.Capability{
			"media.bulk_upload",
			"drive.write",
		},
	}
	if err := reg.RegisterDefinition(def); err != nil {
		return err
	}
	return nil
}
