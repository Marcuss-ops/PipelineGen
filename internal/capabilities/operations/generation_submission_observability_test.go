package operations_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
)

// TestSubmitLockStats_CountsHolds pins the SUBMIT-LOCK-INSTRUMENTATION
// counters (September 2026): every Submit call that enters the mutex
// section increments HoldCount, and LockWaitTotal accumulates non-negative
// wait time. On an uncontended service the wait stays ~0; the contract
// pinned here is that the counters are consistent (HoldCount == number of
// Submits) and that AverageLockWait is well-defined.
func TestSubmitLockStats_CountsHolds(t *testing.T) {
	env := newFASE2Service(t)
	svc := env.Service
	ctx := context.Background()

	before := svc.SubmitLockStats()
	require.Zero(t, before.HoldCount, "no Submit ran yet")
	require.Zero(t, before.LockWaitTotal)

	req := canonicalSubmitRequest(operations.ScopeScriptGenerate, "stats-hold-key-1", makeHashFASE2("stats-body-1"))
	_, err := svc.Submit(ctx, req)
	require.NoError(t, err)

	after := svc.SubmitLockStats()
	assert.EqualValues(t, 1, after.HoldCount, "one Submit must increment HoldCount by 1")
	assert.GreaterOrEqual(t, after.LockWaitTotal, time.Duration(0),
		"cumulative wait is non-negative by construction")
	assert.GreaterOrEqual(t, after.AverageLockWait(), time.Duration(0))
}

// TestSubmitLockStats_ConcurrentSubmits pins that the counters are
// goroutine-safe: N concurrent Submits (distinct keys → all fresh) must
// produce HoldCount == N.
func TestSubmitLockStats_ConcurrentSubmits(t *testing.T) {
	env := newFASE2Service(t)
	svc := env.Service
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("stats-concurrent-key-%d", i)
			req := canonicalSubmitRequest(operations.ScopeScriptGenerate, key, makeHashFASE2("stats-body-"+fmt.Sprint(i)))
			// Every request has a distinct key, so all must succeed; the
			// counters must be consistent regardless.
			_, err := svc.Submit(ctx, req)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	stats := svc.SubmitLockStats()
	assert.EqualValues(t, n, stats.HoldCount,
		"HoldCount must equal the number of mutex-entering Submit calls")
}
