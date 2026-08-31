package queue

import (
	"time"

	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// RetryBudgetAvailable remains as a compatibility helper for queue consumers.
func RetryBudgetAvailable(retryCount, maxRetries int) bool {
	return jobscheduling.RetryAllowed(retryCount, maxRetries)
}

// RetryDue remains as a compatibility facade. Scheduling owns retry timing.
func RetryDue(j *job.Job, now time.Time) bool {
	return jobscheduling.RetryDue(j, now)
}
