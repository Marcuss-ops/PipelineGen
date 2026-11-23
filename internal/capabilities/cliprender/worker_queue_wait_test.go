package cliprender

import (
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestAddQueueWaitMS_MeasuresDurableWait(t *testing.T) {
	started := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	j := &job.Job{CreatedAt: started.Add(-5 * time.Second)}
	result := job.Result{}
	addQueueWaitMS(result, j, started)
	if got := result["queue_wait_ms"]; got != int64(5000) {
		t.Fatalf("queue_wait_ms = %v, want 5000", got)
	}
}

func TestAddQueueWaitMS_DoesNotFabricateZero(t *testing.T) {
	started := time.Now()
	for _, j := range []*job.Job{nil, {}, {CreatedAt: started.Add(time.Second)}} {
		result := job.Result{}
		addQueueWaitMS(result, j, started)
		if got := result["queue_wait_ms"]; got != "NOT_INSTRUMENTED" {
			t.Fatalf("queue_wait_ms = %v, want NOT_INSTRUMENTED", got)
		}
	}
}
