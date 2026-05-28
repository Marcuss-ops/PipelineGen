// Package ptrutil provides utilities for working with pointer types.
package ptrutil

// BoolDefault returns the value of the bool pointer, or the default value if nil.
func BoolDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// Bool returns a pointer to the given bool value.
func Bool(v bool) *bool {
	return &v
}
