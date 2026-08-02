// Package main — worker binary entry point (P1-3 slim, June 2026).
//
// This file is the *only* file in cmd/worker/. The full binary's
// logic lives in internal/app/workerruntime/ split across 7
// files by capability stable (run.go / config.go / identity.go /
// capabilities.go / preflight.go / heartbeat.go / registration.go).
//
// Pattern: matches the cmd/archcheck/main.go slim-entry convention
// (cmd/archcheck/ARCHITECTURE.md — orchestrator lives elsewhere,
// main does flag parse + parse args + delegate to Run).
//
// Exit codes:
//
//	0 — worker shutdown clean (signal received, runner returned)
//	1 — Run() returned non-nil error (started but errored)
//	    OR worker.NewRunner.Run(runCtx) failed.
//	Log.Fatal path inside workerruntime.Run also exits 1.
//
// Doctor subcommand (`worker doctor [flags]` / `worker --mode=doctor`):
// exits with the doctor verdict code (0 READY / 1 NOT_READY / 2 usage /
// 3 internal) — see internal/application/workerdoctor. The dispatch MUST
// happen before flag.Parse because the doctor owns its own flag set
// (cmd/worker/doctor_main.go).
//
// Operational Readiness note (AGENTS.md / docs/operations): the
// port for the master is read from $VELOX_MASTER_URL or
// VELOX_BROKER_URL, with canonical default http://127.0.0.1:8000.
// The cfgPath flag is the canonical override surface (config.yaml
// is the default per the Operational Readiness PR).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/workerruntime"
)

func main() {
	// Doctor subcommand first: it owns its own flag set and must not
	// reach the worker boot path. A bare `worker doctor --json` therefore
	// never starts the worker runtime (this was previously dead code —
	// the bootstrap smoke now fails closed if the verdict is missing).
	// Sync the logger before os.Exit (os.Exit skips deferred cleanup).
	if isDoctorSubcommand(os.Args[1:]) {
		logger := newDoctorLogger()
		code := runDoctor(os.Args[2:], logger)
		_ = logger.Sync()
		os.Exit(code)
	}

	var (
		cfgPth = flag.String("config", "config.yaml", "Path to configuration file")
	)
	flag.Parse()

	// AGENTS.md §7 post-write save ctx pattern: signal.NotifyContext
	// is the canonical Go entry point for a top-level deferred
	// cancel that survives process shutdown. workerruntime.Run
	// derives a child ctx (runCtx) from this for the heartbeat
	// goroutine + runner loop.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := workerruntime.Run(ctx, *cfgPth); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

// isDoctorSubcommand reports whether args select the doctor subcommand.
// Both the positional form used by scripts/worker-bootstrap-smoke.sh
// (`worker doctor --json`) and the documented --mode=doctor form are
// accepted; anything else is a normal worker boot invocation.
func isDoctorSubcommand(args []string) bool {
	return len(args) > 0 && (args[0] == "doctor" || args[0] == "--mode=doctor")
}

// newDoctorLogger builds the zap logger used by the doctor subcommand.
// zap.NewProduction can only fail on invalid encoder config (it cannot
// for the defaults), so a nil-safe fallback keeps the CLI robust.
func newDoctorLogger() *zap.Logger {
	logger, err := zap.NewProduction()
	if err != nil {
		return zap.NewNop()
	}
	return logger
}
