// Package module provides base implementations for common module patterns.
package module

import (
	"context"

	"github.com/gin-gonic/gin"

	"velox/go-master/pkg/config"
)

// BaseModule provides a simple default implementation of the Module interface.
// Modules can embed this and override only what they need.
type BaseModule struct {
	name    string
	enabled func(cfg *config.Config) bool
}

// NewBaseModule creates a new base module.
func NewBaseModule(name string, enabled func(cfg *config.Config) bool) *BaseModule {
	return &BaseModule{
		name:    name,
		enabled: enabled,
	}
}

// Name returns the module name.
func (m *BaseModule) Name() string {
	return m.name
}

// Enabled checks if the module is enabled.
func (m *BaseModule) Enabled(cfg *config.Config) bool {
	if m.enabled != nil {
		return m.enabled(cfg)
	}
	return false
}

// RegisterRoutes registers the module's routes.
// Override this in embedded structs.
func (m *BaseModule) RegisterRoutes(rg *gin.RouterGroup) {
	// Override in implementation
}

// Start performs startup tasks.
// Override this in embedded structs.
func (m *BaseModule) Start(ctx context.Context) error {
	return nil
}

// Stop performs shutdown tasks.
// Override this in embedded structs.
func (m *BaseModule) Stop(ctx context.Context) error {
	return nil
}
