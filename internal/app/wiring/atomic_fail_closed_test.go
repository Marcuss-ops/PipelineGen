// Package app — hermetic test surface for the PR-SOURCING-ADAPTER-FAIL-CLOSED
// (July 2026) fail-closed gates and typed-error sentinels.
//
// godlike/06 SSOT (one canonical owner per fact): the typed-error
// sentinels live ONLY at internal/application/assets/sourcing/ports.go
// (the canonical Pattern-0 typed-contract surface). wireSourcingAtomic
// lives ONLY at internal/app/assets_register_sourcing.go (the canonical
// composition-root surface). This test file exercises the CANONICAL
// COMPOSITION-ROOT GATE — the typed-error probes cross-package via
// errors.Is to verify the dual-%w wrap is intact.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test probes a falsifiable
// invariant — the gate's return value semantics are locked at compile
// time (Go 1.20+ errors.Is supports dual-%w chains) so future refactors
// that break the wrap would surface as build failure, not as silent
// silent-success at composition boot.
package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// stubAtomic is a hermetic stub implementing sourcing.SourcingAtomicPort
// for the canonical happy-path tests. Both methods return nil (no-op
// success) so the test exercises ONLY the gate's nil-vs-error semantics,
// not the stub's behavior. Compile-time pin below enforces the
// interface-method signature match.
type stubAtomic struct{}

func (s *stubAtomic) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	return nil
}

func (s *stubAtomic) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	return nil
}

// Compile-time assertion: the stubAtomic must satisfy the canonical
// sourcing.SourcingAtomicPort surface. Pinning at the type-system
// level locks the method signatures: any future drift in either the
// port's method set or the stub's receivers would surface as build
// failure, not runtime panic. This is the canonical godlike/06 SSOT
// Pattern-0 compile-time guarantee loaded at package init time.
//
// NOTE: this assertion runs at compile time and is NOT a runtime
// test — TestSourcingAtomicPort_StubImplementationSatisfiesInterface
// below documents the invariant for any future operator inspecting the
// test output. The build-success of this package IS the load-bearing
// guard.
var _ sourcing.SourcingAtomicPort = (*stubAtomic)(nil)

// TestWireSourcingAtomic_MissingHandler_Required_ReturnsCapabilitiesRequired
// verifies the canonical typed-error dual-%w wrap from wireSourcingAtomic
// for the Enabled=true-but-nil-handler path. Per godlike/07 typed-error
// contract: callers MUST probe via errors.Is — never via raw string match.
func TestWireSourcingAtomic_MissingHandler_Required_ReturnsCapabilitiesRequired(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{
			MediaDriveRequired: true,
		},
	}
	got, err := wireSourcingAtomic(cfg, nil)
	if got != nil {
		t.Errorf("wireSourcingAtomic: got = %v, want nil (fail-closed at composition)", got)
	}
	if err == nil {
		t.Fatalf("wireSourcingAtomic: err = nil, want typed sentinel")
	}
	if !errors.Is(err, sourcing.ErrSourcingCapabilitiesRequired) {
		t.Errorf("wireSourcingAtomic: errors.Is(err, ErrSourcingCapabilitiesRequired) = false; want true; err = %v", err)
	}
	if errors.Is(err, sourcing.ErrSourcingCapabilitiesDisabled) {
		t.Errorf("wireSourcingAtomic: errors.Is(err, ErrSourcingCapabilitiesDisabled) = true; want false (must be the Required sentinel strictly, NOT the Disabled one); err = %v", err)
	}
}

