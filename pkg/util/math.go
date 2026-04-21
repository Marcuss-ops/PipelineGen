// Package util provides shared utility functions used across the codebase.
package util

// Min returns the smaller of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
