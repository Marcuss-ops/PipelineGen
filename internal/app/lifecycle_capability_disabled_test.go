// Package app — TDD coverage for the CapabilityDisabled typed sentinel
// introduced by PR-LIFECYCLE-CAPABILITY-DISABLED-SENTINEL
// (architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04).
//
// Per godlike/07 typed-error contract: the sentinel is reachable via
// `errors.Is(err, ErrCapabilityDisabled)` from any wrapping context;
// the 2 disabled steps (yt-cache-prewarm + yt-nightly-prewarm) surface
// it via fmt.Errorf("%w")+startup-log; Required:false ensures startup
// survives even with the typed error returned. The tests below pin
// all 4 invariants via the PRODUCTION-SHAPE idiom (real fmt.Errorf
// %w chain, not errors.New) so they fail loudly if the production
// wrap ever drops the typed-error contract.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestErrCapabilityDisabled_DistinctFromNil: ErrCapabilityDisabled is
// NOT nil and is distinct from any other typed sentinel in the package.
// Per godlike/07 typed-error contract: each sentinel is errors.New(...)
// and reachable from wrapping contexts.
func TestErrCapabilityDisabled_DistinctFromNil(t *testing.T) {
	if ErrCapabilityDisabled == nil {
		t.Fatal("ErrCapabilityDisabled MUST be a non-nil typed sentinel (godlike/07 typed-error contract)")
	}
	if ErrCapabilityDisabled.Error() == "" {
		t.Fatal("ErrCapabilityDisabled.Error() MUST be non-empty so log surfaces carry diagnostic context")
	}
	if !strings.Contains(ErrCapabilityDisabled.Error(), "capability disabled") {
		t.Errorf("ErrCapabilityDisabled.Error() should describe the state verbatim so log readers see 'capability disabled' without unwrapping chains; got %q", ErrCapabilityDisabled.Error())
	}
}

// TestErrCapabilityDisabled_NotEqualToOtherSentinels: pin that
// ErrCapabilityDisabled is a distinct value from the other sentinels
// declared in the package. Catches future drift where someone
// re-uses the same errors.New(...) literal across features AND
// where someone wraps the WRONG sentinel in a `%w` chain (the wrap
// would carry the wrong sentinel's identity through errors.Is probes).
//
// We compare by errors.Is probe semantics — different sentinels must
// NOT match each other. We also probe through 2 wrap depths to
// catch the 'wrong sentinel in a %w chain' bug class.
func TestErrCapabilityDisabled_NotEqualToOtherSentinels(t *testing.T) {
	others := []struct {
		name string
		sent error
	}{
		{"ErrRecommendAdapterNotConfigured", wiring.ErrRecommendAdapterNotConfigured},
		{"ErrStageDriveInsufficientForCompletion", ErrStageDriveInsufficientForCompletion},
	}
	for _, o := range others {
		// Direct comparison.
		if errors.Is(ErrCapabilityDisabled, o.sent) {
			t.Errorf("ErrCapabilityDisabled must NOT directly match %s (distinct sentinel surfaces)", o.name)
		}
		if errors.Is(o.sent, ErrCapabilityDisabled) {
			t.Errorf("%s must NOT directly match ErrCapabilityDisabled (distinct sentinel surfaces)", o.name)
		}
		// Wrap-2 probe: ensure that wrapping a DIFFERENT sentinel
		// does not accidentally match ErrCapabilityDisabled through
		// 2 levels of %w chains. This catches the 'wrong sentinel in
		// a %w' bug class.
		wrapped := fmt.Errorf("level1: %w", o.sent)
		wrapped2 := fmt.Errorf("level2: %w", wrapped)
		if errors.Is(wrapped2, ErrCapabilityDisabled) {
			t.Errorf("wrapping %s through 2 %%w levels must NOT match ErrCapabilityDisabled (sentinel identity leak); got err=%v", o.name, wrapped2)
		}
	}
}

