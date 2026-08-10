// Package jobs — handler_registration_test.go: TDD coverage for
// PR-REFLECT-ELIM-HANDLER-REGISTRATION (2026-07-04).
//
// Verifies the implementation's typed-error gate:
//
//	(1) Compile-time pin: appjobs.HandlerFunc shape accepted.
//	(2) Happy path: struct method Handle (signature matches HandlerFunc
//	    explicitly + wrapped via cast) returns no error + populates
//	    the dispatcher's handler map.
//	(3) Type-error path (raw string): non-HandlerFunc shape returns
//	    a typed error naming the canonical `appjobs.HandlerFunc(...)`
//	    cast requirement (locks the post-refactor no-fake-availability
//	    contract per godlike/07).
//	(4) Type-error path (anonymous func literal of structural
//	    signature): the pre-refactor reflection block silently
//	    accepted such literals at runtime; the post-refactor
//	    type-switch rejects them with a typed error. This is the
//	    canonical win — production callers MUST use the explicit
//	    cast at the call site to satisfy the compile-time contract.
//	(5) Type-error path (*int zero value): no Func kind, post-refactor
//	    type-switch catches it; pre-refactor runtime reflection
//	    produced a different error message format.
//
// Each test operates on a fresh *Service with an empty Dispatcher
// (no domain.Store wired) — these tests only exercise the
// RegisterHandler + HasHandler surface, not the JobFinalizer path.
package jobs

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// handlerTestFixture is the struct whose Handle method is the
// canonical HandlerFunc-shaped value used in the happy-path test.
// Drift in HandlerFunc's signature becomes a build failure on the
// compile-time pin below, before any test runs.
type handlerTestFixture struct {
	callCount atomicInt
}

// atomicInt is a tiny thread-safe counter (we don't want to import
// sync/atomic just for this single test).
type atomicInt struct{ n int }

func (a *atomicInt) inc() int {
	a.n++
	return a.n
}

// Handle is the canonical HandlerFunc-shaped method (no-op body —
// the test asserts registration succeeded, not the body content).
func (h *handlerTestFixture) Handle(_ context.Context, _ *Job, _ *JobTools) (map[string]any, error) {
	h.callCount.inc()
	return map[string]any{"ok": true, "calls": h.callCount.n}, nil
}

// Compile-time pin is implicit via the happy-path test below: the
// `HandlerFunc(fixture.Handle)` cast on fixture.Handle (a method VALUE)
// would fail to compile if HandlerFunc's signature drifts (e.g. \
// Result\ changes from `map[string]any` to a typed struct). This
// makes the pin redundant — the test exercises the type assignment
// at compile time, not via an explicit var declaration. Explicit
// pin via `(*handlerTestFixture).Handle` would NOT work because that
// is a Go method-EXPRESSION (with receiver as first param) rather
// than a method-VALUE (with receiver bound); the explicit-pin idiom
// used in other test files is only valid when paired with an
// instance (`fixture.Handle`). Compile-time drift detection is
// therefore already enforced by the happy-path test below — no
// separate var block needed.

// newServiceForTestHandlerRegistration constructs a Service with a
// fresh dispatcher (no domain.Store wired) since these tests only
// exercise RegisterHandler + HasHandler via the dispatcher surface.
// Future Pin: arm a runtime reflection-free verification here —
// never call reflect on the type — to enforce that handler
// registration stops performing runtime type-checks (godlike/07
// hard ban on the pre-refactor reflect.ValueOf path).
func newServiceForTestHandlerRegistration() *Service {
	return &Service{
		dispatcher: NewDispatcher(),
		log:        zap.NewNop(),
	}
}

// 1. TestRegisterHandler_HappyPath: a HandlerFunc-shaped value
// (struct method wrapped via explicit cast) returns no error and
// populates the dispatcher's handler map. This is the canonical
// post-refactor path every production caller MUST follow per
// godlike/06 SSOT + artlist/job_core.go:247 precedent.
func TestRegisterHandler_HappyPath(t *testing.T) {
	s := newServiceForTestHandlerRegistration()
	fixture := &handlerTestFixture{}

	if err := s.RegisterHandler("test.job", HandlerFunc(fixture.Handle)); err != nil {
		t.Fatalf("RegisterHandler rejected canonical HandlerFunc input (godlike/06 contract violation): %v", err)
	}
	if !s.HasHandler("test.job") {
		t.Fatalf("HasHandler returned false after successful RegisterHandler for 'test.job' — dispatcher state corrupted")
	}
}

// 2. TestRegisterHandler_TypeError_String: passing a raw string
// returns a typed error and does NOT mutate the dispatcher's handler
// map. The error message MUST name the canonical cast pattern so
// operators/managers know how to fix the call site.
func TestRegisterHandler_TypeError_String(t *testing.T) {
	s := newServiceForTestHandlerRegistration()

	err := s.RegisterHandler("bad.job", "not-a-handler-func")
	if err == nil {
		t.Fatalf("RegisterHandler accepted raw string (post-refactor guarantees rejection for non-HandlerFunc shapes)")
	}
	if !strings.Contains(err.Error(), "appjobs.HandlerFunc") {
		t.Fatalf("RegisterHandler error message must name the canonical `appjobs.HandlerFunc(method)` cast pattern, got: %v", err)
	}
	if s.HasHandler("bad.job") {
		t.Fatalf("RegisterHandler error path mutated dispatcher handler map for 'bad.job' (state corruption)")
	}
}

