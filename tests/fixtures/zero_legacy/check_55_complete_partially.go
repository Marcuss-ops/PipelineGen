//go:build ignore

// Self-check fixture for Check 55: forbid CompletePartially on Store interface.
//
// This file intentionally contains the forbidden pattern so the CI
// self-check can verify that the regex catches future regressions.
// The production codebase MUST have zero CompletePartially hits.
//
// ARCH-ALLOWLIST: check55-self-check-fixture — this file is deliberately
// excluded from the production gate via --glob '!tests/fixtures/zero_legacy/**'.
package fixture

// Example: CompletePartially must never be added to the Store interface.
type Store interface {
	// CompletePartially would be a false-success anti-pattern.
	// It must never be added to the canonical job Store contract.
	CompletePartially(ctx string) error
}
