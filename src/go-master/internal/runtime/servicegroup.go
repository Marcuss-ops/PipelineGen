// Package runtime provides lifecycle management for background services.
package runtime

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// BackgroundService represents any long-running component that can be
// started and stopped under a shared lifecycle.
type BackgroundService interface {
	Start(ctx context.Context) error
	Stop() error
	Name() string
}

// ServiceGroup manages a collection of BackgroundServices with unified
// start/stop semantics. Services are started in order and stopped in
// reverse order. A shared context is derived and cancelled on Stop(),
// which also calls Stop() on every service for explicit cleanup.
type ServiceGroup struct {
	services []BackgroundService
	mu       sync.Mutex
	cancel   context.CancelFunc
	log      *zap.Logger
}

// NewServiceGroup creates a new ServiceGroup.
func NewServiceGroup(log *zap.Logger) *ServiceGroup {
	return &ServiceGroup{log: log}
}

// Add registers a service. Must be called before Start().
func (g *ServiceGroup) Add(s BackgroundService) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.services = append(g.services, s)
}

// Services returns a copy of the registered services list.
func (g *ServiceGroup) Services() []BackgroundService {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]BackgroundService, len(g.services))
	copy(out, g.services)
	return out
}

// Start launches all services with a shared context derived from ctx.
// If any service fails to start, previously started services are stopped
// in reverse order and the derived context is cancelled.
//
// Note: Start() must be non-blocking for each service. Services that run
// long-lived goroutines should launch them internally and return immediately.
func (g *ServiceGroup) Start(ctx context.Context) error {
	g.mu.Lock()
	services := make([]BackgroundService, len(g.services))
	copy(services, g.services)
	g.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)

	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	for i, svc := range services {
		g.log.Info("starting service", zap.String("service", svc.Name()))
		if err := svc.Start(ctx); err != nil {
			g.log.Error("service failed to start", zap.String("service", svc.Name()), zap.Error(err))
			cancel()

			// Stop already-started services in reverse order
			for j := i - 1; j >= 0; j-- {
				g.log.Info("stopping service (rollback)", zap.String("service", services[j].Name()))
				_ = services[j].Stop()
			}
			return fmt.Errorf("failed to start %s: %w", svc.Name(), err)
		}
		g.log.Info("service started", zap.String("service", svc.Name()))
	}
	return nil
}

// Stop shuts down all services in reverse initialization order.
// It first cancels the shared context (for goroutine-based services)
// and then calls Stop() on every service for explicit resource cleanup.
func (g *ServiceGroup) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.cancel != nil {
		g.cancel()
	}

	var firstErr error
	for i := len(g.services) - 1; i >= 0; i-- {
		svc := g.services[i]
		g.log.Info("stopping service", zap.String("service", svc.Name()))
		if err := svc.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed stopping %s: %w", svc.Name(), err)
		}
	}
	return firstErr
}

// ServiceAdapter wraps heterogeneous start/stop functions into a
// BackgroundService. This is useful for services that don't natively
// implement the interface (e.g., different method signatures, or
// services that only respond to context cancellation).
type ServiceAdapter struct {
	name  string
	start func(ctx context.Context) error
	stop  func() error
}

// NewServiceAdapter creates a BackgroundService from start/stop functions.
// The stop function may be nil if the service only relies on context
// cancellation for shutdown.
func NewServiceAdapter(name string, start func(ctx context.Context) error, stop func() error) *ServiceAdapter {
	return &ServiceAdapter{name: name, start: start, stop: stop}
}

func (s *ServiceAdapter) Start(ctx context.Context) error { return s.start(ctx) }
func (s *ServiceAdapter) Stop() error {
	if s.stop != nil {
		return s.stop()
	}
	return nil
}
func (s *ServiceAdapter) Name() string { return s.name }
