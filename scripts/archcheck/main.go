// Package main — archcheck (legacy burndown ratchet).
//
// scripts/archcheck is the **legacy burndown** half of PipelineGen's
// architectural gating. It is a regex-heavy (`rg`) transitional
// ratchet — its job is to enforce that the codebase shrinks the
// legacy surface (database/sql in api/application/domain, interface{}
// growth, dependency setters, type aliases, fake 501 routes,
// handlers reaching for *sql.DB, Python legacy writers, etc.) under a
// committed monotone-decreasing bl.
//
// Companion binary: **cmd/archcheck** is the **target-tree** half —
// for its role description, see cmd/archcheck/main.go's package doc.
// Keep the two binaries separate because their lifecycles are
// orthogonal: this binary is transient (rules expire when they reach
// verified_zero=true); cmd/archcheck is permanent (it enforces the
// target tree indefinitely). Cross-references between the two
// binaries live in AGENTS.md §"Architecture Map", in
// architecture/current.yaml (Wave 16 + Wave 19 docs), and in
// architecture/current.yaml::archcheck_entry_point_decision
// (Wave 19 PR2 dead-plus-setup, June 2026).
//
// Exit codes:
//
//	0 — report printed; no violations (default mode).
//	1 — violations present (focused or ratchet mode).
//	2 — load/walk/marshal/seek error.
//
// File layout:
//
//	main.go          — CLI entry point (main + runSeedBaseline)
//	command.go       — CLI flag parsing (ParseCommandLine, Config, Mode)
//	runner.go        — check orchestration (runFocusedChecks, runRatchetChecks, runPhase0Checks)
//	report.go        — JSON output (Report struct, EncodeReport)
//	checks.go        — standard ratchet check functions
//	phase0_checks.go — Phase 0 baseline-on-baseline rules
//	symbol_refs.go   — architecture-symbol CI gate
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

func main() {
	cfg, err := ParseCommandLine(os.Args[1:], os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}

	if cfg.Mode == ModeSeedBaseline {
		runSeedBaseline()
		return
	}

	if cfg.Mode == ModeSymbolRefs {
		os.Exit(runSymbolRefsChecks())
	}

	if cfg.Mode == ModeMigrate {
		if err := migrate(defaultLegacyPath, defaultTargetDir); err != nil {
			fmt.Fprintf(os.Stderr, "archcheck: --migrate-deprecations: %v\n", err)
			os.Exit(2)
		}
		return
	}

	var report Report
	switch cfg.Mode {
	case ModeRatchet:
		report = runRatchetChecks()
	default:
		report = runFocusedChecks()
	}

	if cfg.FutureRatchet {
		phase0Stats, phase0Violations := runPhase0Checks()
		for k, v := range phase0Stats {
			report.Checks[k] = v
		}
		report.Violations = append(report.Violations, phase0Violations...)
		report.Passed = report.Passed && len(phase0Violations) == 0
		report.Mode = report.Mode + "-future-ratchet"
	}

	if err := EncodeReport(&report); err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: %v\n", err)
		os.Exit(2)
	}

	if !report.Passed {
		os.Exit(1)
	}
}

// runSeedBaseline is the operator-only path: write a fresh phase 0
// baseline from the current rg actuals and exit 0. Independent of
// the ratchet / future-ratchet flags because it is a snapshot op,
// not a check op. If rg is missing in the environment the operator
// will see check-rule violations in the seeded baseline — that's a
// signal to install rg locally and re-run rather than commit a
// poisoned bl.
func runSeedBaseline() {
	if _, err := os.Stat(bl.File); err == nil {
		fmt.Fprintf(os.Stderr, "archcheck: seed-baseline refused: %s already exists; remove it first or use --ratchet --future-ratchet to compare\n", bl.File)
		os.Exit(2)
	}
	// Compute the 5 Phase 0 actuals locally (in package main) and pass
	// them to bl.Seed() as a struct. The baseline package cannot
	// call these check functions directly (different package, no
	// visibility into the 5 check functions), so dependency injection
	// is the cleanest path. Pass `nil` for the baseline slice: the
	// seed path computes the actual set from scratch, the diff
	// regressions/stale are discarded.
	ibActual, _, _ := checkInterfaceBraceGrowth(nil)
	sdActual, _, _ := checkSetterDetector(nil)
	taActual, _, _ := checkTypeAliasCrossPkg(nil)
	frActual, _, _ := checkFakeRoute(nil)
	hdActual, _, _ := checkHandlerToDB(nil)
	actuals := bl.Actuals{
		InterfaceBraces:     ibActual,
		Setters:             sdActual,
		TypeAliasesCrossPkg: taActual,
		FakeRoutes:          frActual,
		HandlersToDB:        hdActual,
	}
	if _, err := bl.Seed(actuals); err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: seed-baseline failed: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "{\"seeded\":%q,\"path\":%q}\n", time.Now().UTC().Format(time.RFC3339), bl.File)
	os.Exit(0)
}
