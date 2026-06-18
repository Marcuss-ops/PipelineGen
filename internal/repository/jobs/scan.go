package jobs

import (
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// unmarshalJobFields deserializza i campi JSON e temporali di un job dopo una Scan.
func unmarshalJobFields(job *models.Job, payloadJSON, resultJSON string, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string) {
	if len(payloadJSON) > 0 {
		json.Unmarshal([]byte(payloadJSON), &job.Payload)
	}
	if len(resultJSON) > 0 {
		json.Unmarshal([]byte(resultJSON), &job.Result)
	}
	job.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
	if t := timeutil.ParseRFC3339Ptr(timeutil.DerefString(createdAt)); t != nil {
		job.CreatedAt = *t
	}
	if t := timeutil.ParseRFC3339Ptr(timeutil.DerefString(updatedAt)); t != nil {
		job.UpdatedAt = *t
	}
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)
}
