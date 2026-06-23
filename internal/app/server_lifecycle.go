// Package app — server lifecycle manager (PR 1 completion).
//
// serverLifecycle implements api.LifecycleManager by wrapping the
// deferred startJobRunner closure (from lifecycle.go::startBackgroundJobs)
// and the cleanup function (from shutdown.go::buildCleanup). This completes
// the separation of route modules from lifecycle management: background
// services startup and teardown are owned by the lifecycle, not by the HTTP
// server or a raw defer in main.go.
//
// Problem #1 (fix/lifecycle-readiness-barrier): serverLifecycle.Start now
// actually USES the ctx argument. A real readiness barrier (pkg/concurrent
// Group with first-error-wins semantics) runs the optional capability
// probes (db ping / Qdrant /readyz / Drive About.Get) before any
// deferred-start closure fires. If a probe returns an error, Start
// returns it WITHOUT firing the closures (fail-closed). Cleanup stays
// safe to invoke after a Start failure (idempotent), so the HTTP server
// retains the contract that Stop is always callable.
//
// NOTE: the broader scope of problem #1 — moving the 15+ concurrent.SafeGo
// calls inside lifecycle.go::startBackgroundJobs from "fire at scope exit"
// to "fire behind serverLifecycle.Start" — is split into followup commits.
// This commit establishes the barrier + ctx discipline at the lifecycle
// boundary; the SafeGo migration is mechanical once the barrier exists.
package app

