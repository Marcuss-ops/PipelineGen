// Package job — handler_test.go (P1 #13 unification, July 2026).
//
// Pins the canonical Handler / JobExecutionTools / Result contract
// placed in the domain layer (godlike/06 SSOT — domain owns cross-
// cutting contracts that the application / worker packages both
// alias from). Because the test file is in package `job` (this
// package), it is the BELOW-BOTH importer; there is no upward
// dependency on application/jobs or worker.
//
// ── Why this lives here ─────────────────────────────────────────────
//
// Pre-P1-#13 the canonical Handler signature lived in
// internal/application/jobs/types.go. The worker sub-package
// required the same Handler, so worker/registry.go aliased it
// (`type Handler = jobs.Handler`) — worker ↛ to its parent. The
// P1-#13 test file (handler_signature_test.go in jobs pkg)
// imported worker.NewRegistry() to assert cross-registry
// assignability, which closed the cycle jobs ↔ worker at the test
// level. Moving the canonical types down to the domain layer
// eliminates the cycle: both jobs AND worker alias from
// domainjob.Handler, and the domain-package test pins the
// canonical surface without ever importing its consumers.
//
// ── Three SSOT pins ─────────────────────────────────────────────────
//
// Three properties of the canonical Handler surface are pinned:
//
//  1. Canonical signature — a function literal that declares the
//     3-arg Handler shape must compile against the Handler type.
//  2. Return-type identity — domainjob.Result is byte-equivalent
//     to map[string]any via Go type-alias semantics; existing
//     handlers that return `map[string]any{...}` still compile.
//  3. Field-set shape — domainjob.JobExecutionTools carries the
//     3-callback envelope (Progress / Event / IsCancelled). No
//     future addition to the struct is sanctioned at this audit-
//     pin site; a new field would require an EXPAND-then-BACKFILL
//     godlike/07 cycle documented next to the struct definition.
package job

import (
	"context"
	"reflect"
	"testing"
)

// canonicalLiteral is the canonical Handler function shape. Its
// type is the SSOT — if domainob.Handler ever drifts from
// `func(context.Context, *Job, *JobExecutionTools) (Result, error)`
// this literal stops compiling against the Handler type and the
// test panics at build time. This is the godlike/06 audit-pin.
func canonicalLiteral(_ context.Context, j *Job, _ *JobExecutionTools) (Result, error) {
	return Result{"job_type": j.Type}, nil
}

// TestCanonicalHandler_Shape — pins that a function literal with
// the canonical Handler body is assignable to the Handler named
// type. The compile-time assignment below IS the test; the runtime
// reflection check below is a defensive belt-and-braces check that
// the literal's runtime type IS Handler (not a wrapped interface
// or a structurally-compatible but distinct alias).
func TestCanonicalHandler_Shape(t *testing.T) {
	var h Handler = canonicalLiteral
	if h == nil {
		t.Fatalf("canonical Handler literal assignment lost; Handler reference should be non-nil when assigned from a non-nil literal")
	}
	if reflect.ValueOf(h).IsNil() {
		t.Fatalf("canonical Handler literal IS nil; handler assignments must propagate the wrapped function value")
	}

	// Reflection sanity: the Handler runtime type must have the
	// canonical 3-arg/2-return shape AND accept literal assignments
	// FROM a legacy `map[string]any` return-shape handler (below,
	// TestCanonicalHandler_ReturnTypeIsResult). The compile-time
	// `var h Handler = canonicalLiteral` IS the godlike/06 SSOT
	// lock — the runtime check below is the defensive belt-and-
	// braces that catches drift where the named Handler type is
	// accidentally re-shaped while still satisfying the literal.
	rtRuntime := reflect.TypeOf(h)
	if rtRuntime.NumIn() != 3 {
		t.Fatalf("canonical Handler must accept 3 args; got %d (godlike/06 SSOT drift)", rtRuntime.NumIn())
	}
	if rtRuntime.NumOut() != 2 {
		t.Fatalf("canonical Handler must return 2 values; got %d (godlike/06 SSOT drift)", rtRuntime.NumOut())
	}
	if rtRuntime.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		t.Fatalf("canonical Handler arg[0] must be context.Context; got %s", rtRuntime.In(0))
	}
	if rtRuntime.In(1) != reflect.TypeOf((*Job)(nil)) {
		t.Fatalf("canonical Handler arg[1] must be *domain/job.Job; got %s", rtRuntime.In(1))
	}
	if rtRuntime.In(2) != reflect.TypeOf((*JobExecutionTools)(nil)) {
		t.Fatalf("canonical Handler arg[2] must be *domain/job.JobExecutionTools; got %s", rtRuntime.In(2))
	}
}

