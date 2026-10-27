// Package delivery — canonical alphanumeric-content check.
package delivery

import "unicode"

// ContainsAlphanumeric is the exported entry point for the canonical
// alphanumeric-content check helper.
func ContainsAlphanumeric(s string) bool { return containsAlphanumeric(s) }

// containsAlphanumeric returns true if s contains at least one letter
// or digit. Used to reject slugs that sanitize to pure punctuation
// (e.g. "___", "---", "...").
func containsAlphanumeric(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
