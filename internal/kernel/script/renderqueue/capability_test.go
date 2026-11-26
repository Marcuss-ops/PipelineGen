package renderqueue

import (
	"testing"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// TestCapabilityGPUPinsQueueVocabulary keeps the producer-side claim gate and
// the queue's claim predicate on one spelling. If the queue ever renames the
// capability, PipelineGen must fail here instead of silently rejecting every
// gpu_only request at preflight while a capable worker sits idle.
func TestCapabilityGPUPinsQueueVocabulary(t *testing.T) {
	if CapabilityGPU != queueclient.CapabilityGPU {
		t.Fatalf("renderqueue.CapabilityGPU = %q, queue/client.CapabilityGPU = %q", CapabilityGPU, queueclient.CapabilityGPU)
	}
}