import (
	"context"
	"fmt"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Compile-time assertion: serverLifecycle satisfies module.LifecycleManager.
var _ module.LifecycleManager = (*serverLifecycle)(nil)

// serverLifecycle wraps the startJobRunner closure (deferred from WireServices
// until after registry freeze), the driveStart + outboxStart + processStart
// closures (extracted from BuildDriveBundle/BuildOutboxBundle/BuildProcessBundle
// per PR9-A/B/C), and the cleanup function (LIFO teardown stack: coreClean →
// artlist Close → logDB Close → middleware StopLogger).
//
// Optional capability probes (dbProbe / vectorProbe / driveProbe) feed the
// readiness barrier — when nil the corresponding probe is skipped, so
// deployments that opt out of a capability (no Drive creds, vector search
// disabled) still pass the barrier.
type serverLifecycle struct {
	startJobRunner func()
	driveStart     func() // PR9-A: deferred Drive side-effect initialisation
	outboxStart    func() // PR9-B: deferred outbox events pool initialisation
	processStart   func() // PR9-C: deferred Qdrant collection setup
	cleanup        func()

	// Capability probes for the readiness barrier (commit fix/lifecycle-readiness).
	// Each returns nil on success. nil probes are skipped.
	dbProbe     func(ctx context.Context) error
	vectorProbe func(ctx context.Context) error
	driveProbe  func(ctx context.Context) error
}

// probeTimeout caps each per-probe wall-clock so a slow dependency cannot
// stall the readiness barrier. The barrier is first-error-wins; a timed-out
// probe returns ctx.DeadlineExceeded just like a hard failure. 5s comfortably
// covers healthy Drive/Qdrant on a local LAN; operators tuning for slow
// clouds can override via env-driven probe timeouts in a followup commit.
const probeTimeout = 5 * time.Second

// Start triggers the deferred initialisation closures in dependency order:
// Drive + Qdrant must be ready BEFORE the outbox pool and job runner can
// safely claim and process jobs that depend on them.
//
// Lifecycle (commit fix/lifecycle-readiness) — replaces the legacy
// "fire-and-forget" version that ignored ctx:
//
//  1. ctx.Err() — fail-closed if the parent context is already done
//     (covers the listener-failure scenario: server.Start's signal
//     NotifyContext for SIGINT/SIGTERM cancels the lifecycle ctx, so a
//     subsequent Start is a no-op rather than a goroutine leak).
//  2. Readiness barrier via pkg/concurrent.WithContext — runs the
//     configured probes (db + vector + drive) in parallel under a
//     derived context. Each probe internally derives a probeTimeout
//     to avoid stalling the barrier on slow dependencies. First error
//     wins; remaining probes are cancelled via the Group's internal
//     cancel. If any probe fails, Start returns the error WITHOUT
//     firing the deferred-start closures (fail-closed).
//  3. Drive + Qdrant + Outbox + JobRunner — fire only after barrier
//     succeeds; each closure is wrapped in SafeCall (panic-recovery
//     + name-tagged error) so a synchronous panic inside any closure
//     surfaces to the caller instead of crashing the server.
//
// PR7 fix (June 2026): reordered to start Drive+Qdrant first, then outbox,
// then job runner last. Previously the job runner started before Qdrant
// EnsureCollection, risking jobs that depend on vector search.
func (l *serverLifecycle) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("lifecycle start: context already done: %w", err)
	}

	// Readiness barrier: pkg/concurrent.Group runs the probes in
	// parallel under a derived context. First error wins.
	g, gctx := concurrent.WithContext(ctx)
	if l.dbProbe != nil {
		g.Go("lifecycle-db-ping", func() error {
			pCtx, cancel := context.WithTimeout(gctx, probeTimeout)
			defer cancel()
			return l.dbProbe(pCtx)
		})
	}
	if l.vectorProbe != nil {
		g.Go("lifecycle-vector-ping", func() error {
			pCtx, cancel := context.WithTimeout(gctx, probeTimeout)
			defer cancel()
			return l.vectorProbe(pCtx)
		})
	}
	if l.driveProbe != nil {
		g.Go("lifecycle-drive-ping", func() error {
			pCtx, cancel := context.WithTimeout(gctx, probeTimeout)
			defer cancel()
			return l.driveProbe(pCtx)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("lifecycle readiness barrier failed: %w", err)
	}

	// Barrier passed — fire the deferred-start closures in dependency
	// order. Driving each one through SafeCall preserves the PR9-A/B/C
	// semantic (each Build*Bundle still constructs only; only Start
	// actually fires the side-effect). SafeCall attaches panic recovery
	// so a misbehaving closure returns an error rather than crashing
	// the server.
	if err := SafeCall("driveStart", l.driveStart); err != nil {
		return err
	}
	if err := SafeCall("processStart", l.processStart); err != nil {
		return err
	}
	if err := SafeCall("outboxStart", l.outboxStart); err != nil {
		return err
	}
	if err := SafeCall("startJobRunner", l.startJobRunner); err != nil {
		return err
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

// Stop runs the cleanup stack: cancels the parent context (signals
// goroutines), stops channel monitor + drive sync scheduler, drains
// the outbox events pool, and closes the main database.
//
// Idempotent: calling Stop after a failed Start (or after a previous
// Stop) is safe — the innerCancel cleanup table is nil-checked, and
// the LIFO stack is walked over a defensive nil guard.
func (l *serverLifecycle) Stop(ctx context.Context) error {
	_ = ctx
	if l.cleanup != nil {
		l.cleanup()
	}
	return nil
}

// NewServerLifecycleWithProbes is the canonical constructor that wires
// the readiness-barrier probes. Probes may be nil (capability opted out
// at composition time). Nil-fn closures are also skipped (e.g. a role-
// minimal deployment may not need the job runner). Returns nil if every
// argument is nil so callers can default to a no-op lifecycle.
func NewServerLifecycleWithProbes(
	startJobRunner func(),
	driveStart func(),
	outboxStart func(),
	processStart func(),
	cleanup func(),
	dbProbe func(ctx context.Context) error,
	vectorProbe func(ctx context.Context) error,
	driveProbe func(ctx context.Context) error,
) module.LifecycleManager {
	if startJobRunner == nil && driveStart == nil && outboxStart == nil && processStart == nil && cleanup == nil &&
		dbProbe == nil && vectorProbe == nil && driveProbe == nil {
		return nil
	}
	return &serverLifecycle{
		startJobRunner: startJobRunner,
		driveStart:     driveStart,
		outboxStart:    outboxStart,
		processStart:   processStart,
		cleanup:        cleanup,
		dbProbe:        dbProbe,
		vectorProbe:    vectorProbe,
		driveProbe:     driveProbe,
	}
}