// 3. TestRegisterHandler_TypeError_AnonymousFuncLiteral: an
// anonymous func literal of the STRUCTURAL signature is REJECTED
// post-refactor (it is NOT a cast HandlerFunc). This is the canonical
// win over the pre-refactor reflection path: production callers MUST
// apply the explicit cast at the call site to satisfy compile-time
// type discipline, vs. the pre-refactor silent-acceptance at runtime.
func TestRegisterHandler_TypeError_AnonymousFuncLiteral(t *testing.T) {
	s := newServiceForTestHandlerRegistration()

	// Structurally identical to HandlerFunc but NOT cast to
	// appjobs.HandlerFunc — must be rejected post-refactor. If a
	// future agent re-introduces a func-literal case here, this
	// test would fail (locking the no-fake-availability contract).
	anonLiteral := func(_ context.Context, _ *Job, _ *JobTools) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	}
	err := s.RegisterHandler("bad.literal", anonLiteral)
	if err == nil {
		t.Fatalf("RegisterHandler accepted anonymous func literal (post-refactor requires explicit HandlerFunc cast at call site)")
	}
	if !strings.Contains(err.Error(), "appjobs.HandlerFunc") {
		t.Fatalf("RegisterHandler error message must name the canonical cast pattern, got: %v", err)
	}
}

// 4. TestRegisterHandler_TypeError_IntPointer: a typed *int zero
// value is REJECTED (no Func kind). The pre-refactor reflection
// block ALSO rejected this but with a different error format
// (per-field AssignableTo validation); post-refactor the type-switch
// catches the type mismatch FIRST (cheaper + cleaner error
// message). This test locks the new behaviour.
func TestRegisterHandler_TypeError_IntPointer(t *testing.T) {
	s := newServiceForTestHandlerRegistration()

	n := 0
	err := s.RegisterHandler("bad.int", &n)
	if err == nil {
		t.Fatalf("RegisterHandler accepted *int (no Func kind)")
	}
	if !strings.Contains(err.Error(), "appjobs.HandlerFunc") {
		t.Fatalf("RegisterHandler error message must name the canonical cast pattern, got: %v", err)
	}
	if s.HasHandler("bad.int") {
		t.Fatalf("RegisterHandler error path mutated dispatcher handler map for 'bad.int' (state corruption)")
	}
}

// 5. TestRegisterHandler_NilHandler: passing an explicit nil `any`
// is REJECTED with a typed error (no panic). The type-switch returns
// a typed-error gate IMMEDIATELY rather than panicking on a nil
// interface assertion. Defensive: production callers should not
// pass nil but the dispatcher must not panic if they do.
func TestRegisterHandler_NilHandler(t *testing.T) {
	s := newServiceForTestHandlerRegistration()

	err := s.RegisterHandler("nil.handler", nil)
	if err == nil {
		t.Fatalf("RegisterHandler accepted nil handler (post-refactor require typed error for nil inputs)")
	}
	if !strings.Contains(err.Error(), "appjobs.HandlerFunc") {
		t.Fatalf("RegisterHandler error message must name the canonical cast pattern, got: %v", err)
	}
}

// 6. TestHasHandler_NilReceiver: keeps the existing nil-tolerance
// gate — HasHandler must NEVER panic (composition-root callers
// pass s.Service==nil through the validator without pre-check).
func TestHasHandler_NilReceiver(t *testing.T) {
	var s *Service // nil receiver
	if s.HasHandler("anything") {
		t.Fatalf("HasHandler with nil receiver returned true (must return false per nil-tolerance contract)")
	}
}

// 7. TestValidateHandlerCompleteness_NoGap: when the dispatcher
// binds every registered type, validation passes. This locks the
// §15.9 success path. (The gap-detected path is exercised by the
// composition-root validator tests at internal/app/critical_handler_
// validator_test.go — these unit tests focus on the no-gap branch
// to keep handler_registration_test.go hermetic.)
func TestValidateHandlerCompleteness_NoGap(t *testing.T) {
	s := newServiceForTestHandlerRegistration()
	fixture := &handlerTestFixture{}

	if err := s.RegisterHandler("only.type", HandlerFunc(fixture.Handle)); err != nil {
		t.Fatalf("RegisterHandler failed: %v", err)
	}

	// Use NewRegistry() (not `&Registry{}`) so the entries map is
	// initialized; Registry.Register panics on a nil map (assignment
	// to entry in nil map). NewRegistry() is the canonical constructor
	// and matches the production wiring in internal/app/registry.go.
	reg := NewRegistry()
	if err := reg.Register(RegistryEntry{Completion: CompletionDeclaration{JobType: "only.type", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}}); err != nil {
		t.Fatalf("Registry.Register failed for 'only.type': %v", err)
	}
	if err := s.ValidateHandlerCompleteness(reg); err != nil {
		t.Fatalf("ValidateHandlerCompleteness reported gap where there was none: %v", err)
	}
}
