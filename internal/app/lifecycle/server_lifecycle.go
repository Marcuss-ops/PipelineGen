// Package app — server lifecycle manager (PR 1 completion, lifecycle-runtime-ownership).
//
// serverLifecycle implements api.LifecycleManager and owns ALL runtime
// service startups. The startup plan is ordered:
//
//  1. Readiness barrier (probes: db / vector / drive — parallel, first-error-wins)
//  2. Required services (Drive folder validation, Qdrant collection, outbox pool)
//  3. Optional services (scanners, monitors, sweepers, refreshers, health checks)
//  4. Job consumers (job runner — always last)
//
// Stop runs the plan steps in reverse order, then the LIFO cleanup stack.
// No background goroutines are launched during composition — all startups
// are deferred to serverLifecycle.Start.
package lifecycle

import (
	"context"
	"fmt"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// Compile-time assertion: serverLifecycle satisfies module.LifecycleManager.
var _ module.LifecycleManager = (*serverLifecycle)(nil)

// serverLifecycle owns the full runtime lifecycle: readiness probes,
// the ordered startup plan (built by startBackgroundJobs during composition
// but NOT executed until Start), and the LIFO cleanup stack.
//
// The startupPlan encodes the dependency order — all background workers,
// scanners, monitors, sweepers, and the job runner are listed as
// StartupStep entries. Prerequisite services (Drive, Qdrant, outbox)
// are also included as required steps.
//
// QDRANT-005 (June 2026) closure: probes were promoted from three
// fixed fields (db/vector/drive) to a unified slice. NewServerLifecycle-
// WithProbes still wires the canonical trio via AddProbe(name=...) — the
// constructor's signature is unchanged so cmd/server's existing args
// keep working. cmd/server/main.go additionally plugs the Qdrant probe
// via AddProbe(name="qdrant", fn=...). Previously this silently type-
// asserted to a non-existent interface and never ran in prod.
type serverLifecycle struct {
	// probes is the unified readiness-barrier probe list, consulted in
	// declaration order. Probes are registered via AddProbe (or
	// automatically by NewServerLifecycleWithProbes for the canonical
	// DB/Vector/Drive trio).
	probes []*probeEntry

	// startupPlan is the ordered list of services to start.
	// Required steps that fail abort the entire Start sequence.
	// Optional failures are logged but do not block remaining steps.
	// The job runner MUST be the last entry in the plan.
	startupPlan []StartupStep

	// startedSteps tracks the steps whose Start method has been invoked.
	// Stop uses this to tear down only the services that actually entered
	// the startup sequence.
	startedSteps []int

	// cleanup is the LIFO teardown stack (coreClean → artlist Close →
	// logDB Close → middleware StopLogger). Invoked during Stop.
	cleanup func()

	// log is used for reporting optional step failures.
	log *zap.Logger
}

// probeEntry pairs a human-readable probe name with the probe fn.
// name appears in barrier-failure logs so operators know which
// dependency failed the readiness check.
type probeEntry struct {
	name string
	fn   func(ctx context.Context) error
}

// probeTimeout caps each per-probe wall-clock so a slow dependency cannot
// stall the readiness barrier. The barrier is first-error-wins; a timed-out
// probe returns ctx.DeadlineExceeded just like a hard failure. 5s comfortably
// covers healthy Drive/Qdrant on a local LAN; operators tuning for slow
// clouds can override via env-driven probe timeouts in a followup commit.
const probeTimeout = 5 * time.Second

// Start executes the full startup sequence:
//
//  1. ctx.Err() — fail-closed if the parent context is already done.
//  2. Readiness barrier — runs configured probes in parallel; first error
//     aborts the sequence without starting any service.
//  3. Startup plan — each step is executed in declaration order:
//     a. Required steps: error aborts the sequence and returns immediately
//     (no further steps execute).
//     b. Optional steps: error is logged and exposed (via StructuredError)
//     but does NOT block remaining steps.
//     c. Job runner is the last required step — it freezes the dispatcher
//     and begins claiming jobs.
//
// Context contract: Start uses the server's runtime context (derived from
// signal.NotifyContext in api/server.go). All background goroutines
// launched here MUST use this context so they shut down when the server
// receives SIGINT/SIGTERM.
func (l *serverLifecycle) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("lifecycle start: context already done: %w", err)
	}
	l.startedSteps = l.startedSteps[:0]

	// Readiness barrier: pkg/concurrent.Group runs the probes in
	// parallel under a derived context. First error wins.
	// QDRANT-005 (June 2026): unified AddProbe-driven probe list;
	// the three fixed fields were collapsed into this slice.
	g, gctx := concurrent.WithContext(ctx)
	for _, p := range l.probes {
		if p == nil || p.fn == nil {
			continue
		}
		probeFn := p.fn
		probeName := "lifecycle-" + p.name + "-ping"
		g.Go(probeName, func() error {
			pCtx, cancel := context.WithTimeout(gctx, probeTimeout)
			defer cancel()
			return probeFn(pCtx)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("lifecycle readiness barrier failed: %w", err)
	}

	// Run the startup plan in declaration order.
	for i, step := range l.startupPlan {
		l.startedSteps = append(l.startedSteps, i)
		if err := step.Start(ctx); err != nil {
			if step.Required {
				return fmt.Errorf("required step %q failed: %w", step.Name, err)
			}
			// Optional failure: log and continue.
			if l.log != nil {
				l.log.Warn("optional startup step failed",
					zap.String("step", step.Name), zap.Error(err))
			}
		}
	}
	return nil
}

// Stop runs the cleanup sequence in LIFO order:
//  1. Startup plan stops in reverse order (each step.Stop is called).
//  2. LIFO cleanup stack (coreClean → artlist Close → logDB Close →
//     middleware StopLogger).
//
// Idempotent: calling Stop after a failed Start (or after a previous
// Stop) is safe. Stops swallow errors to avoid masking the shutdown
// cause — the original signal (SIGINT/SIGTERM) is the canonical exit
// reason.
func (l *serverLifecycle) Stop(ctx context.Context) error {
	_ = ctx
	// Run step stops in reverse order.
	for i := len(l.startedSteps) - 1; i >= 0; i-- {
		step := l.startupPlan[l.startedSteps[i]]
		if step.Stop != nil {
			_ = step.Stop(ctx)
		}
	}
	l.startedSteps = nil
	// Run the LIFO cleanup stack.
	if l.cleanup != nil {
		cleanup := l.cleanup
		l.cleanup = nil
		cleanup()
	}
	return nil
}

// SafeCall runs fn synchronously and converts any panic to a named
// error. nil fn returns nil immediately. The closure name is included
// in the panic message so operators can locate the failing surface
// without grepping the call stack.
//
// Named to match pkg/concurrent.SafeGo — the async sibling — so callers
// reading lifecycle.go can find both without grep. If a future commit
// promotes this to pkg/concurrent (alongside SafeGo), the consumer code
// stays identical.
func SafeCall(name string, fn func()) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("lifecycle closure %q panicked: %v", name, r)
		}
	}()
	fn()
	return nil
}

