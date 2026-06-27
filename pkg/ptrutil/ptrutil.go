// Package ptrutil provides utilities for working with pointer types.
package ptrutil

// Ptr returns a pointer to the given value. This replaces the ad-hoc pattern
// of declaring a temporary variable just to take its address.
func Ptr[T any](v T) *T {
	return &v
}

// DerefOr returns the dereferenced value, or fallback if the pointer is nil.
func DerefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// Bool is a convenience alias for Ptr[bool].
func Bool(v bool) *bool { return Ptr(v) }

// Str is a convenience alias for Ptr[string].
func Str(v string) *string { return Ptr(v) }

// BoolDefault returns the value of a *bool or def if nil.
func BoolDefault(v *bool, def bool) bool {
	return DerefOr(v, def)
}
