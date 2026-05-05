// Package module defines the common interface for all PipelineGen modules.
package module

import (
	"context"

	"github.com/gin-gonic/gin"

	"velox/go-master/pkg/config"
)

// Module is the common interface that all feature modules must implement.
// This allows for clean registration and lifecycle management.
type Module interface {
	// Name returns the unique name of the module (e.g., "artlist", "youtube-clips")
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
	modules []Module
}

// NewRegistry creates a new module registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make([]Module, 0),
	}
}

// Register adds a module to the registry.
func (r *Registry) Register(m Module) {
	r.modules = append(r.modules, m)
}

// GetEnabled returns all modules that are enabled in the current config.
func (r *Registry) GetEnabled(cfg *config.Config) []Module {
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

// StartAll starts all enabled modules.
func (r *Registry) StartAll(ctx context.Context, cfg *config.Config) error {
	for _, m := range r.GetEnabled(cfg) {
		if err := m.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all enabled modules.
func (r *Registry) StopAll(ctx context.Context, cfg *config.Config) error {
	for _, m := range r.GetEnabled(cfg) {
		if err := m.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}