// AddProbe registers an additional readiness probe that runs during the
// parallel barrier in Start. It is safe to call AFTER construction; the
// runtime callers (cmd/server/main.go: Qdrant probe, future probes for
// Embedding/APIs/etc.) use this to extend the readiness contract without
// touching the canonical constructor.
//
// AddProbe must be called BEFORE Start. Calling AddProbe after Start has
// begun has no effect on the current barrier — the probe list is read
// once at the top of Start.
func (l *serverLifecycle) AddProbe(name string, probe func(ctx context.Context) error) {
	if l == nil || probe == nil {
		return
	}
	l.probes = append(l.probes, &probeEntry{name: name, fn: probe})
}

// NewServerLifecycleWithProbes is the canonical constructor that wires
// the readiness-barrier probes and the startup plan. Probes may be nil
// (capability opted out at composition time). The startup plan may be
// empty (background jobs disabled). Returns nil if every argument is nil
// so callers can default to a no-op lifecycle.
//
// QDRANT-005 (June 2026): the three constructor probes are now
// registered through AddProbe so the constructor's signature stays
// unchanged for callers while the lifecycle exposes a generic extension
// surface for runtime-injected probes (e.g. Qdrant from cmd/server).
func NewServerLifecycleWithProbes(
	startupPlan []StartupStep,
	cleanup func(),
	dbProbe func(ctx context.Context) error,
	vectorProbe func(ctx context.Context) error,
	driveProbe func(ctx context.Context) error,
	log *zap.Logger,
) module.LifecycleManager {
	if len(startupPlan) == 0 && cleanup == nil &&
		dbProbe == nil && vectorProbe == nil && driveProbe == nil {
		return nil
	}
	sl := &serverLifecycle{
		startupPlan: startupPlan,
		cleanup:     cleanup,
		log:         log,
	}
	if dbProbe != nil {
		sl.AddProbe("db", dbProbe)
	}
	if vectorProbe != nil {
		sl.AddProbe("vector", vectorProbe)
	}
	if driveProbe != nil {
		sl.AddProbe("drive", driveProbe)
	}
	return sl
}
