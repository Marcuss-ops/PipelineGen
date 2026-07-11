// Package main — archcheck command-line parsing.
//
// command.go owns the CLI surface of scripts/archcheck. main() parses
// os.Args[1:] via ParseCommandLine and dispatches to the mode
// implementation. Future flag additions (--baseline, --format, ...)
// go here; main() stays under 100 lines.
//
// The Config / Mode split is intentional: Mode is the gate mode
// (focused / ratchet / seed-baseline), FutureRatchet is an orthogonal
// toggle that EXTENDS focused or ratchet with the Phase 0
// baseline-on-baseline rules (grace cycle before promotion to
// required). This matches the original main.go behaviour where
// `--ratchet --future-ratchet` is a valid combination and
// `--seed-baseline --future-ratchet` is meaningless (seed-baseline
// always exits 0).
package main

import (
	"flag"
	"io"
)

// Mode is the archcheck invocation mode.
//
// Mode values are mutually exclusive at the dispatch layer
// (ModeSeedBaseline short-circuits runFocusedChecks / runRatchetChecks
// and writes a baseline instead). ModeFocused and ModeRatchet may be
// EXTENDED by Config.FutureRatchet.
type Mode int

const (
	// ModeFocused runs the "report-only" gate that always exits 0
	// unless the (smaller) focused surface breaks. Used by the
	// default scripts/archcheck invocation. Backed by
	// runFocusedChecks.
	ModeFocused Mode = iota
	// ModeRatchet runs the full ratchet gate (allowlist + baseline).
	// Activated by `--ratchet`. Backed by runRatchetChecks.
	ModeRatchet
	// ModeSeedBaseline is operator-only: writes a fresh phase 0
	// baseline from the current actual state and exits 0. Activated
	// by `--seed-baseline`. Short-circuits any check dispatch.
	ModeSeedBaseline
	// ModeSymbolRefs runs the architecture-symbol CI gate (action
	// P0-5 slice 4/4 of the cleanup plan, June 2026): walks
	// architecture/*.yaml,
	// extracts Go-like path tokens, and prints findings to
	// stderr. Exits 1 when any reference is unresolved. Activated
	// by `--symbol-refs`. Backed by runSymbolRefsChecks (in
	// scripts/archcheck/symbol_refs.go).
	ModeSymbolRefs
)

// String renders Mode for the report.Mode field. Keep these strings
// stable — they appear in CI dashboards, jq pipelines, and the
// report_schema.json golden file.
func (m Mode) String() string {
	switch m {
	case ModeRatchet:
		return "ratchet"
	case ModeSeedBaseline:
		return "seed-baseline"
	case ModeSymbolRefs:
		return "symbol-refs"
	default:
		return "focused"
	}
}

// Config holds the parsed command-line configuration for one
// scripts/archcheck invocation.
type Config struct {
	// Mode is the gate mode (focused / ratchet / seed-baseline).
	Mode Mode
	// FutureRatchet extends focused/ratchet with Phase 0 baseline-on-
	// baseline rules (grace cycle before promotion to required).
	FutureRatchet bool
}

// ParseCommandLine parses args (typically `os.Args[1:]`) and returns
// the resulting Config. The FlagSet's output is routed to stderr
// (the caller passes `os.Stderr` as `errSink`) so main()'s stdout
// stays clean for the JSON report.
//
// On parse error (unknown flag, missing value, ...) the function
// returns the flag library error verbatim and main() should exit 2.
// On `--help` / `-h` the function returns `flag.ErrHelp`; main()
// treats that as exit 0.
func ParseCommandLine(args []string, errSink io.Writer) (*Config, error) {
	fs := flag.NewFlagSet("archcheck", flag.ContinueOnError)
	fs.SetOutput(errSink)
	ratchet := fs.Bool("ratchet", false, "run the ratchet architectural gate (allowlist + baseline)")
	futureRatchet := fs.Bool("future-ratchet", false, "additionally run Phase 0 PR-A baseline-on-baseline rules (grace cycle before promotion to required)")
	seedBaseline := fs.Bool("seed-baseline", false, "explicitly seed scripts/archcheck/phase0_baseline.json from current actual state and exit 0 (operator-only; intended once per minor cycle at PR-A bootstrapping)")
	symbolRefs := fs.Bool("symbol-refs", false, "run the architecture-symbol CI gate: walk architecture/*.yaml and emit a Finding per unresolved Go-path reference (action P0-5 slice 4/4 of the cleanup plan)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		FutureRatchet: *futureRatchet,
	}
	switch {
	case *seedBaseline:
		cfg.Mode = ModeSeedBaseline
	case *symbolRefs:
		cfg.Mode = ModeSymbolRefs
	case *ratchet:
		cfg.Mode = ModeRatchet
	default:
		cfg.Mode = ModeFocused
	}
	return cfg, nil
}
