// Package newerrit provides typed-sentinel infrastructure for
// constructor fail-fast surfaces where the godlike/05 panic-on-nil
// invariant must coexist with godlike/07 typed-error semantics.
//
// Why this package exists.
//
// The codebase has 100+ `New*` constructors that emit raw
// `panic(<string>)` when a required dependency is nil. Each panic
// call is invisible to instrumentation:
//
//   - Prometheus counters cannot label by dep name.
//   - Structured logs cannot template the dep field.
//   - Ops dashboards cannot break down panic rates by dependency.
//
// The migration forward-pointer **PR-RUNTIME-CONSTRUCTOR-TYPED-ERRORS**
// will replace:
//
//	if deps.Log == nil {
//	    panic("svc.New: Log is required")
//	}
//
// with:
//
//	if err := newerrit.RequireNotNil("svc.New: Log", deps.Log); err != nil {
//	    panic(err)
//	}
//
// The fail-fast semantic is preserved (panic still fires) but the
// panic-value is now *NilDependencyError (typed) — instrumentable.
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - pkg/newerrit owns the typed NilDependencyError struct
//   - pkg/newerrit owns the ErrNilDependency sentinel value
//   - constructor code emits the typed panic, NOT a new ad-hoc struct
//
// godlike/07 typed-error contract:
//
//   - Every nil dep is auditable (Name field); no anonymous bool checks
//   - errors.Is(err, ErrNilDependency) returns true for any *NilDependencyError
//   - errors.As(err, &typedErr) recovers the Name field for instrumentation
//   - fmt.Errorf("...%w", typedErr) chains still pass errors.Is probes
package newerrit

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// ErrNilDependency is the canonical sentinel for the nil-dep
// fail-fast class. errors.Is(err, ErrNilDependency) returns true
// for any *NilDependencyError (typed via the Is method), and for
// any fmt.Errorf("...%w", typedErr) wrap chain (via Unwrap).
//
// Prometheus instrumentation: a counter increment keyed by
// `errors.Is` matching surfaces the canonical "constructor
// fail-fast" class without coupling to specific dep names.
var ErrNilDependency = errors.New("newerrit: constructor dependency is nil")

// NilDependencyError is the typed carrier returned by RequireNotNil
// when val is nil. The Name field carries the dep name as the
// caller passed it (e.g. "ChannelMonitor.deps.Logger") so Prometheus
// counters can emit per-dep labels and structured logs can template
// the canonical dep identifier.
//
// Implementations of the godlike/07 typed-error contract:
//
//   - Error() formats a stable, grep-friendly message
//   - Is(target) supports errors.Is sentinel probes
//   - Unwrap() surfaces ErrNilDependency so fmt.Errorf wrap chains still
//     pass errors.Is probes via the typed-A path
type NilDependencyError struct {
	Name string
}

// Error formats a stable, grep-friendly message. Includes the Name
// field via strconv.Quote so quoted names round-trip through
// spelling-sensitive log scanners (godlike/07 instrumentation).
//
// Output shape (godlike/07 typed-error contract — MUST stay stable
// because existing log scanners key on the prefix "newerrit:"):
//
//	newerrit: constructor dependency "X" is nil
func (e *NilDependencyError) Error() string {
	return fmt.Sprintf("newerrit: constructor dependency %s is nil", strconv.Quote(e.Name))
}

// Is supports errors.Is(err, ErrNilDependency) sentinel probes.
//
// Matching modes (godlike/07 instrumentation contract — load-bearing
// for Prometheus counter aggregation, where two constructor-panics
// with the same Name MUST be errors.Is-equal so the counter dedup's
// them as the same failure class):
//
//  1. POINTER EQUITY: same `*NilDependencyError` value across two
//     paths matches (canonical case).
//
//  2. VALUE EQUITY: two `*NilDependencyError` values with equal Name
//     fields match. NilDependencyError has ONLY one field (Name
//     string), which is comparable; value-equality is well-defined.
//
//  3. SENTINEL EQUITY: match against the bare ErrNilDependency sentinel
//     when target is not a typed pointer.
func (e *NilDependencyError) Is(target error) bool {
	if t, ok := target.(*NilDependencyError); ok {
		return t == e || *t == *e
	}
	return target == ErrNilDependency
}

