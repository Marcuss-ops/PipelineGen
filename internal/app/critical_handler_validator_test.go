// Package app — critical_handler_validator_test.go (Audit P0 #2 continuation, July 2026).
//
// Tests the composition-root critical-handler registration validator
// (defined in critical_handler_validator.go).
//
// Per the user-spec, the test SHAPE must:
//   - mock the CLOSURE pattern (NOT the concrete handler types), so
//     the validator is decoupled from any specific service implementation;
//   - exercise the 1-error-among-3 scenario where voiceover fails
//     and the other 2 succeed, asserting the aggregated error
//     message contains the failure context BUT NOT the success paths;
//   - exercise the all-OK path with no spurious failures;
//   - exercise the nil-svc + nil-log + nil-Bind + empty-slice safety
//     paths.
//
// Every test uses the same validator signature
// `func(svc *appjobs.Service) error` so the test can be run against
// the production validator directly without a dispatching adapter.
package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// noopHandlerFunc is a valid appjobs.HandlerFunc shape used to pre-bind
// success-path handlers via svc.RegisterHandler so HasHandler returns
// true. The closure records no side-effects; tests only observe the
// resulting dispatcher's HasHandler state.
func noopHandlerFunc(_ context.Context, _ *appjobs.Job, _ *appjobs.JobTools) (map[string]any, error) {
	return nil, nil
}

var _ appjobs.HandlerFunc = noopHandlerFunc

