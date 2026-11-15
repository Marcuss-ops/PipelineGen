// Package runner certifies the runner-owned concurrency policy.
package runner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVidRushBackpressure_Defaults(t *testing.T) {
	bp := DefaultVidRushBackpressure()
	assert.Equal(t, DefaultNLPConcurrency, bp.ExtractionLimit, "extraction defaults to the certified NLP concurrency")
	assert.Equal(t, DefaultVidRushImageConcurrency, bp.ProviderSearchLimit)
	assert.Equal(t, DefaultVidRushImageConcurrency, bp.MaterializationLimit)

	resolved := (VidRushBackpressure{}).Resolved()
	assert.Equal(t, DefaultNLPConcurrency, resolved.ExtractionLimit)
	assert.Equal(t, DefaultVidRushImageConcurrency, resolved.ProviderSearchLimit)
	assert.Equal(t, DefaultVidRushImageConcurrency, resolved.MaterializationLimit)
}

func TestDefaultConcurrency_IsFour(t *testing.T) {
	assert.Equal(t, 4, DefaultNLPConcurrency, "certified NLP concurrency must be 4")
	// 4 is the measured TTS optimum: an 8-slot pool on the live 5-scene
	// certification job raised provider contention (wall 51.2s → 58.2s).
	assert.Equal(t, 4, DefaultTTSConcurrency, "certified TTS concurrency must be 4")
}

func TestVidRushBackpressure_RespectsExplicitLimits(t *testing.T) {
	bp := VidRushBackpressure{ExtractionLimit: 2, ProviderSearchLimit: 6, MaterializationLimit: 3}
	resolved := bp.Resolved()
	assert.Equal(t, 2, resolved.ExtractionLimit)
	assert.Equal(t, 6, resolved.ProviderSearchLimit)
	assert.Equal(t, 3, resolved.MaterializationLimit)
}

func TestVidRushBackpressure_ClampsImageWorkToFifteen(t *testing.T) {
	resolved := (VidRushBackpressure{
		ProviderSearchLimit:  32,
		MaterializationLimit: 32,
	}).Resolved()
	assert.Equal(t, MaxVidRushImageConcurrency, resolved.ProviderSearchLimit)
	assert.Equal(t, MaxVidRushImageConcurrency, resolved.MaterializationLimit)
}

func TestGenerationGate_QueuedAcquisition(t *testing.T) {
	gate := NewGenerationGate()
	require.NoError(t, gate.Acquire(context.Background()))

	acquired := make(chan struct{})
	go func() {
		if err := gate.Acquire(context.Background()); err != nil {
			return
		}
		close(acquired)
		gate.Release()
	}()

	// Release frees the slot for the blocked waiter, which then releases it
	// after acquiring it.
	gate.Release()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("queued waiter did not acquire")
	}
	require.NoError(t, gate.Acquire(context.Background()))
	gate.Release()
}

func TestGenerationGate_CapacityAllowsNConcurrentHolders(t *testing.T) {
	gate := NewGenerationGateWithCapacity(2)

	require.NoError(t, gate.Acquire(context.Background()))
	require.NoError(t, gate.Acquire(context.Background()))

	thirdAcquired := make(chan struct{})
	go func() {
		if err := gate.Acquire(context.Background()); err != nil {
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
	if gate.Capacity() != 1 {
		t.Fatalf("capacity = %d, want 1 (zero falls back to single-slot)", gate.Capacity())
	}
}

func TestGenerationGate_AcquireCancelledWhileWaiting(t *testing.T) {
	gate := NewGenerationGate()
	require.NoError(t, gate.Acquire(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	waiterErr := make(chan error, 1)
	go func() {
		waiterErr <- gate.Acquire(ctx)
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
	require.NoError(t, gate.Acquire(context.Background()))
	gate.Release()
}
