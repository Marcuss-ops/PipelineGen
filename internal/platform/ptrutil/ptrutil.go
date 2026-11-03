// Package ptrutil provides utilities for working with pointer types.
package ptrutil

// DerefOr returns the dereferenced value, or fallback if the pointer is nil.
func DerefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// BoolDefault returns the value of a *bool or def if nil.
func BoolDefault(v *bool, def bool) bool {
	return DerefOr(v, def)
}