// TestCanonicalHandler_ReturnTypeIsResult — pins that domainjob.Result
// is byte-equivalent to map[string]any via Go type-alias semantics:
// pre-P1-#13 handlers that declared return types as `map[string]any`
// still compile against the canonical Handler. The compile-time
// alias assignment below IS the test; the runtime identity check is
// the belt-and-braces defense.
func TestCanonicalHandler_ReturnTypeIsResult(t *testing.T) {
	// A pre-P1-#13-shaped handler returning `map[string]any` literal
	// must still satisfy the canonical Handler signature.
	legacy := func(_ context.Context, _ *Job, _ *JobExecutionTools) (map[string]any, error) {
		return map[string]any{"legacy": true}, nil
	}
	var h Handler = legacy
	if h == nil {
		t.Fatalf("pre-P1-#13 legacy `map[string]any` return-shape literal MUST compile against Handler (godlike/06 back-compat broken)")
	}
	got, err := h(context.Background(), &Job{Type: "test"}, &JobExecutionTools{})
	if err != nil {
		t.Fatalf("legacy Handler invocation returned error: %v", err)
	}
	if got["legacy"] != true {
		t.Fatalf("legacy return value lost through Handler assignment: got %v", got)
	}

	// Belt-and-braces: Result IS map[string]any at runtime.
	if reflect.TypeOf(got) != reflect.TypeOf(map[string]any{}) {
		t.Fatalf("domainob.Result must be byte-equivalent to map[string]any at runtime; got %s", reflect.TypeOf(got))
	}
}

// TestJobExecutionTools_FieldSet — pins the 2-callback envelope
// (FASE 4(b), July 2026).
//
// Pre-Fase-4 the struct carried 3 callbacks (Progress / Event /
// IsCancelled). FASE 4(b) removed the IsCancelled field because
// the pre-Fase-4 2-second IsCancelled-poll goroutine (the
// startCancelWatcher at worker_execution.go) is gone; cancel
// now propagates through native context cancellation (ctx.Err())
// and the typed kerneljob.RenewLeaseResult.State observation
// (CancelRequested → renewLeaseLoopWith calls jobCancel).
//
// Adding a 3rd field to the struct (godlike/07 EXPAND) is a
// future-PR decision and would be flagged by this test via
// reflection drift — the production-pinned handler signature
// accepts 2 callbacks only, so a new field requires either a
// new type or a godlike/07 4-phase migration.
func TestJobExecutionTools_FieldSet(t *testing.T) {
	rt := reflect.TypeOf(JobExecutionTools{})
	wantFields := map[string]bool{
		"Progress": false,
		"Event":    false,
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if _, ok := wantFields[f.Name]; ok {
			wantFields[f.Name] = true
		}
	}
	for name, found := range wantFields {
		if !found {
			t.Fatalf("JobExecutionTools must carry field %q (FASE 4(b) SSOT — handler signature reports a 2-callback envelope; pre-Fase-4 IsCancelled was removed when the 2s polling goroutine was retired)", name)
		}
	}
	if rt.NumField() != 2 {
		t.Fatalf("JobExecutionTools must carry exactly 2 fields (FASE 4(b)); got %d (a 3rd field would break the canonical Handler signature without a godlike/07 4-phase migration)", rt.NumField())
	}
}

// TestHandlerAliases_CompileTimeLock (P1 #13, July 2026) notes
// that runtime-reflection pins for the cross-registry alias
// identity (domainob.Handler == appjobs.Handler ==
// appjobs.HandlerFunc == worker.Handler) are NOT in this file.
// Go type aliases are SEMANTICALLY equivalent at compile time —
// `type X = Y` makes X and Y the SAME type; a runtime
// reflect.TypeOf check is redundant. The compile-time alias
// declarations in
//
//	internal/application/jobs/types.go (Handler, HandlerFunc,
//	  JobExecutionTools, Result = domainob.X)
//	internal/application/jobs/worker/registry.go
//	  (type Handler = domainob.Handler)
//
// ARE the godlike/06 SSOT lock — a future rename or
// re-declaration of the canonical Handler without updating
// the alias declarations surfaces as a build failure at the
// alias sites instantly. No additional test surface is
// required here.
func TestHandlerAliases_CompileTimeLock(t *testing.T) {
	// Compilation-time audit-pin: a slice of Handler
	// literals (compiled iff they satisfy the domain SSOT
	// signature) is the godlike/06 lock at the test-file
	// surface. If domainob.Handler ever drifts from the
	// canonical 3-arg/2-return shape, this literal stops
	// compiling.
	_ = []Handler{
		ctxJobToolsHandler,
		canonicalLiteral,
	}
}

// ctxJobToolsHandler is a second canonical-shape literal
// used as a literal-collection witness by the audit-pin
// test above. Distinct from canonicalLiteral so the
// collection has 2 distinct function values (rule out
// accidental dedup).
func ctxJobToolsHandler(ctx context.Context, _ *Job, _ *JobExecutionTools) (Result, error) {
	return Result{"ctx_seen": ctx != nil}, nil
}
