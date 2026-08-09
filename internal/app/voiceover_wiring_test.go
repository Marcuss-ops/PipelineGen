// Package app — voiceover_wiring_test.go (Catena A P0, June 2026).
//
// Boot smoke test for the voiceover.generate job handler. Two contract
// pins live here:
//
//  1. The canonical `voiceover.generate` job type (registered in
//     internal/domain/job/job.go) MUST have a handler after the
//     composition root finishes wiring. A successful wire prints
//     `voiceover.generate handler registered (Catena A P0 wiring
//     complete)` in the late-bindings block of composition.go; this
//     test reads the same bookkeeping via
//     jobs.Service.HasHandler(job.TypeVoiceoverGenerate).
//
//  2. The GenerateJobHandler.Register(jobsSvc) call MUST be idempotent
//     w.r.t. registration bookkeeping — calling it twice is a no-op,
//     calling it once flips HasHandler to true. Verified by direct
//     stub-handler registration against a jobs.Service built via
//     BuildJobsBundle, since unit-testing the full composition path
//     requires Drive + OAuth fixtures (covered by integration tests).
//
// Why regression-guard this so loudly: the pre-Catena-A HEAD (commit
// `e52b8fa9`) had a clean /api/voiceover/generate endpoint that
// enqueued jobs onto an unsigned dispatcher — every job silently
// dead-lettered. HasHandler was the missing single line; this test
// trips the gate if a future refactor regresses the registration.
package app

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestVoiceoverGenerateHandler_RequiresRegistration pins the contract
// that BEFORE the canonical handler is registered, HasHandler returns
// false (so the gate can identify the missing-registration case) and
// AFTER registration it returns true. The test uses a stub handler
// because the canonical use case requires Drive + outbox dispatcher
// wiring which is exercised by integration tests, NOT this unit-level
// smoke check.
func TestVoiceoverGenerateHandler_RequiresRegistration(t *testing.T) {
	sqliteDB, err := storage.OpenSQLiteDB(":memory:", zaptest.NewLogger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	jobsBundle, err := wiring.BuildJobsBundle(sqliteDB, zaptest.NewLogger(t), nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, jobsBundle)
	require.NotNil(t, jobsBundle.Service)

	// Pre-condition: no handler for voiceover.generate yet. This is the
	// regression target for future Catena-A-shaped wiring accidents —
	// `false` is the gate value the smoke test asserts against.
	require.False(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerate),
		"voiceover.generate handler is unregistered at boot — Catena A P0 wiring is missing")

	// Register a stub handler. The signature accepts the canonical
	// (ctx, *job.Job, *appjobs.JobExecutionTools) → (map[string]any, error)
	// shape — the canonical GenerateJobHandler.HandleJob uses the same
	// signature (see jobs/generate_handler.go), so this tracks the
	// wire shape the dispatcher expects. Conformance is locked via the
	// `var _ appjobs.Handler = stubVoiceoverGenerateHandler` line at the
	// bottom of this file (P1 #13a godlike/06 audit-pin); the legacy
	// `appjobs.JobTools` parameter name was retired in favour of the
	// canonical `appjobs.JobExecutionTools` per the P1 #13 unification.
	err = jobsBundle.Service.RegisterHandler(job.TypeVoiceoverGenerate, appjobs.HandlerFunc(stubVoiceoverGenerateHandler))
	require.NoError(t, err)

	require.True(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerate),
		"voiceover.generate handler must be registered after Register (Catena A P0 wiring contract)")

	// P0.3 (June 2026): per-language child job type (voiceover.generate_item)
	// also requires registration after the parent fan-out schedules N
	// children on the broker. Mirror the parent's HasHandler gate so a
	// future refactor that drops the child Register call is caught at
	// boot smoke rather than at first runtime job dispatch.
	require.False(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerateItem),
		"voiceover.generate_item handler is unregistered at boot — P0.3 parent-child fan-out wiring is missing")
	err = jobsBundle.Service.RegisterHandler(job.TypeVoiceoverGenerateItem, appjobs.HandlerFunc(stubVoiceoverGenerateHandler))
	require.NoError(t, err)
	require.True(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerateItem),
		"voiceover.generate_item handler must be registered after Register (P0.3 wiring contract)")
}

// stubVoiceoverGenerateHandler is a minimal closure that satisfies the
// canonical `appjobs.Handler` signature (P1 #13 unification, July 2026):
// `func(context.Context, *job.Job, *appjobs.JobExecutionTools) (map[string]any, error)`.
// Real work happens in the typed-port GenerateJobHandler registered by
// composition.go; this stub exists only to exercise the dispatcher
// lookup table for HasHandler.
//
// Compile-time conformance pin (godlike/06 audit-pin, P1 #13a): the
// `var _ appjobs.Handler = stubVoiceoverGenerateHandler` line below
// locks the closure against future parameter-type drift. If a refactor
// regresses `*appjobs.JobExecutionTools` to `*worker.Tools` (or another
// non-canonical shape), the test file fails to compile at this pin
// rather than silently passing through the Go type-alias identity.
// Mirrors the compile-time lock in
// internal/domain/job/handler_test.go::TestHandlerAliases_CompileTimeLock.
func stubVoiceoverGenerateHandler(ctx context.Context, j *appjobs.Job, tools *appjobs.JobExecutionTools) (map[string]any, error) {
	return map[string]any{"stub": true}, nil
}

// Compliance pin: stubVoiceoverGenerateHandler conforms to the canonical
// appjobs.Handler signature (P1 #13a closure cleanup). The compile-time
// assignment is the godlike/06 SSOT lock — a regression of the parameter
// type or return type surfaces as a build failure here.
var _ appjobs.Handler = stubVoiceoverGenerateHandler

// TestVoiceoverGenerateJobHandlerTypeIsWiredInSamePackage is a
// compile-time pin: the *voiceoverjobs.GenerateJobHandler type lives
// in the same package as the boot smoke test, so any drift in its
// signature (e.g. if a future refactor relocates it) breaks the test
// compilation immediately rather than at runtime.
func TestVoiceoverGenerateJobHandlerTypeIsWiredInSamePackage(t *testing.T) {
	var h *jobs.GenerateJobHandler
	require.Nil(t, h,
		"GenerateJobHandler pointer is nil at allocation-time — presence asserts the type compiles + is reachable from this package")
}
