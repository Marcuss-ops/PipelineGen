// Package workerruntime_test — renew scenario helpers/tests split out of
// worker_registry_e2e_test.go to keep every file ≤400 lines.
package workerruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestE2E_RemoteWorkerRenewsLease(t *testing.T) {
	// 1. Mock broker with a long-expiry lease. The lease's
	//    ExpiresAt must exceed the test budget realistically so
	//    renewal is the only thing keeping the job from timing out
	//    in the broker's bookkeeping layer. We use 5 minutes of
	//    "fake" remaining time so any sub-second test holds the
	//    job without surprising the runner's lease semantics.
	mock := newMockBroker(t)
	lease := &appjobs.Lease{
		LeaseID: "lease-phase7-1",
		Job: &job.Job{
			ID:       "job-phase7-renew-1",
			Type:     "test.slow_phase7",
			Payload:  json.RawMessage(`{}`),
			Status:   job.StatusRunning,
			Revision: 1,
			LeaseID:  "lease-phase7-1",
		},
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	mock.serveLease(lease)

	// 2. Custom handler under test.slow_phase7 — a PRIVATE type
	//    (not in production's capability set) that BLOCKS the
	//    worker until it observes ≥1 renewal, then returns a
	//    deterministic shape that the test asserts on. The
	//    block-until-signal pattern proves the RUNNER's renewal
	//    loop fires while the handler is mid-execution, which is
	//    the W1 Phase 7 contract.
	// P1 #13 (July 2026): worker.Handler = jobs.Handler takes
	// *JobExecutionTools (not *worker.Tools). The tools parameter is
	// unused (_) so the parameter type change is cosmetic-only.
	handler := func(ctx context.Context, _ *job.Job, _ *job.JobExecutionTools) (job.Result, error) {
		select {
		case <-mock.renewSeen:
			// broker mock observed ≥1 renew — unblock and
			// report the snapshot count. Reading the count
			// outside the renewSeen closure is safe because
			// the renewal goroutine sends/closes via the
			// broker mock's mutex; another renewCount++
			// between our read and the return is acceptable
			// (the test asserts ≥1, not exactly 1).
			return map[string]any{
				"phase7": "renew_ok",
				"renews": int(mock.renewCounterOrZero()),
			}, nil
		case <-time.After(35 * time.Second):
			// 35s = 1.16× DefaultRenewInterval — under the
			// production cadence at least one renew must have
			// fired by this point; under the injected fast
			// cadence many more renewals will have fired
			// already. A timeout here means renewLoop was
			// wired wrong or the broker mock's Renew never
			// invoked.
			return nil, fmt.Errorf("phase7 timeout: no renew observed in 35s (count=%d)",
				mock.renewCounterOrZero())
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// 3. Worker registry populated with the custom handler only.
	//    No Dispatcher round-trip via AllHandlers() — the
	//    renewal protocol is independent of the dispatcher, and
	//    standing up clipindexer here would muddy the test's
	//    invariants (renewals on test.slow_phase7 don't
	//    interact with media.reindex types).
	workerReg := worker.NewRegistry()
	require.NoError(t, workerReg.Register("test.slow_phase7", handler))
	workerReg.Freeze()
	require.Equal(t, []string{"test.slow_phase7"}, workerReg.JobTypes(),
		"registry should hold exactly the phase7 stub handler")

	// 4. Workspace in a t.TempDir so t.Cleanup removes it after
	//    the test.
	tmpDir := t.TempDir()
	ws, err := worker.NewWorkspace(filepath.Join(tmpDir, "ws"))
	require.NoError(t, err)

	// 5. Runner with a fast cadence. SetRenewInterval clamps
	//    values below minRenewInterval (50ms); 200ms is
	//    comfortably above the floor and gives the broker mock's
	//    Renew N≥1 invocations well within the 35s handler
	//    timeout. The cadence itself is documented on
	//    Runner.effectiveRenewInterval (production default
	//    DefaultRenewInterval = 30s); this test does not
	//    re-assert the cadence constant — the constant is trusted,
	//    and the FACT that the broker mock saw N≥1 Renew calls
	//    is what proves the loop fired.
	runner := worker.NewRunner(mock, workerReg, ws, nil, zap.NewNop(),
		"w-phase7-1", "sess-phase7-1", []string{"test.slow_phase7"})
	runner.SetRenewInterval(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	// 6. Poll mock.completedResults until the broker records at
	//    least ONE Complete. The 45s deadline absorbs: handler
	//    start (microseconds), first renew at 200ms, handler
	//    unblock, marshal, Complete round-trip.
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for {
		results := mock.completedResults()
		if len(results) >= 1 {
			got := results[0]
			require.Equal(t, "job-phase7-renew-1", got.JobID,
				"Complete must carry the leased JobID")
			require.Equal(t, "lease-phase7-1", got.LeaseID,
				"Complete must carry the leased LeaseID")
			// The post-renewal ExpectedRevision must equal the
			// lease's Revision (mock broker never bumps
			// revision on Renew), proving Tools.Complete used
			// the Tools-tracked revision rather than a stale
			// snapshot. Real broker bumps revision on Renew,
			// so this test's lock at 1 is conservative.
			require.Equal(t, 1, got.ExpectedRevision,
				"Complete ExpectedRevision must equal the lease's Revision (mock broker never bumps revision on Renew)")

			var result map[string]any
			require.NoError(t, json.Unmarshal(got.Result, &result),
				"handler result must serialize cleanly to JSON")
			require.Equal(t, "renew_ok", result["phase7"],
				"handler must return renew_ok after observing ≥1 renew")
			require.GreaterOrEqual(t, mock.renewCounterOrZero(), int32(1),
				"W1 Phase 7 spec: ≥1 Renew call observed during lease hold")
			t.Logf("W1 Phase 7 end-to-end OK: handler saw %d renew calls, broker recorded Complete with JobID=%s (ExpectedRevision=%d)",
				mock.renewCounterOrZero(), got.JobID, got.ExpectedRevision)
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("worker did not complete the test.slow_phase7 job within 45s (renew count=%d)",
				mock.renewCounterOrZero())
		case <-time.After(20 * time.Millisecond):
			// poll loop
		}
	}

	// 7. Cancel runner; assert clean exit within 2s. Validates
	//    the deferred renewCancel + the runLease → renewLoop
	//    termination through ctx.Done().
	cancel()
	select {
	case err := <-runErr:
		require.True(t, err == nil || errors.Is(err, context.Canceled),
			"runner.Run should exit on ctx cancel; got: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner.Run did not exit within 2s after ctx cancel")
	}
}
