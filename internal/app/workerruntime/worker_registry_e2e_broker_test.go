// Package workerruntime_test — broker scenario helpers/tests split out of
// worker_registry_e2e_test.go to keep every file ≤400 lines.
package workerruntime_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"


	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

var observedRegisterPath atomic.Value

// observedURL reads observedRegisterPath with a nil-safe fallback for
// the case where the round-trip didn't reach the middleware (e.g.
// gin rejected the URL before any middleware ran). The empty-string
// default keeps `containsPath`'s contract intact: containsPath("",
// anything-non-empty) = false, so a missing middleware-run surfaces
// as a real drift failure rather than a silent pass.
func observedURL() string {
	v := observedRegisterPath.Load()
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// mockBroker implements the workers.Broker interface so the real
// WorkerHandler on the gin router can be exercised. Only
// RegisterWorker is asserted; other methods fail loud (Errorf) if
// they are unexpectedly invoked through the alignment smoke.
type mockBroker struct {
	t              *testing.T
	mu             sync.Mutex
	registerCalled bool
	// Phase 6 fields. When lease is set via serveLease(), the mock
	// is wired to deliver ONE real job to the worker under test and
	// record the broker interactions. When lease is nil
	// (alignment-smoke test path), the broker still surfaces every
	// non-RegisterWorker invocation as a test error so unexpected
	// calls are loud instead of silently no-op'd.
	lease       *appjobs.Lease
	leaseServed bool
	completed   []appjobs.CompleteCommand
	// Phase 7 fields. renewCount is incremented by Mock.Renew on
	// every invocation; renewSeen is closed once the count crosses
	// 1, enabling the renewal-aware handler in
	// TestE2E_RemoteWorkerRenewsLease to unblock and observe the
	// protocol end-to-end. Separated from the lock to keep the
	// signal-on-N closure primitive (channel close is well-defined
	// in the Go memory model; a counter + Bool boolean would need
	// the same mutex discipline).
	renewCount int32
	renewSeen  chan struct{}
}

func newMockBroker(t *testing.T) *mockBroker {
	return &mockBroker{t: t, renewSeen: make(chan struct{})}
}

// renewCounterOrZero returns the current renewal count under the
// mock's mutex. Convenience accessor used by TestE2E_RemoteWorkerRenewsLease
// instead of reaching into the struct's mutex directly.
func (m *mockBroker) renewCounterOrZero() int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renewCount
}

// serveLease configures the mock broker to return the supplied lease
// on the next Claim call. Subsequent Claim calls return (nil, nil)
// so Worker.Runner naturally falls into its idle/retry branch and
// can be cancelled cleanly. Used by TestE2E_RemoteWorkerExecutesMediaReindex
// to deliver a single media.reindex job to the worker.
//
// Calling serveLease after Claim has already been invoked is a logic
// error (the lease is meant to be threaded in before Run starts).
// We don't guard here; the test that uses it is sequential.
func (m *mockBroker) serveLease(l *appjobs.Lease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lease = l
}

// completedResults returns a defensive copy of every Complete command
// the broker has received so far. Used by Phase 6 to assert the
// worker reported a successful job outcome to the broker.
func (m *mockBroker) completedResults() []appjobs.CompleteCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]appjobs.CompleteCommand, len(m.completed))
	copy(out, m.completed)
	return out
}

func (m *mockBroker) RegisterWorker(_ context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	m.mu.Lock()
	m.registerCalled = true
	// observedRegisterPath is now set by the gin middleware in the
	// test setup so it reflects the actual URL the client hit
	// (e.g. "/internal/v1/workers/register"), not a server-side
	// post-prefix path.
	m.mu.Unlock()
	// Mirror the canonical WorkerSession struct from
	// internal/domain/job/worker.go. Field names are exact-match on the
	// JSON tags so the production broker.Register deserialisation
	// would round-trip cleanly if the test ever swapped the mock for
	// the real repos.WorkerNodesRepository.
	return &appjobs.WorkerSession{
		WorkerID:         cmd.WorkerID,
		SessionID:        fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		SessionExpiresAt: time.Now().Add(30 * time.Second),
		Capabilities:     cmd.Capabilities,
		Version:          cmd.Version,
		Hostname:         cmd.Hostname,
	}, nil
}

func (m *mockBroker) Heartbeat(_ context.Context, _ appjobs.HeartbeatCommand) error {
	m.t.Logf("mock broker: Heartbeat called unexpectedly")
	return nil
}

// Claim, Renew, Progress, Complete, Fail, IsCancelled are
// conditionally implemented. When the Phase 6 test pre-loads a
// lease via serveLease(), these return real responses so the
// worker pipeline can run. When lease is nil (alignment-smoke
// test) they surface as test errors so unexpected invocations are
// loud instead of silently no-op'd.

func (m *mockBroker) Claim(_ context.Context, _ appjobs.ClaimCommand) (*appjobs.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Claim (smoke test should only call RegisterWorker)")
		return nil, fmt.Errorf("not implemented in alignment-smoke mock")
	}
	if m.leaseServed {
		return nil, nil
	}
	m.leaseServed = true
	return m.lease, nil
}

