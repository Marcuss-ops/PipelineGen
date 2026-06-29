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
//
// FASE 1.C (June 2026) is the structural refactor that split the
// former 996-line god-file into policy/ + report/ + scan/ +
// runner/ + main (this file). See cmd/archcheck/ARCHITECTURE.md
// for the post-FASE-1.C target layout.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		root   = flag.String("root", ".", "project root to scan (default: cwd)")
		polstr = flag.String("policy", "architecture/policy.yaml", "path to policy YAML")
		strict = flag.Bool("strict", false, "exit 1 on any violation (Phase N gate)")
		phase  = flag.String("phase", "0", "phase label (printed in the report header)")
	)
	flag.Parse()

	code, err := Run(context.Background(), *root, *polstr, *phase, *strict)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: %v\n", err)
		os.Exit(2)
	}
	os.Exit(code)
}
