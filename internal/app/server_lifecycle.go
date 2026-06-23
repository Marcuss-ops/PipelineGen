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
// until after registry freeze), the driveStart + outboxStart closures
// (extracted from BuildDriveBundle/BuildOutboxBundle per PR9-A/B), and the
// cleanup function (LIFO teardown stack: coreClean → artlist Close → logDB
// Close → middleware StopLogger).
type serverLifecycle struct {
	startJobRunner func()
	driveStart     func() // PR9-A: deferred Drive side-effect initialisation
	outboxStart    func() // PR9-B: deferred outbox events pool initialisation
	cleanup        func()
}

// Start triggers the deferred job runner start, Drive background
// initialisation, and outbox events pool start. Background goroutines
// (channel monitor, sweepers, etc.) are already running from
// startBackgroundJobs inside initCompositionMinimal; this is only the
// final piece that must happen after WireRegistry has registered all
// handlers and frozen the dispatcher.
func (l *serverLifecycle) Start(ctx context.Context) error {
	_ = ctx
	if l.startJobRunner != nil {
		l.startJobRunner()
	}
	if l.driveStart != nil {
		l.driveStart()
	}
	if l.outboxStart != nil {
		l.outboxStart()
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
// runner start closure, the deferred drive-background + outbox start
// closures (PR9-A/B), and the cleanup function. Any parameter may be nil
// (e.g. test mode where no background jobs are started).
func NewServerLifecycle(startJobRunner func(), driveStart func(), outboxStart func(), cleanup func()) module.LifecycleManager {
	if startJobRunner == nil && driveStart == nil && outboxStart == nil && cleanup == nil {
		return nil
	}
	return &serverLifecycle{
		startJobRunner: startJobRunner,
		driveStart:     driveStart,
		outboxStart:    outboxStart,
		cleanup:        cleanup,
	}
}
