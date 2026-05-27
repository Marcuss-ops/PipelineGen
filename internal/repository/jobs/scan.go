package jobs

import (
	"encoding/json"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/timeutil"
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
	job.CreatedAt = timeutil.ParseRFC3339String(createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)
}
