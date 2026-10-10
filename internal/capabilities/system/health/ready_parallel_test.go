// Package system — ready_parallel_test.go.
//
// Regression coverage for the readiness starvation bug: every probe used to run
// in series against ONE 15s deadline, so the slow probes (Drive canary retry
// loop, Drive root probe) consumed the whole budget and every probe after them
// saw an already-expired context. /ready then took exactly the 15s wall and
// reported healthy dependencies as failed — Ollama answered /api/tags in 8ms
// and was still reported unreachable; tts, storage_* and script_generate.db
// were false-negative for the same reason.
//
// The pin is that a probe's context is its OWN budget: a slow probe loses its
// own verdict, never anyone else's.
package system

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slowCanary blocks until its context is done — the shape of the real Drive
// canary when the Drive API answers late (its retry loop spends the whole
// budget).
type slowCanary struct{}

func (slowCanary) CanaryUpload(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// ctxRecordingOllama records the state of the context it was called with and
// reports a healthy Ollama (which is what the real one does: /api/tags answers
// 200 in single-digit milliseconds).
type ctxRecordingOllama struct {
	mu   sync.Mutex
	errs []error
}

func (o *ctxRecordingOllama) CheckOllama(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errs = append(o.errs, ctx.Err())
	return nil
}

func (o *ctxRecordingOllama) recorded() []error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]error(nil), o.errs...)
}

func TestCheckReadySlowProbeDoesNotStarveTheOthers(t *testing.T) {
	restore := readyCheckBudget
	readyCheckBudget = 300 * time.Millisecond
	t.Cleanup(func() { readyCheckBudget = restore })

	mock := &scenarioMock{name: "all", mandatory: true, ok: true}
	ollama := &ctxRecordingOllama{}
	ready := NewReadyChecker(NewService(ServiceDeps{DB: mock, Jobs: mock})).
		WithDriveCanary(slowCanary{}, "canary-folder").
		WithOllamaChecker(ollama)

	// The transport's aggregate SLA, exactly as the HTTP handler applies it.
	ctx, cancel := context.WithTimeout(context.Background(), readyCheckBudget)
	defer cancel()

	started := time.Now()
	resp := ready.CheckReady(ctx)
	elapsed := time.Since(started)

	// The slow probe still fails — and the aggregate stays unhealthy because of
	// it, which is the correct verdict.
	require.False(t, resp.Checks["drive_canary"]["ok"].(bool), "the slow canary must still be reported as failed")
	require.False(t, resp.OK, "a failed critical probe must still flip the aggregate")

	// The fast probe behind it must NOT inherit the canary's expired context.
	calls := ollama.recorded()
	require.Len(t, calls, 1)
	require.NoError(t, calls[0], "a healthy dependency must be probed with a live context, not the aggregate's expired one")
	require.True(t, resp.Checks["ollama"]["ok"].(bool), "Ollama answered, so its probe must be green")

	// Probes run concurrently with their own budget: the wall clock is the
	// slowest probe, not the sum.
	require.Less(t, elapsed, 2*readyCheckBudget,
		"probes must run concurrently (elapsed %s vs budget %s)", elapsed, readyCheckBudget)
}
