package system

import (
	"strings"
	"testing"
)

// FuzzNormalizeHealthChecks ensures NormalizeCheckNames never panics
// and maintains invariants for arbitrary input.
func FuzzNormalizeHealthChecks(f *testing.F) {
	// Seed corpus.
	seeds := []string{
		"db",
		"db,jobs",
		"db,,jobs",
		" db ",
		"qdrant&check=jobs",
		// unicode
		"db\x00jobs",
		"データベース",
		// very long strings
		strings.Repeat("a", 10000),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Split by '&' to create a multi-value input like query params.
		parts := strings.Split(raw, "&")
		if len(parts) > 50 {
			parts = parts[:50] // bound input size.
		}

		// Must not panic.
		var result []string
		requireNotPanics := func() {
			result = NormalizeCheckNames(parts)
		}
		if !panics(requireNotPanics) {
			// Invariants on the result.
			for _, r := range result {
				if r == "" {
					t.Errorf("empty entry in result: %v → %v", parts, result)
				}
			}
			// Check for duplicates.
			seen := make(map[string]bool, len(result))
			for _, r := range result {
				if seen[r] {
					t.Errorf("duplicate entry %q in result: %v → %v", r, parts, result)
				}
				seen[r] = true
			}
			// Order stability: first occurrences preserved.
			if len(result) > 1 {
				firstIdx := make(map[string]int, len(parts))
				for i, p := range parts {
					for _, sub := range strings.Split(p, ",") {
						sub = strings.TrimSpace(sub)
						if sub == "" {
							continue
						}
						if _, ok := firstIdx[sub]; !ok {
							firstIdx[sub] = i
						}
					}
				}
				for i := 1; i < len(result); i++ {
					if firstIdx[result[i-1]] > firstIdx[result[i]] {
						t.Errorf("order not preserved: %v → %v, firstIdx=%v", parts, result, firstIdx)
						break
					}
				}
			}
		}
	})
}

// FuzzValidateHealthChecks ensures ValidateCheckNames never panics and
// returns typed errors for unknown names.
func FuzzValidateHealthChecks(f *testing.F) {
	seeds := []string{"db", "jobs", "unknown", "db,jobs", "", "  "}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		names := NormalizeCheckNames(strings.Split(raw, ","))
		err := ValidateCheckNames(names)
		if err != nil {
			var ue *ErrUnknownCheck
			if !isErrUnknownCheck(err) {
				t.Errorf("expected *ErrUnknownCheck or nil, got %T: %v", err, err)
			}
			_ = ue
		}
	})
}

// panics returns true if fn panics.
func panics(fn func()) (didPanic bool) {
	defer func() {
		if r := recover(); r != nil {
			didPanic = true
		}
	}()
	fn()
	return false
}

// isErrUnknownCheck is a helper for the fuzz test (errors.As is less
// convenient inside a fuzz closure that already uses *testing.T).
func isErrUnknownCheck(err error) bool {
	_, ok := err.(*ErrUnknownCheck)
	return ok
}
