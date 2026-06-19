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

// Bool returns a pointer to the given bool value.
// Deprecated: use Ptr(v) instead.
func Bool(v bool) *bool {
	return &v
}

// Str returns a pointer to the given string value.
// Deprecated: use Ptr(v) instead.
func Str(v string) *string {
	return &v
}

// BoolDefault returns the value of a *bool or def if nil.
func BoolDefault(v *bool, def bool) bool {
	return DerefOr(v, def)
}

