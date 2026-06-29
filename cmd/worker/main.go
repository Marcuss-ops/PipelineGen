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

	"github.com/Marcuss-ops/PipelineGen/internal/app/workerruntime"
)

func main() {
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