// Unwrap surfaces ErrNilDependency so fmt.Errorf(...%w, typedErr) wrap
// chains pass errors.Is probes. The typed-A path is preferred
// (errors.As), but this keeps the sentinel path consistent across
// wraps.
func (e *NilDependencyError) Unwrap() error {
	return ErrNilDependency
}

// typedNilKinds is the allowlist of reflect.Kind values for which an
// underlying-nil check is the godlike/07 NO-FAKE-AVAILABILITY
// requirement. Pointer / Map / Slice / Chan / Func / Interface
// types can be "non-nil at the interface layer but nil at the
// underlying value layer" — the canonical typed-nil pitfall.
//
// Kind additions are explicit-and-conservative: only kinds whose
// underlying-type has a meaningful "zero value" semantic. Numeric
// kinds (Int/Uint/Float/Complex), String, Struct, Array, Bool all
// have a zero-value-at-the-value-layer that is NOT a pointer-ish
// "could-nil-trap" — they are excluded by design. UnsafePointer
// is excluded because reflect.Value.IsNil() panics for that kind.
//
// Reference: https://pkg.go.dev/reflect#Value.IsNil — calls IsNil()
// ON the underlying value, not the wrapping interface.
var typedNilKinds = map[reflect.Kind]bool{
	reflect.Ptr:       true,
	reflect.Map:       true,
	reflect.Slice:     true,
	reflect.Chan:      true,
	reflect.Func:      true,
	reflect.Interface: true,
}

// isTypedNil returns true when val is wrapped non-nil-interface
// around an underlying typed-nil value. Returns false when:
//
//   - val is plain-nil interface (val == nil) — RequireNotNil handles this directly
//   - val's reflect.Kind is not in typedNilKinds
//   - val's underlying value is non-nil
//
// godlike/07 NO-FAKE-AVAILABILITY: this is the load-bearing edge case.
// Without reflection-based detection, the helper silently passes
// `var p *T = nil; newerrit.RequireNotNil("p", p)` as "non-nil" — the
// user spec mandates fail-fast on this anti-pattern.
func isTypedNil(val any) bool {
	v := reflect.ValueOf(val)
	if !v.IsValid() {
		return true // reflect.ValueOf(nil) returns an invalid Value
	}
	if !typedNilKinds[v.Kind()] {
		return false
	}
	// v.IsNil() panics on kinds where IsNil isn't defined; we filter via the
	// allowlist above, so this is safe.
	return v.IsNil()
}

// RequireNotNil returns a typed *NilDependencyError when val is
// (a) plain-nil interface OR (b) wrapped typed-nil pointer/map/slice/
// chan/func/interface, nil otherwise.
//
// The canonical usage pattern for New* constructors:
//
//	if err := newerrit.RequireNotNil("ChannelMonitor.deps.Logger", deps.Logger); err != nil {
//	    panic(err)
//	}
//
// The fail-fast semantic is preserved (panic still fires) but the
// panic-value is now typed: errors.Is(err, ErrNilDependency) returns
// true for any siting of this helper, regardless of the specific
// dep name.
//
// Per godlike/07 minimum-blast-radius: the helper is small (~5 LoC)
// and replaces 100+ panic("X is nil") strings with typed sentinels.
// Migration is forward-pointer PR-RUNTIME-CONSTRUCTOR-TYPED-ERRORS
// (target 2026-08-15 per architecture/current.yaml).
//
// NOTE on nil vs empty: this helper considers `val == nil`
// (Go's typed-nil check, NOT a "is empty" check). An empty string or
// zero value is NOT nil at the typed layer, so callers wanting
// "non-empty" semantics should layer a separate check. The signature
// `any` (rather than `interface{ IsNil() bool }`) keeps the call site
// trivial: any nil pointer, slice, map, channel, or interface
// returns an error; any non-nil value (including zero-value scalars)
// returns nil. Empty string (`""`), zero int (`0`), and zero-value
// structs are typed non-nil and pass the helper without modification.
func RequireNotNil(name string, val any) error {
	if val == nil {
		return &NilDependencyError{Name: name}
	}
	// Layer 2: typed-nil detection via reflection. Catches the canonical
	// "(*T)(nil) wrapped in any" pitfall where val == nil is FALSE at
	// the interface layer but TRUE at the underlying-value layer.
	if isTypedNil(val) {
		return &NilDependencyError{Name: name}
	}
	return nil
}
