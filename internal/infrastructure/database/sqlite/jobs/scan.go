package jobs

import (
	"encoding/json"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// unmarshalJobFields deserialises JSON payload/result and time fields after a Scan.
func unmarshalJobFields(j *job.Job, payloadJSON, resultJSON string, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string) {
	if len(payloadJSON) > 0 {
		json.Unmarshal([]byte(payloadJSON), &j.Payload)
	}
	if len(resultJSON) > 0 {
		json.Unmarshal([]byte(resultJSON), &j.Result)
	}
	j.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
	if t := timeutil.ParseRFC3339Ptr(timeutil.DerefString(createdAt)); t != nil {
		j.CreatedAt = *t
	}
	if t := timeutil.ParseRFC3339Ptr(timeutil.DerefString(updatedAt)); t != nil {
		j.UpdatedAt = *t
	}
	j.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	j.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	j.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)
}