// makeValidatorSvcForTest opens an in-memory SQLite-backed jobs.Bundle
// via the canonical BuildJobsBundle path. Mirrors voiceover_wiring_test.go's
// construction pattern; the validator only reads HasHandler so the
// underlying dispatcher state is what matters.
func makeValidatorSvcForTest(t *testing.T) *appjobs.Service {
	t.Helper()
	sqliteDB, err := storage.OpenSQLiteDB(t.TempDir()+"/validator_test.db", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	bundle, err := BuildJobsBundle(sqliteDB, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, bundle.Service, "BuildJobsBundle must yield a non-nil jobs.Service")
	return bundle.Service
}

// TestValidateCriticalHandlers_OneErrorAmongThree pins the user-spec
// scenario: voiceover.generate binding returns a simulated error; the
// other 2 critical handlers succeed. The aggregated error MUST mention
// the voiceover failure (Name + wrapped underlying error) and MUST NOT
// mention the success paths (otherwise the operator's log scan
// confuses unbound-with-successful). All three Bind closures MUST
// have been invoked (1-error-among-3 surface).
func TestValidateCriticalHandlers_OneErrorAmongThree(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	voiceoverErr := errors.New("simulated post-call HasHandler check failed for voiceover.generate")
	voiceoverSeen, stockSeen, imageSeen := false, false, false

	// Pre-register success paths so HasHandler returns true. This is
	// how production wiring works: ComposeTime calls the handler's
	// Register method (which is silent-Warn for the stock + image
	// stubs in this test). The validator's Bind closure for those
	// success paths then performs a post-call HasHandler check, which
	// returns true after the pre-registration.
	require.NoError(t, svc.RegisterHandler(appjobs.TypeMediaStock, appjobs.HandlerFunc(noopHandlerFunc)))
	require.NoError(t, svc.RegisterHandler(appjobs.TypeImageGenerateGoogle, appjobs.HandlerFunc(noopHandlerFunc)))

	handlers := []CriticalHandler{
		{
			Name: "voiceover.generate",
			Bind: func(_ *appjobs.Service) error {
				voiceoverSeen = true
				return voiceoverErr
			},
		},
		{
			Name: "stockpipeline.media_stock",
			Bind: func(s *appjobs.Service) error {
				stockSeen = true
				if !s.HasHandler(appjobs.TypeMediaStock) {
					return fmt.Errorf("media.stock not bound (post-call HasHandler check)")
				}
				return nil
			},
		},
		{
			Name: "images.image_generate_google",
			Bind: func(s *appjobs.Service) error {
				imageSeen = true
				if !s.HasHandler(appjobs.TypeImageGenerateGoogle) {
					return fmt.Errorf("image.generate.google not bound (post-call HasHandler check)")
				}
				return nil
			},
		},
	}

	err := ValidateCriticalHandlers(svc, zap.NewNop(), handlers)

	// Aggregated error: must mention the failing one, NOT the successes.
	require.Error(t, err, "1-error-among-3 must surface as a non-nil aggregated error")
	assert.Contains(t, err.Error(), "voiceover.generate",
		"aggregated error must surface the voiceover.generate handler Name (audit-pinning surface)")
	assert.Contains(t, err.Error(), voiceoverErr.Error(),
		"aggregated error must surface the wrapped underlying voiceover failure context")
	assert.Contains(t, err.Error(), "1 binding failure",
		"aggregated error must report the failure count so operators can grep on the surface")

	// Success paths MUST NOT appear in the error message.
	assert.NotContains(t, err.Error(), "stockpipeline.media_stock",
		"successful bind path must not contaminate the aggregated error message (operator clarity)")
	assert.NotContains(t, err.Error(), "images.image_generate_google",
		"successful bind path must not contaminate the aggregated error message (operator clarity)")

	// All Bind closures were invoked (1-error-among-3 surface contract).
	assert.True(t, voiceoverSeen, "voiceover.generate Bind closure must have been invoked")
	assert.True(t, stockSeen, "stock Bind closure must have been invoked (1-error-among-3 surface)")
	assert.True(t, imageSeen, "image Bind closure must have been invoked (1-error-among-3 surface)")
}

// TestValidateCriticalHandlers_AllOK pins the happy path: every Bind
// closure succeeds → ValidateCriticalHandlers returns nil and the
// dispatcher state matches what the Bind closures performed.
func TestValidateCriticalHandlers_AllOK(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	handlers := []CriticalHandler{
		{
			Name: "voiceover.generate",
			Bind: func(_ *appjobs.Service) error { return nil },
		},
		{
			Name: "stockpipeline.media_stock",
			Bind: func(s *appjobs.Service) error {
				if err := s.RegisterHandler(appjobs.TypeMediaStock, appjobs.HandlerFunc(noopHandlerFunc)); err != nil {
					return fmt.Errorf("media.stock bind: %w", err)
				}
				return nil
			},
		},
		{
			Name: "images.image_generate_google",
			Bind: func(s *appjobs.Service) error {
				if err := s.RegisterHandler(appjobs.TypeImageGenerateGoogle, appjobs.HandlerFunc(noopHandlerFunc)); err != nil {
					return fmt.Errorf("image.generate.google bind: %w", err)
				}
				return nil
			},
		},
	}

	err := ValidateCriticalHandlers(svc, zap.NewNop(), handlers)

	require.NoError(t, err, "all-OK scenario must yield nil aggregated error")
	require.True(t, svc.HasHandler(appjobs.TypeMediaStock),
		"production-side RegisterHandler call in the Bind closure must have bound media.stock")
	require.True(t, svc.HasHandler(appjobs.TypeImageGenerateGoogle),
		"production-side RegisterHandler call in the Bind closure must have bound image.generate.google")
}

// TestValidateCriticalHandlers_MultipleErrorsVerifiedErrorsJoin pins
// the errors.Join contract: when 3 handlers all fail, the aggregated
// error contains ALL 3 handler Names, ALL 3 wrapped underlying errors,
// and the failure count reads "3 binding failure(s)".
func TestValidateCriticalHandlers_MultipleErrorsVerifiedErrorsJoin(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	handlers := []CriticalHandler{
		{
			Name: "voiceover.generate",
			Bind: func(_ *appjobs.Service) error { return errors.New("err-1: dispatch failure") },
		},
		{
			Name: "stockpipeline.media_stock",
			Bind: func(_ *appjobs.Service) error { return errors.New("err-2: pre-call HasHandler check failed") },
		},
		{
			Name: "images.image_generate_google",
			Bind: func(_ *appjobs.Service) error { return errors.New("err-3: post-call HasHandler check failed") },
		},
	}

	err := ValidateCriticalHandlers(svc, zap.NewNop(), handlers)

	require.Error(t, err)
	// All three handler Names enumerated.
	assert.Contains(t, err.Error(), "voiceover.generate")
	assert.Contains(t, err.Error(), "stockpipeline.media_stock")
	assert.Contains(t, err.Error(), "images.image_generate_google")
	// All three wrapped underlying errors enumerated.
	assert.Contains(t, err.Error(), "err-1")
	assert.Contains(t, err.Error(), "err-2")
	assert.Contains(t, err.Error(), "err-3")
	// Failure count.
	assert.Contains(t, err.Error(), "3 binding failure")
}

// TestValidateCriticalHandlers_NilSvc pins the wiring-bug guard:
// nil jobs.Service means the composition root passed in a wrong
// shape. This is a fat-finger programming bug at the composition
// root; the validator must abort loudly.
func TestValidateCriticalHandlers_NilSvc(t *testing.T) {
	err := ValidateCriticalHandlers(nil, zap.NewNop(), []CriticalHandler{
		{Name: "x", Bind: func(_ *appjobs.Service) error { return nil }},
	})

	require.Error(t, err, "nil jobs.Service must surface a typed error")
	assert.Contains(t, err.Error(), "nil jobs.Service",
		"the typed-error prefix must mention nil jobs.Service for grep-anchored grep audit")
}

// TestValidateCriticalHandlers_NilLogDefaultsToNoop pins the
// nil-safe log fallback: nil *zap.Logger swaps in zap.NewNop()
// (no-op logger) so the validator stays usable in composition
// roots that don't carry an explicit logger.
func TestValidateCriticalHandlers_NilLogDefaultsToNoop(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	err := ValidateCriticalHandlers(svc, nil, []CriticalHandler{
		{Name: "noop_handler", Bind: func(_ *appjobs.Service) error { return nil }},
	})
	require.NoError(t, err, "nil-log should default to zap.NewNop() and not error")
}

// TestValidateCriticalHandlers_NilBindClosureSkipped pins the
// nil-tolerant Bind contract: a CriticalHandler entry whose Bind
// closure is nil (e.g. an optional capability that hasn't been
// wired in this deploy) is skipped, NOT a binding failure.
func TestValidateCriticalHandlers_NilBindClosureSkipped(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	handlers := []CriticalHandler{
		{Name: "valid_one", Bind: func(_ *appjobs.Service) error { return nil }},
		{Name: "nil_bind_closure"}, // bind closure intentionally nil
		{Name: "valid_two", Bind: func(_ *appjobs.Service) error { return nil }},
	}

	err := ValidateCriticalHandlers(svc, zap.NewNop(), handlers)
	require.NoError(t, err, "nil Bind closure is skipped, not treated as a binding failure")
}

// TestValidateCriticalHandlers_EmptySlice pins the empty-slice
// contract: no handlers → no bindings to validate → no error.
func TestValidateCriticalHandlers_EmptySlice(t *testing.T) {
	svc := makeValidatorSvcForTest(t)

	err := ValidateCriticalHandlers(svc, zap.NewNop(), []CriticalHandler{})
	require.NoError(t, err, "empty handlers slice must be a no-op (no bindings to validate)")
}
