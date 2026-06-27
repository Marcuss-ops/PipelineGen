package usecase

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── 4.1 Capacity ─────────────────────────────────────────────────────────────

func TestSemaphoreUseCase_Capacity(t *testing.T) {
	t.Parallel()
	uc, err := NewSemaphoreUseCase(2, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, 2, uc.Capacity())

	// First two acquires succeed immediately.
	r1, err := uc.Acquire(context.Background(), "jobA")
	require.NoError(t, err)
	r2, err := uc.Acquire(context.Background(), "jobB")
	require.NoError(t, err)

	// Third acquire blocks until a release frees a slot.
	var thirdAcquired atomic.Bool
	var thirdErr error
	go func() {
		r3, e := uc.Acquire(context.Background(), "jobC")
		thirdErr = e
		if e == nil {
			r3()
		}
		thirdAcquired.Store(true)
	}()

	// Give the goroutine time to block.
	time.Sleep(50 * time.Millisecond)
	assert.False(t, thirdAcquired.Load(), "third acquire must block while capacity is full")

	// Release one slot — third acquire must succeed.
	r1()
	time.Sleep(100 * time.Millisecond)
	assert.True(t, thirdAcquired.Load(), "third acquire must succeed after release")
	assert.NoError(t, thirdErr)

	r2()
}

// ── 4.2 Context cancellation ────────────────────────────────────────────────

func TestSemaphoreUseCase_AcquireCancelled(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	// Occupy the only slot.
	_, err := uc.Acquire(context.Background(), "first")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uc.Acquire(ctx, "second")
	require.ErrorIs(t, err, ErrSemaphoreAcquisitionCanceled)
}

// ── 4.3 Release idempotent ──────────────────────────────────────────────────

func TestSemaphoreUseCase_ReleaseIdempotent(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	r, err := uc.Acquire(context.Background(), "job")
	require.NoError(t, err)

	// Release twice — must not panic, must not free two slots.
	r()
	r() // idempotent

	require.Equal(t, int64(1), uc.ReleaseCount(), "release count must only increment once")

	// Verify a new acquire still works (slot was freed exactly once).
	r2, err := uc.Acquire(context.Background(), "job2")
	require.NoError(t, err)
	r2()
	require.Equal(t, int64(2), uc.ReleaseCount())
}

// ── 4.4 Release su errore ───────────────────────────────────────────────────

func TestSemaphoreUseCase_ReleaseOnError(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())

	// Simulate: acquire, then "error" — release must still free the slot.
	r, err := uc.Acquire(context.Background(), "job")
	require.NoError(t, err)
	r() // release on error path

	// Slot must be free for next job.
	r2, err := uc.Acquire(context.Background(), "job2")
	require.NoError(t, err)
	r2()

	require.Equal(t, int64(2), uc.ReleaseCount())
}

// ── 4.5 Nil safe ────────────────────────────────────────────────────────────

func TestSemaphoreUseCase_NilSafe(t *testing.T) {
	t.Parallel()
	var uc *SemaphoreUseCase

	rel, err := uc.Acquire(context.Background(), "j")
	require.Error(t, err)
	require.NotPanics(t, func() { rel() })

	assert.Equal(t, int64(0), uc.AcquireCount())
	assert.Equal(t, int64(0), uc.ReleaseCount())
	assert.Equal(t, 0, uc.Capacity())
}

// ── 4.6 Race stress ─────────────────────────────────────────────────────────

func TestSemaphoreUseCase_ConcurrentStress(t *testing.T) {
	t.Parallel()
	capacity := 3
	uc, _ := NewSemaphoreUseCase(capacity, zap.NewNop())

	var maxConcurrent atomic.Int64
	var current atomic.Int64
	var wg sync.WaitGroup

	iterations := 200
	wg.Add(iterations)

	for i := range iterations {
		go func(id int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			rel, err := uc.Acquire(ctx, "job")
			if err != nil {
				return
			}
			defer rel()

			cur := current.Add(1)
			// Track max.
			for {
				prev := maxConcurrent.Load()
				if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
					break
				}
			}

			time.Sleep(time.Millisecond) // simulate short work

			current.Add(-1)
		}(i)
	}
	wg.Wait()

	assert.LessOrEqual(t, maxConcurrent.Load(), int64(capacity),
		"max concurrent must not exceed capacity")
	assert.Equal(t, int64(0), current.Load(), "no dangling acquires")
}