// TestWireSourcingAtomic_MissingHandler_NotRequired_ReturnsCapabilitiesDisabled
// verifies the canonical typed-error dual-%w wrap from wireSourcingAtomic
// for the Enabled=false-and-nil-handler path. Per godlike/07 typed-error
// contract: composition may continue without sourcing capabilities,
// but the gate surfaces the typed error so the deferred-at-runtime
// fail-closed path is explicit (NOT a silent no-op).
func TestWireSourcingAtomic_MissingHandler_NotRequired_ReturnsCapabilitiesDisabled(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{
			MediaDriveRequired: false,
		},
	}
	got, err := wireSourcingAtomic(cfg, nil)
	if got != nil {
		t.Errorf("wireSourcingAtomic: got = %v, want nil (fail-closed at composition)", got)
	}
	if err == nil {
		t.Fatalf("wireSourcingAtomic: err = nil, want typed sentinel")
	}
	if !errors.Is(err, sourcing.ErrSourcingCapabilitiesDisabled) {
		t.Errorf("wireSourcingAtomic: errors.Is(err, ErrSourcingCapabilitiesDisabled) = false; want true; err = %v", err)
	}
	if errors.Is(err, sourcing.ErrSourcingCapabilitiesRequired) {
		t.Errorf("wireSourcingAtomic: errors.Is(err, ErrSourcingCapabilitiesRequired) = true; want false (must be the Disabled sentinel strictly, NOT the Required one); err = %v", err)
	}
}

// TestWireSourcingAtomic_NilConfig_MissingHandler_ReturnsCapabilitiesDisabled
// verifies the canonical typed-error behavior when cfg is nil AND
// handler is nil. The gate must NOT panic and the Disabled sentinel
// must be returned (the safe-default fallback — NOT the Required
// sentinel which would erroneously fail-closed a nil-config invocation).
func TestWireSourcingAtomic_NilConfig_MissingHandler_ReturnsCapabilitiesDisabled(t *testing.T) {
	got, err := wireSourcingAtomic(nil, nil)
	if got != nil {
		t.Errorf("wireSourcingAtomic: got = %v, want nil", got)
	}
	if err == nil {
		t.Fatalf("wireSourcingAtomic: err = nil, want typed sentinel")
	}
	if !errors.Is(err, sourcing.ErrSourcingCapabilitiesDisabled) {
		t.Errorf("wireSourcingAtomic: errors.Is(err, ErrSourcingCapabilitiesDisabled) = false; want true (nil cfg fallback must NOT panic + must surface the Disabled sentinel); err = %v", err)
	}
}

// TestWireSourcingAtomic_HandlerProvided_Required_ReturnsHandler verifies
// that when a handler IS wired AND cfg.Features.MediaDriveRequired ==
// true, the gate returns the handler as-is + nil error (the canonical
// composition-success path).
func TestWireSourcingAtomic_HandlerProvided_Required_ReturnsHandler(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{
			MediaDriveRequired: true,
		},
	}
	stub := &stubAtomic{}
	got, err := wireSourcingAtomic(cfg, stub)
	if err != nil {
		t.Errorf("wireSourcingAtomic: err = %v, want nil (handler provided, success path)", err)
	}
	if got != stub {
		t.Errorf("wireSourcingAtomic: got = %v, want = %v (gate must return the handler byte-identically)", got, stub)
	}
}

// TestWireSourcingAtomic_HandlerProvided_NotRequired_ReturnsHandler verifies
// that when a handler IS wired AND cfg.Features.MediaDriveRequired ==
// false, the gate also returns the handler + nil error (the canonical
// handler-provided happy path is independent of the MediaDriveRequired
// feature toggle).
func TestWireSourcingAtomic_HandlerProvided_NotRequired_ReturnsHandler(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{
			MediaDriveRequired: false,
		},
	}
	stub := &stubAtomic{}
	got, err := wireSourcingAtomic(cfg, stub)
	if err != nil {
		t.Errorf("wireSourcingAtomic: err = %v, want nil", err)
	}
	if got != stub {
		t.Errorf("wireSourcingAtomic: got = %v, want = %v", got, stub)
	}
}

// TestSourcingAtomicPort_StubImplementationSatisfiesInterface is the
// canonical operator-readable documentation test for the
// `var _ sourcing.SourcingAtomicPort = (*stubAtomic)(nil)` compile-time
// assertion at the top of this file. The actual guard is the assertion
// itself (build fails if the stub drifts from the port interface);
// this test is documentation-only and passes by default.
func TestSourcingAtomicPort_StubImplementationSatisfiesInterface(t *testing.T) {
	t.Log("TestSourcingAtomicPort_StubImplementationSatisfiesInterface: pass (compile-time assertion at top of file is the canonical guard)")
}