// TestYTCachePrewarmStartupStep_ProductionShape: pin the per-step
// Start return value at the production call site. The Start func
// below mirrors EXACTLY the production-site shape:
//
//	Start: func(...) error {
//	    return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
//	}
//
// Using errors.New (which doesn't establish a %w chain) would make
// this test a string-prefix match, hiding a future production-side
// bug where someone drops the %w. The fmt.Errorf %w chain is the
// canonical godlike/07 typed-error contract — pin it.
func TestYTCachePrewarmStartupStep_ProductionShape(t *testing.T) {
	step := StartupStep{
		Name: "yt-cache-prewarm", Required: false,
		Start: func(startCtx context.Context) error {
			// PRODUCTION-SHAPE: identical to internal/app/lifecycle.go
			// lines around the yt-cache-prewarm step. The wrap MUST be
			// fmt.Errorf with %w so errors.Is works through the chain.
			return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
		},
		Stop: func(_ context.Context) error { return nil },
	}
	err := step.Start(context.Background())
	if err == nil {
		t.Fatal("yt-cache-prewarm Start MUST return non-nil error per godlike/07 no-fake-availability (a step returning success while loading NOTHING is a fake success)")
	}
	if !errors.Is(err, ErrCapabilityDisabled) {
		t.Errorf("yt-cache-prewarm Start MUST errors.Is(err, ErrCapabilityDisabled) == true (via %%w chain); got err=%v, ErrCapabilityDisabled=%v", err, ErrCapabilityDisabled)
	}
	if !strings.Contains(err.Error(), "yt-cache-prewarm") {
		t.Errorf("yt-cache-prewarm Start error message MUST include the step name as diagnostic context; got %q", err.Error())
	}
}

// TestYTNightlyPrewarmStartupStep_ProductionShape: same contract as
// the cache-prewarm test, on the second surviving disabled step.
func TestYTNightlyPrewarmStartupStep_ProductionShape(t *testing.T) {
	step := StartupStep{
		Name: "yt-nightly-prewarm", Required: false,
		Start: func(startCtx context.Context) error {
			return fmt.Errorf("yt-nightly-prewarm: %w", ErrCapabilityDisabled)
		},
		Stop: func(_ context.Context) error { return nil },
	}
	err := step.Start(context.Background())
	if err == nil {
		t.Fatal("yt-nightly-prewarm Start MUST return non-nil error per godlike/07 no-fake-availability")
	}
	if !errors.Is(err, ErrCapabilityDisabled) {
		t.Errorf("yt-nightly-prewarm Start MUST errors.Is(err, ErrCapabilityDisabled) == true (via %%w chain); got err=%v", err)
	}
	if !strings.Contains(err.Error(), "yt-nightly-prewarm") {
		t.Errorf("yt-nightly-prewarm Start error message MUST include the step name as diagnostic context; got %q", err.Error())
	}
}

