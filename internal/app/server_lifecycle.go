// Package app — server lifecycle manager (PR 1 completion).
//
// serverLifecycle implements api.LifecycleManager by wrapping the
// deferred startJobRunner closure (from lifecycle.go::startBackgroundJobs)
// and the cleanup function (from shutdown.go::buildCleanup). This completes
// the separation of route modules from lifecycle management: background
// services startup and teardown are owned by the lifecycle, not by the HTTP
// server or a raw defer in main.go.
package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Compile-time assertion: serverLifecycle satisfies module.LifecycleManager.
var _ module.LifecycleManager = (*serverLifecycle)(nil)

// serverLifecycle wraps the startJobRunner closure (deferred from WireServices
// until after registry freeze), the driveStart + outboxStart + processStart
// closures (extracted from BuildDriveBundle/BuildOutboxBundle/BuildProcessBundle
// per PR9-A/B/C), and the cleanup function (LIFO teardown stack: coreClean →
// artlist Close → logDB Close → middleware StopLogger).
type serverLifecycle struct {
	startJobRunner func()
	driveStart     func() // PR9-A: deferred Drive side-effect initialisation
	outboxStart    func() // PR9-B: deferred outbox events pool initialisation
	processStart   func() // PR9-C: deferred Qdrant collection setup
	cleanup        func()
}

// Start triggers the deferred initialisation closures in dependency order:
// Drive + Qdrant must be ready BEFORE the outbox pool and job runner can
// safely claim and process jobs that depend on them.
//
// Background goroutines (channel monitor, sweepers, etc.) are already
// running from startBackgroundJobs inside initCompositionMinimal; this is
// only the final piece that must happen after WireRegistry has registered
// all handlers and frozen the dispatcher.
//
// PR7 fix (June 2026): reordered to start Drive+Qdrant first, then outbox,
// then job runner last. Previously the job runner started before Qdrant
// EnsureCollection, risking jobs that depend on vector search.
func (l *serverLifecycle) Start(ctx context.Context) error {
	_ = ctx
	if l.driveStart != nil {
		l.driveStart()
	}
	if l.processStart != nil {
		l.processStart() // best-effort: Qdrant EnsureCollection may fail silently
	}
	if l.outboxStart != nil {
		l.outboxStart()
	}
	if l.startJobRunner != nil {
		l.startJobRunner()
	}
	return nil
}

// Stop runs the cleanup stack: cancels the parent context (signals
// goroutines), stops channel monitor + drive sync scheduler, drains
// the outbox events pool, and closes the main database.
func (l *serverLifecycle) Stop(ctx context.Context) error {
	_ = ctx
	if l.cleanup != nil {
		l.cleanup()
	}
	return nil
}

// NewServerLifecycle creates a LifecycleManager from the deferred job
// runner start closure, the deferred drive/outbox/process start closures
// (PR9-A/B/C), and the cleanup function. Any parameter may be nil (e.g.
// test mode where no background jobs are started).
func NewServerLifecycle(startJobRunner func(), driveStart func(), outboxStart func(), processStart func(), cleanup func()) module.LifecycleManager {
	if startJobRunner == nil && driveStart == nil && outboxStart == nil && processStart == nil && cleanup == nil {
		return nil
	}
	return &serverLifecycle{
		startJobRunner: startJobRunner,
		driveStart:     driveStart,
		outboxStart:    outboxStart,
		processStart:   processStart,
		cleanup:        cleanup,
	}
}
