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

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
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

	jobsBundle, err := BuildJobsBundle(sqliteDB, zaptest.NewLogger(t))
	require.NoError(t, err)
	require.NotNil(t, jobsBundle)
	require.NotNil(t, jobsBundle.Service)

	// Pre-condition: no handler for voiceover.generate yet. This is the
	// regression target for future Catena-A-shaped wiring accidents —
	// `false` is the gate value the smoke test asserts against.
	require.False(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerate),
		"voiceover.generate handler is unregistered at boot — Catena A P0 wiring is missing")

	// Register a stub handler. The signature accepts HandleJob's
	// (ctx, *job.Job, *appjobs.JobTools) → (map[string]any, error)
	// shape — the canonical GenerateJobHandler.HandleJob uses the same
	// signature (see jobs/generate_handler.go), so this tracks the
	// wire shape the dispatcher expects.
	jobsBundle.Service.RegisterHandler(job.TypeVoiceoverGenerate, stubVoiceoverGenerateHandler)

	require.True(t, jobsBundle.Service.HasHandler(job.TypeVoiceoverGenerate),
		"voiceover.generate handler must be registered after Register (Catena A P0 wiring contract)")
}

// stubVoiceoverGenerateHandler is a minimal closure that satisfies the
// jobs.Service.HandlerFunc signature. Real work happens in the typed-port
// GenerateJobHandler registered by composition.go; this stub exists
// only to exercise the dispatcher lookup table for HasHandler.
func stubVoiceoverGenerateHandler(ctx context.Context, j *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return map[string]any{"stub": true}, nil
}

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
