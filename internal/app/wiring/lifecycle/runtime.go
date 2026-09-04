package lifecycle

import (
	"context"
	"fmt"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// StartupStep is one ordered unit in the server runtime startup/shutdown plan.
type StartupStep struct {
	Name     string
	Required bool
	Start    func(ctx context.Context) error
	Stop     func(ctx context.Context) error
}

// Runtime owns readiness probes, ordered startup and reverse-order teardown.
// It is deliberately independent from the composition root: callers pass the
// already-built plan and probes through typed functions.
type Runtime struct {
	probes       []*probeEntry
	startupPlan  []StartupStep
	startedSteps []int
	cleanup      func()
	log          *zap.Logger
}

var _ module.LifecycleManager = (*Runtime)(nil)

type probeEntry struct {
	name string
	fn   func(ctx context.Context) error
}

const probeTimeout = 5 * time.Second

// NewRuntime creates the lifecycle runtime without starting any goroutine.
func NewRuntime(startupPlan []StartupStep, cleanup func(), log *zap.Logger) *Runtime {
	return &Runtime{startupPlan: startupPlan, cleanup: cleanup, log: log}
}

// Start runs the readiness barrier and then the startup plan in declaration order.
func (l *Runtime) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("lifecycle start: context already done: %w", err)
	}
	l.startedSteps = l.startedSteps[:0]

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

	for i, step := range l.startupPlan {
		l.startedSteps = append(l.startedSteps, i)
		if step.Start == nil {
			continue
		}
		if err := step.Start(ctx); err != nil {
			if step.Required {
				return fmt.Errorf("required step %q failed: %w", step.Name, err)
			}
			if l.log != nil {
				l.log.Warn("optional startup step failed", zap.String("step", step.Name), zap.Error(err))
			}
		}
	}
	return nil
}

// Stop tears down started steps in reverse order and then runs the LIFO cleanup.
func (l *Runtime) Stop(ctx context.Context) error {
	for i := len(l.startedSteps) - 1; i >= 0; i-- {
		step := l.startupPlan[l.startedSteps[i]]
		if step.Stop != nil {
			_ = step.Stop(ctx)
		}
	}
	l.startedSteps = nil
	if l.cleanup != nil {
		cleanup := l.cleanup
		l.cleanup = nil
		cleanup()
	}
	return nil
}

// AddProbe registers a readiness probe before the runtime barrier executes.
func (l *Runtime) AddProbe(name string, probe func(ctx context.Context) error) {
	if l == nil || probe == nil {
		return
	}
	l.probes = append(l.probes, &probeEntry{name: name, fn: probe})
}

// SafeCall runs a synchronous lifecycle closure and turns panics into errors.
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
