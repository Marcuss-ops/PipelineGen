package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Module is the common interface that all feature modules must implement.
// This allows for clean registration and lifecycle management.
type Module interface {
	// Name returns the unique name of the module (e.g., "artlist", "clips")
	Name() string

	// Enabled checks if this module should be enabled based on config
	Enabled(cfg *config.Config) bool

	// RegisterRoutes registers the module's routes to the provided router group
	RegisterRoutes(rg *gin.RouterGroup)

	// Start performs any async startup tasks (goroutines, watchers, etc.)
	// Called after all routes are registered and server is about to start.
	Start(ctx context.Context) error

	// Stop performs graceful shutdown for the module
	Stop(ctx context.Context) error
}

// Registry holds all registered modules and manages their lifecycle.
type Registry struct {
	mu      sync.Mutex
	modules []Module
	frozen  bool
}

// NewRegistry creates a new module registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make([]Module, 0),
	}
}

// Register adds a module to the registry. Returns an error if a module with
// the same name is already registered, or if the registry is frozen.
func (r *Registry) Register(m Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("registry is frozen: cannot register module %q", m.Name())
	}
	for _, existing := range r.modules {
		if existing.Name() == m.Name() {
			return fmt.Errorf("module %q already registered", m.Name())
		}
	}
	r.modules = append(r.modules, m)
	return nil
}

// Freeze prevents any further module registration. Safe to call concurrently
// with Register — but should be called once, before StartAll.
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// GetEnabled returns all modules that are enabled in the current config.
func (r *Registry) GetEnabled(cfg *config.Config) []Module {
	r.mu.Lock()
	defer r.mu.Unlock()
	enabled := make([]Module, 0)
	for _, m := range r.modules {
		if m.Enabled(cfg) {
			enabled = append(enabled, m)
		}
	}
	return enabled
}

// RegisterAllRoutes registers routes for all enabled modules.
func (r *Registry) RegisterAllRoutes(cfg *config.Config, apiGroup *gin.RouterGroup) {
	for _, m := range r.GetEnabled(cfg) {
		m.RegisterRoutes(apiGroup)
	}
}

// StartAll starts all enabled modules transactionally: if any module fails to
// start, all previously-started modules are stopped in reverse order and the
// aggregated error is returned.
func (r *Registry) StartAll(ctx context.Context, cfg *config.Config) error {
	var started []Module
	for _, m := range r.GetEnabled(cfg) {
		if err := m.Start(ctx); err != nil {
			// Rollback: stop already-started modules in reverse order.
			// Use a fresh background context with a 5-second timeout so that
			// Stop() can run even if the parent ctx has already been cancelled.
			var rollbackErrs []error
			for i := len(started) - 1; i >= 0; i-- {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if stopErr := started[i].Stop(stopCtx); stopErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("module %q rollback stop: %w", started[i].Name(), stopErr))
				}
				cancel()
			}
			if len(rollbackErrs) > 0 {
				return fmt.Errorf("module %q start failed: %w (rollback errors: %v)", m.Name(), err, rollbackErrs)
			}
			return fmt.Errorf("module %q start failed: %w", m.Name(), err)
		}
		started = append(started, m)
	}
	return nil
}

// StopAll stops all enabled modules in reverse order (last started, first
// stopped). Errors are aggregated with errors.Join so every module gets a
// chance to shut down gracefully.
func (r *Registry) StopAll(ctx context.Context, cfg *config.Config) error {
	modules := r.GetEnabled(cfg)
	var errs []error
	for i := len(modules) - 1; i >= 0; i-- {
		if err := modules[i].Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %q stop: %w", modules[i].Name(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("module shutdown errors: %v", errs)
	}
	return nil
}
