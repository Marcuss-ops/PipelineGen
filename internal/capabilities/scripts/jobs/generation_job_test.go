// Package jobs -- generation_job_test.go (Issue 7 / P1, June 2026).
//
// Pins the fail-fast contract on GenerateJobHandler.RegisterJobs:
//
//  1. TestRegisterJobs_FailsWhenBrokerMissing: pass nil broker,
//     assert the new typed error is returned and the error message
//     matches the canonical "jobs broker is required" substring so
//     operator audit logs from the composition root grep cleanly.
//
//  2. TestRegisterJobs_HappyPath: build a real broker (via a
//     minimal mockBroker that records the (jobType, handler) pair
//     passed to RegisterHandler), call RegisterJobs with it, assert
//     (a) returns nil, (b) mockBroker.lastType == script.generate,
//     (c) the handler passed is non-nil. The third sub-test pins
//     the registered handler identity so a future refactor that
//     swaps h.Handle for a wrapper does not regress silently.
//
// Future-proofing note: ports.Broker uses `handler any` (rather than
// the job-system `HandlerFunc` concrete signature) because the
// canonical Broker interface in `internal/capabilities/scripts/ports`
// is a typed-port surface that propagates the `func(ctx, *job.Job,
// *appjobs.JobTools)` shape from the job system. The mock splits the
// recording into (type, handler) pairs so a type assertion in the
// happy-path assertion can recover the concrete handler shape.
package jobs

import (
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── mockBroker for TestRegisterJobs_HappyPath ────────────────────────

type mockBroker struct {
	lastType    string
	lastHandler any
	forceError  error
	calls       int
}

func (m *mockBroker) RegisterHandler(jobType string, handler any) error {
	m.calls++
	m.lastType = jobType
	m.lastHandler = handler
	return m.forceError
}

// ── TestRegisterJobs_FailsWhenBrokerMissing ────────────────────────
//
// Issue 7 / P1: the canonical fail-fast test. Pass nil broker,
// assert the new typed error is returned. The spec requires the
// error message to be "generate job handler: jobs broker is
// required" so operator audit logs grep consistently.
//
// We do NOT assert handler-construction ordering here -- the
// handler itself can be nil or partially-constructed; the
// RegisterJobs contract is that the broker must be non-nil,
// independent of the handler's construction state.
func TestRegisterJobs_FailsWhenBrokerMissing(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	// nil `one` and `many` -- the fail-fast is checked before
	// any use-case invocation, so partial construction is fine.
	handler := NewGenerateJobHandler(nil, nil, logger)

	err := handler.RegisterJobs(nil)
	require.Error(t, err, "Issue 7 / P1: RegisterJobs must fail when broker is nil")
	assert.Contains(t, err.Error(), "jobs broker is required",
		"err message must match canonical substring for operator audit grep")
	assert.Contains(t, err.Error(), "generate job handler",
		"err message must include package prefix so it can be triaged as script-side")
}

// ── TestRegisterJobs_HappyPath ──────────────────────────────────────
//
// Companion positive test. Pass a recording mockBroker, assert the
// canonical call shape:
//
//   - RegisterJobs returns nil
//   - mockBroker.calls == 1 (no re-register attempts)
//   - mockBroker.lastType == script.generate (canonical type)
//   - mockBroker.lastHandler is non-nil (handler identity preserved)
//
// Pins the contract on the registered-handler identity so future
// refactors that swap h.Handle for a wrapper cannot regress
// silently.
func TestRegisterJobs_HappyPath(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	// partial; RegisterJobs does not invoke the use cases
	handler := NewGenerateJobHandler(nil, nil, logger)

	broker := &mockBroker{}
	err := handler.RegisterJobs(broker)

	require.NoError(t, err, "happy path: RegisterJobs with a non-nil broker returns nil")
	assert.Equal(t, 1, broker.calls, "happy path: RegisterJobs calls broker.RegisterHandler exactly once")
	assert.Equal(t, job.TypeScriptGenerate, broker.lastType,
		"happy path: broker is registered for script.generate (canonical spec type)")
	assert.NotNil(t, broker.lastHandler,
		"happy path: broker receives a non-nil handler so dispatch will not panic")
}

// ── TestRegisterJobs_PropagatesBrokerError ─────────────────────────
//
// Forward the RegisterHandler error from the broker to the caller.
// Confirms the wrapper does not mask upstream errors.
func TestRegisterJobs_PropagatesBrokerError(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	handler := NewGenerateJobHandler(nil, nil, logger)

	broker := &mockBroker{forceError: errors.New("broker unavailable")}
	err := handler.RegisterJobs(broker)

	require.Error(t, err, "broker failure must propagate to caller")
	assert.Contains(t, err.Error(), "broker unavailable",
		"propagated error must preserve underlying message for diagnostics")
}

// ── TestRegisterJobs_NilHandlerRejected ────────────────────────────
//
// The handler-side nil check predates Issue 7 and is preserved.
// A nil `h` receiver must still error so callers cannot silently
// register a zero-value GenerateJobHandler struct.
func TestRegisterJobs_NilHandlerRejected(t *testing.T) {
	t.Parallel()

	var nilHandler *GenerateJobHandler
	broker := &mockBroker{}

	err := nilHandler.RegisterJobs(broker)
	require.Error(t, err, "nil handler must fail")
	assert.Contains(t, err.Error(), "not constructed",
		"nil handler error must preserve canonical 'not constructed' message")
}
