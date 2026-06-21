// Package portutil provides typed-nil detection for interface-typed port
// fields in the composition root. In Go, an interface variable that holds a
// nil concrete pointer (e.g. `var p io.Writer = (*MyWriter)(nil)`) evaluates
// to `p != nil` because the interface carries type metadata even when the
// underlying pointer is nil. The == nil guard in Service methods
// (`if s.port == nil`) does NOT protect against this — a typed-nil port
// passes the guard and panics on the first method call.
//
// Use IsNilPort as a defensive complement to == nil:
//
//	if s.port == nil || portutil.IsNilPort(s.port) {
//	    return fmt.Errorf("port not wired")
//	}
//
// Prefer composition-time fixes (return bare nil from adapter constructors
// when the inner value is nil) over runtime IsNilPort checks. This utility is
// the safety net for call sites that cannot control the composition side.
package portutil

import "reflect"

// IsNilPort reports whether v is a nil interface (v == nil) or a non-nil
// interface that wraps a nil concrete value (typed-nil). Returns false
// for non-nilable types (structs, ints, strings, etc.).
//
// IsNilPort uses reflect and is NOT suitable for hot-path nil guards. Use it
// at composition time (once per port wiring) or in initialization guards,
// not in per-request method calls where the overhead of reflect would
// dominate.
// Note: IsNilPort accepts any (not a generic type parameter) because the
// caller always passes an interface-typed port field. A generic
// IsNilPort[T any] is semantically appealing but creates type-inference
// friction (the caller must supply [T] for bare nil) and the reflect path
// through &v in a generic body differs from the direct interface path.
// IsNilPort(any) is the simpler, battle-tested shape.
func IsNilPort(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}
