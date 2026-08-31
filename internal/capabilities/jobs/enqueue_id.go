// Package jobs — compatibility adapter for queue identity generation.
// The canonical implementation lives in jobs/queue so enqueue policy can be
// reused without importing the root jobs orchestration package.
package jobs

import jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"

func generateJobID() string {
	return jobqueue.GenerateJobID()
}