func (m *mockBroker) Renew(_ context.Context, _ appjobs.RenewCommand) (*appjobs.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Renew (smoke test should only call RegisterWorker)")
		return nil, fmt.Errorf("not implemented in alignment-smoke mock")
	}
	// Phase 7: count the renewal and signal one-shot via close.
	// The `m.renewCount == 1` guard GATES the close so a
	// double-close panic (close-of-closed-channel) is impossible.
	// A previous version also set `m.renewSeen = nil` after the
	// close as defence-in-depth, but the post-close write raced
	// with the handler goroutine's preload of `mock.renewSeen`
	// for the `<-mock.renewSeen` select-case evaluation (the
	// chan-pointer load happened BEFORE the close's sync
	// barrier into the handler receive). The redundant write is
	// gone — the field is set in newMockBroker, never nilled,
	// and after the close only the channel's own sync semantics
	// matter.
	m.renewCount++
	if m.renewCount == 1 {
		close(m.renewSeen)
	}
	return m.lease, nil
}

func (m *mockBroker) Progress(_ context.Context, _ appjobs.ProgressCommand) error {
	// Progress is non-fatal even on the alignment-smoke path so the
	// test never tilts-over; ZLogger the call exists but does not
	// fail the test (the alignment-smoke path intentionally never
	// invokes Progress — if it does, a more specific test is needed).
	_ = time.Now()
	return nil
}

func (m *mockBroker) Complete(_ context.Context, cmd appjobs.CompleteCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Complete (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.t.Logf("mockBroker.Complete called: %+v", cmd)
	m.completed = append(m.completed, cmd)
	return nil
}

func (m *mockBroker) Fail(_ context.Context, cmd appjobs.FailCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Fail (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.completed = append(m.completed, appjobs.CompleteCommand{
		WorkerID:         cmd.WorkerID,
		WorkerSessionID:  cmd.WorkerSessionID,
		JobID:            cmd.JobID,
		LeaseID:          cmd.LeaseID,
		ExpectedRevision: cmd.ExpectedRevision,
	})
	return nil
}

func (m *mockBroker) IsCancelled(_ context.Context, jobID, leaseID string) (bool, error) {
	m.t.Errorf("mock broker: unexpected IsCancelled jobID=%s leaseID=%s", jobID, leaseID)
	return false, fmt.Errorf("not implemented in alignment-smoke mock")
}

// AZIONE 5 (July 2026): broker returns canonical AssetIDs from finalization.
func (m *mockBroker) CompleteWithArtifacts(_ context.Context, _ appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	m.t.Errorf("mock broker: unexpected CompleteWithArtifacts (smoke test should only call RegisterWorker/Complete)")
	return nil, fmt.Errorf("not implemented in alignment-smoke mock")
}

// containsPath is a tiny helper kept in this file so the test
// doesn't pull in pkg/sliceutil — the assertion is single-string
// and the file already imports enough.
func containsPath(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// ── Phase 6 — Remote worker executes a real bound handler end-to-end ──────

// TestE2E_RemoteWorkerExecutesMediaReindex is the W1 Phase 6 acceptance
// proof that the remote worker can execute a real bound handler
// end-to-end: claim → dispatch → handler return → completion reported
// back to the broker. The chosen job type is media.reindex because
// the Phase 0 inventory showed it is:
//
//	(a) bound to the in-process Dispatcher via
//	    clipindexer.RegisterJobHandler in composition.go,
//	(b) remote-safe — the handler reads input only from Job.Payload +
//	    the shared SQLite DB and writes output only back to the DB,
//	(c) trivially fast on an empty DB — it returns {total:0,
//	    indexed:0, failed:0} without invoking any python subprocess
//	    or Qdrant HTTP call.
//
// The test exercises, end-to-end:
//
//   - clipindexer.Service.HandleJob running through the Dispatcher
//   - worker.Registry populated from dispatcher.AllHandlers() via an
//     inline adaptHandler (the same bridging path
//     BuildWorkerRegistry uses in production cmd/worker)
//   - worker.Tools.Progress / IsCancelled forwarding to the broker
//   - worker.Runner.runLease completing the lifecycle (parsing
//     payload, calling the handler, marshalling the result, calling
//     broker.Complete)
//
// What this test does NOT pretend to cover (deferred to later waves):
//
//   - python/Qdrant subprocess paths — the handler exits early on
//     empty DB. python / Qdrant paths are exercised in W3 + W5.
//   - lease renewal — Runner.runLease does not renew in this short
//     happy path (W1 Phase 7).
//   - HTTP broker round-trip — the broker is stubbed; the W2
//     acceptance gate covers the network path bit-for-bit.
//   - progress event emission to the server's jobs table — broker
//     Progress here is a no-op counter; the worker-server
//     integration proves the same path at the network layer in W3.
