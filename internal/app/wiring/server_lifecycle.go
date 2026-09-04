package wiring

import (
	"context"

	lifecyclewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/lifecycle"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

// serverLifecycle is the root compatibility facade. Runtime semantics live in
// wiring/lifecycle; this wrapper preserves the long-standing package-private
// construction shape used by root integration tests and legacy composition code.
type serverLifecycle struct {
	probes      []*probeEntry
	startupPlan []StartupStep
	cleanup     func()
	log         *zap.Logger
	inner       *lifecyclewiring.Runtime
}

type probeEntry struct {
	name string
	fn   func(ctx context.Context) error
}

var _ module.LifecycleManager = (*serverLifecycle)(nil)

func (l *serverLifecycle) runtime() *lifecyclewiring.Runtime {
	if l.inner != nil {
		return l.inner
	}
	plan := make([]lifecyclewiring.StartupStep, 0, len(l.startupPlan))
	for _, step := range l.startupPlan {
		plan = append(plan, lifecyclewiring.StartupStep{
			Name:     step.Name,
			Required: step.Required,
			Start:    step.Start,
			Stop:     step.Stop,
		})
	}
	l.inner = lifecyclewiring.NewRuntime(plan, l.cleanup, l.log)
	for _, probe := range l.probes {
		if probe != nil {
			l.inner.AddProbe(probe.name, probe.fn)
		}
	}
	return l.inner
}

func (l *serverLifecycle) Start(ctx context.Context) error {
	return l.runtime().Start(ctx)
}

func (l *serverLifecycle) Stop(ctx context.Context) error {
	return l.runtime().Stop(ctx)
}

func (l *serverLifecycle) AddProbe(name string, probe func(ctx context.Context) error) {
	if l == nil || probe == nil {
		return
	}
	if l.inner != nil {
		l.inner.AddProbe(name, probe)
		return
	}
	l.probes = append(l.probes, &probeEntry{name: name, fn: probe})
}

// SafeCall remains a root facade for callers that have not migrated to the
// lifecycle leaf package yet.
func SafeCall(name string, fn func()) error {
	return lifecyclewiring.SafeCall(name, fn)
}

// NewServerLifecycleWithProbes preserves the root constructor while delegating
// runtime ownership to wiring/lifecycle.
func NewServerLifecycleWithProbes(
	startupPlan []StartupStep,
	cleanup func(),
	dbProbe func(ctx context.Context) error,
	vectorProbe func(ctx context.Context) error,
	driveProbe func(ctx context.Context) error,
	log *zap.Logger,
) module.LifecycleManager {
	if len(startupPlan) == 0 && cleanup == nil && dbProbe == nil && vectorProbe == nil && driveProbe == nil {
		return nil
	}
	sl := &serverLifecycle{startupPlan: startupPlan, cleanup: cleanup, log: log}
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
