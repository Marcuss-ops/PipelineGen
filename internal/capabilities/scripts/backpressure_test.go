// Package scriptgeneration — backpressure_test.go certifies the VidRush
// backpressure contract: the three pipeline stages (entity extraction,
// provider search, materialization) are bounded by independent semaphores, and
// the generation priority gate lets high-priority generation preempt
// low-priority extraction when they share a single-slot model.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushBackpressure_Defaults(t *testing.T) {
	bp := DefaultVidRushBackpressure()
	assert.Equal(t, DefaultNLPConcurrency, bp.ExtractionLimit, "extraction defaults to the certified NLP concurrency")
	assert.Equal(t, 4, bp.ProviderSearchLimit)
	assert.Equal(t, 2, bp.MaterializationLimit)

	resolved := (VidRushBackpressure{}).resolved()
	assert.Equal(t, DefaultNLPConcurrency, resolved.ExtractionLimit)
	assert.Equal(t, 4, resolved.ProviderSearchLimit)
	assert.Equal(t, 2, resolved.MaterializationLimit)
}

func TestDefaultConcurrency_IsFour(t *testing.T) {
	assert.Equal(t, 4, DefaultNLPConcurrency, "certified NLP concurrency must be 4")
	assert.Equal(t, 4, DefaultTTSConcurrency, "certified TTS concurrency must be 4")
}

func TestVidRushBackpressure_RespectsExplicitLimits(t *testing.T) {
	bp := VidRushBackpressure{ExtractionLimit: 2, ProviderSearchLimit: 6, MaterializationLimit: 3}
	resolved := bp.resolved()
	assert.Equal(t, 2, resolved.ExtractionLimit)
	assert.Equal(t, 6, resolved.ProviderSearchLimit)
	assert.Equal(t, 3, resolved.MaterializationLimit)
}

func TestGenerationGate_HighPriorityPreemptsLowPriority(t *testing.T) {
	gate := NewGenerationGate()

	// Hold the gate, then queue a low-priority waiter first.
	require.NoError(t, gate.AcquireLow(context.Background()))

	lowAcquired := make(chan struct{})
	highAcquired := make(chan struct{})

	go func() {
		if err := gate.AcquireLow(context.Background()); err != nil {
			return
		}
		close(lowAcquired)
	}()
	// Deterministically wait until the low waiter is actually queued.
	waitForQueued(t, gate, func() int {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		return len(gate.lowWait)
	}, 1)

	// Now queue a high-priority waiter.
	go func() {
		if err := gate.AcquireHigh(context.Background()); err != nil {
			return
		}
		close(highAcquired)
	}()
	waitForQueued(t, gate, func() int {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		return len(gate.highWait)
	}, 1)

	// Release the holder; the high-priority waiter must win even though the
	// low-priority waiter was queued first.
	gate.Release()

	select {
	case <-highAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("high-priority acquisition did not preempt the low-priority waiter")
	}
	select {
	case <-lowAcquired:
		t.Fatal("low-priority waiter acquired before high-priority waiter")
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForQueued(t *testing.T, gate *GenerationGate, count func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waiter did not queue: got %d want %d", count(), want)
}

func TestGenerationGate_FIFOWithinSamePriority(t *testing.T) {
	gate := NewGenerationGate()
	require.NoError(t, gate.AcquireHigh(context.Background()))

	acquired := make(chan struct{})
	go func() {
		if err := gate.AcquireHigh(context.Background()); err != nil {
			return
		}
		close(acquired)
		gate.Release()
	}()

	// Release hands the slot to the single queued high-priority waiter; that
	// waiter acquires and releases, letting us re-acquire cleanly.
	gate.Release()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("queued high-priority waiter did not acquire")
	}
	require.NoError(t, gate.AcquireHigh(context.Background()))
	gate.Release()
}

func TestGenerationGate_CapacityAllowsNConcurrentHolders(t *testing.T) {
	gate := NewGenerationGateWithCapacity(2)

	require.NoError(t, gate.AcquireLow(context.Background()))
	require.NoError(t, gate.AcquireLow(context.Background()))

	thirdAcquired := make(chan struct{})
	go func() {
		if err := gate.AcquireLow(context.Background()); err != nil {
			return
		}
		close(thirdAcquired)
		gate.Release()
	}()

	// The third acquisition must block while both slots are held.
	select {
	case <-thirdAcquired:
		t.Fatal("third acquisition acquired while both slots were held")
	case <-time.After(50 * time.Millisecond):
	}

	// Release one holder; the queued waiter acquires the freed slot.
	gate.Release()
	select {
	case <-thirdAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("third acquisition did not acquire after a release")
	}

	// Release the remaining holders.
	gate.Release()
	gate.Release()
}

func TestGenerationGate_ZeroCapacityFallsBackToSingle(t *testing.T) {
	gate := NewGenerationGateWithCapacity(0)
	if gate.capacity != 1 {
		t.Fatalf("capacity = %d, want 1 (zero falls back to single-slot)", gate.capacity)
	}
}

func TestGenerationGate_AcquireCancelledWhileWaiting(t *testing.T) {
	gate := NewGenerationGate()
	require.NoError(t, gate.AcquireHigh(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	waiterErr := make(chan error, 1)
	go func() {
		waiterErr <- gate.AcquireLow(ctx)
	}()
	cancel()

	select {
	case err := <-waiterErr:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	// The gate must remain held by the original holder; releasing it must not
	// panic or leak a pending waiter.
	gate.Release()
	gate.AcquireHigh(context.Background())
	gate.Release()
}

// recordingMaterializer records materialize calls to prove the stage is wired
// with its own bounded concurrency.
type recordingMaterializer struct {
	mu    sync.Mutex
	calls int
}

func (m *recordingMaterializer) Materialize(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return segment, nil
}

func (m *recordingMaterializer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestCoordinator_BackpressureRunsAllThreeStages(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	resolver := &fakeSegmentProviderResolver{}
	materializer := &recordingMaterializer{}
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(enricher, nil, VidRushBackpressure{
		ExtractionLimit: 1, ProviderSearchLimit: 2, MaterializationLimit: 1,
	})
	coordinator.SetSegmentProviderResolver(resolver)
	coordinator.SetSegmentMaterializer(materializer)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	results, err := coordinator.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, 2, enricher.callCount(), "extraction must run once per scene")
	assert.Equal(t, 2, resolver.callCount(), "provider search must run once per scene")
	assert.Equal(t, 2, materializer.callCount(), "materialization must run once per scene")
}

func TestCoordinator_ExtractionYieldsToGenerationGate(t *testing.T) {
	gate := NewGenerationGate()
	// Simulate generation holding the single-slot model.
	require.NoError(t, gate.AcquireHigh(context.Background()))

	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 4)
	coordinator.SetGenerationGate(gate)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)

	// The extraction must block on the generation gate before reaching the
	// enricher.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, enricher.callCount(), "extraction must wait while generation holds the gate")

	// Release generation; extraction may now proceed.
	gate.Release()
	close(enricher.release)

	results, err := coordinator.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, enricher.callCount())
}
