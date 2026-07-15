// Package main — archcheck.
//
// CLI dispatch only (this file). Orchestration lives in
// cmd/archcheck/runner.go (Run, CheckSpec, DefaultChecks, ExitOK /
// ExitViolations / ExitLoadOrParse). Rule-family scanners live in
// cmd/archcheck/scan/{packages,roots,documents,constructors}.go.
// The data model + parser live in cmd/archcheck/policy/ +
// report/ (separate Go packages).
//
// Exit codes:
//
//	0 — report printed (--strict off; Phase 0 default).
//	1 — violations present (--strict on; Phase N mode).
//	2 — load / walk / marshal error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		root           = flag.String("root", ".", "project root to scan (default: cwd)")
		polstr         = flag.String("policy", "architecture/policy.yaml", "path to policy YAML")
		strict         = flag.Bool("strict", false, "exit 1 on any violation (Phase N gate)")
		phase          = flag.String("phase", "0", "phase label (printed in the report header)")
		productionOnly = flag.Bool("production-only", false, "silence comment-only warning buckets for production-only audit mode")
	)
	flag.Parse()

	// Debt budget is a fail-closed catalog invariant in strict mode. It runs
	// before the broad architecture report so changing an issue from open to
	// in_progress cannot bypass CI without concrete implementation evidence.
	if *strict {
		if err := validateDebtBudget(*root, *polstr); err != nil {
			fmt.Fprintf(os.Stderr, "archcheck: %v\n", err)
			os.Exit(ExitViolations)
		}
	}

	code, err := Run(context.Background(), *root, *polstr, *phase, *strict, *productionOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: %v\n", err)
		os.Exit(ExitLoadOrParse)
	}
	os.Exit(code)
}
