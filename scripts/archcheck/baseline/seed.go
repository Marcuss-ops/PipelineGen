// Package baseline — archcheck phase 0 baseline seeder.
//
// baseline/seed.go owns the operator-only path that writes a fresh
// scripts/archcheck/phase0_baseline.json from the current rg actuals.
// The seed function marshals a baseline.Actuals struct (computed by
// the 5 Phase 0 check functions in main.go) into JSON and writes the
// file. main.go's runSeedBaseline() builds the Actuals struct by
// calling the 5 check functions, then calls baseline.Seed().
//
// Cross-package pattern: baseline/seed.go is `package baseline`, so
// it cannot call the 5 check functions in `package main` directly.
// The fix is dependency injection: the caller (main.go) computes the
// actuals and passes them in as a baseline.Actuals struct. Seed()
// handles only the marshalling + write. This keeps the baseline
// package pure (no knowledge of the 5 check functions) and
// testable in isolation (Seed can be unit-tested with a fixed
// Actuals struct without spinning up the full archcheck binary).
//
// Lifecycle: per architecture/current.yaml Wave 19 PR-A, the seed
// path is operator-only and is intended to run once per minor cycle
// at PR-A bootstrapping. The `--ratchet --future-ratchet` ratchet
// gate reads (not writes) the baseline; promotion to required-gate
// follows the checklist documented in main.go's runPhase0Checks
// block comment.
package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Actuals is the input to Seed — the current state of the 5 Phase 0
// rules as computed by the check functions in main.go. The struct is
// intentionally flat (no nesting, no Meta field) because Actuals is
// a pure data carrier; the Meta timestamp is added by Seed at write
// time.
//
// Field naming mirrors the Baseline struct's JSON tags (minus the
// `_meta` block) so a future refactor can lift Actuals into Baseline
// with a single field-by-field copy.
type Actuals struct {
	InterfaceBraces     []string
	Setters             []string
	TypeAliasesCrossPkg []string
	FakeRoutes          []string
	HandlersToDB        []string
}

// Seed computes a fresh Baseline from the given Actuals, marshals
// it to JSON, and writes it to File. The returned Baseline mirrors
// the file content so callers can use it for stats accounting
// without re-reading the file.
//
// The Meta block is populated at write time: GeneratedAtRFC3339 is
// stamped to the current UTC time, and Note carries the operator
// narrative (the operator-only path banner, plus the promote-to-
// required follow-up pointer).
func Seed(actuals Actuals) (Baseline, error) {
	b := Baseline{
		InterfaceBraces:     actuals.InterfaceBraces,
		Setters:             actuals.Setters,
		TypeAliasesCrossPkg: actuals.TypeAliasesCrossPkg,
		FakeRoutes:          actuals.FakeRoutes,
		HandlersToDB:        actuals.HandlersToDB,
	}
	b.Meta.GeneratedAtRFC3339 = time.Now().UTC().Format(time.RFC3339)
	b.Meta.Note = "Phase 0 (PR-A) baseline seeded by the operator via the `--seed-baseline` flag (operator-only; main() intercepts before the normal Mode dispatch). Promote-to-required follow-up PR will tighten this baseline to zero (or fold the 5 rules into runRatchetChecks() once the minor cycle ends)."

	marshalled, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return b, fmt.Errorf("encode seed baseline: %w", err)
	}
	marshalled = append(marshalled, '\n')
	if err := os.WriteFile(File, marshalled, 0644); err != nil {
		return b, fmt.Errorf("write seed baseline: %w", err)
	}
	return b, nil
}