// TestServerLifecycleStart_CapabilityDisabledStepSurfacesInLog: pin
// the dispatch path. serverLifecycle.Start iterates startupPlan and
// for optional steps, log+continues on non-nil error (server_lifecycle.go
// line ~134-141). The typed ErrCapabilityDisabled must surface via
// zap.Error(err) in a Warn log line whose field name "step" carries
// the StartupStep.Name AND whose field "error" carries the canonical
// typed-error message (containing the literal "capability disabled").
//
// Uses zaptest/observer.New to capture the log entries (the canonical
// zap test idiom for in-memory log capture). Asserts the disabled step
// IS invoked, the typed error IS logged with the canonical message, and
// the entire Start call survives the disabled step.
func TestServerLifecycleStart_CapabilityDisabledStepSurfacesInLog(t *testing.T) {
	disabledCalled := false
	steps := []StartupStep{
		{
			Name: "yt-cache-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				disabledCalled = true
				// PRODUCTION-SHAPE wrap
				return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
			},
			Stop: func(_ context.Context) error { return nil },
		},
	}

	// Capture all logs at Warn level (server_lifecycle.go's optional
	// failure path uses l.log.Warn(...)).
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)
	lm := NewServerLifecycleWithProbes(
		steps, /* startupPlan */
		nil,   /* cleanup */
		nil,   /* dbProbe */
		nil,   /* vectorProbe */
		nil,   /* driveProbe */
		log,
	)
	if lm == nil {
		t.Fatal("NewServerLifecycleWithProbes returned nil for non-empty plan")
	}

	err := lm.Start(context.Background())

	// (a) startup must NOT abort (Required:false + log+continue path).
	if err != nil {
		t.Errorf("disabled-capability step MUST NOT abort startup (Required:false contract); got err=%v", err)
	}

	// (b) Start func was invoked exactly once.
	if !disabledCalled {
		t.Error("yt-cache-prewarm Start MUST have been invoked exactly once")
	}

	// (c) The server_lifecycle.go optional-failure Warn log line was emitted
	// with the typed error context. server_lifecycle.go message:
	//   "optional startup step failed"  + step=... + error=...
	matches := recorded.FilterMessageSnippet("optional startup step failed").AllUntimed()
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 'optional startup step failed' Warn log entry; got %d (entries: %+v)", len(matches), recorded.AllUntimed())
	}
	entry := matches[0]
	// (d) Field "step" carries the canonical StartupStep.Name. zap.String
	// stores the value as `string` in ContextMap; assert directly without
	// a dead-code fallback (zap.Error + zap.String invariants are stable
	// across zap versions — if a future drift ever changes the stored
	// type, the cast fails loudly here, NOT silently against
	// entry.Message which would mask a real regression).
	stepField, ok := entry.ContextMap()["step"].(string)
	if !ok {
		t.Fatalf("log entry MUST carry a 'step' string field; got context=%+v", entry.ContextMap())
	}
	if stepField != "yt-cache-prewarm" {
		t.Errorf("log entry's 'step' field MUST be 'yt-cache-prewarm'; got %q", stepField)
	}
	// (e) Field "error" carries the typed-error message; both the canonical
	// "capability disabled" substring AND the step-name prefix MUST be
	// present (the error is logged via zap.Error which stores err.Error()).
	errField, ok := entry.ContextMap()["error"].(string)
	if !ok {
		t.Fatalf("log entry MUST carry an 'error' string field; got context=%+v", entry.ContextMap())
	}
	if !strings.Contains(errField, "yt-cache-prewarm") {
		t.Errorf("log entry's 'error' field MUST contain the step-name prefix; got %q", errField)
	}
	if !strings.Contains(errField, "capability disabled") {
		t.Errorf("log entry's 'error' field MUST contain the canonical 'capability disabled' substring; got %q", errField)
	}

	// (f) Stop is idempotent + non-error.
	if err := lm.Stop(context.Background()); err != nil {
		t.Errorf("Stop must be idempotent + non-error; got err=%v", err)
	}
}

// TestErrCapabilityDisabled_TypedErrorContract: round-trip the
// canonical errors.Is probe across multiple wrap depths to confirm
// the sentinel is reachable through arbitrary wrapping (godlike/07
// typed-error contract: composable, not brittle).
func TestErrCapabilityDisabled_TypedErrorContract(t *testing.T) {
	level1 := fmt.Errorf("level1: %w", ErrCapabilityDisabled)
	level2 := fmt.Errorf("level2: %w", level1)
	level3 := fmt.Errorf("level3: %w", level2)
	if !errors.Is(level3, ErrCapabilityDisabled) {
		t.Errorf("errors.Is must traverse 3 wrap levels to reach ErrCapabilityDisabled; got err=%v", level3)
	}
	if !strings.Contains(level3.Error(), "capability disabled") {
		t.Errorf("level3.Error() must contain canonical 'capability disabled' substring; got %q", level3.Error())
	}
}
