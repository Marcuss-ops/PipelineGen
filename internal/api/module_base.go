package api

import (
	"fmt"
	"sync"

	"github.com/gin-gonic/gin"
)

// Module is the common interface that all feature modules must implement.
// It is route-only: lifecycle (Start/Stop) is managed separately by the
// composition root (internal/app/lifecycle.go).
//
// Enabled() bakes the config check into the module at construction time so
// the API layer never imports config.Config.
type Module interface {
	// Name returns the unique name of the module (e.g., "artlist", "clips")
	Name() string

	// Enabled returns true when the module should have its routes registered.
	// The check is baked in at construction time (closure over config).
	Enabled() bool

	// RegisterRoutes registers the module's routes to the provided router group
	RegisterRoutes(rg *gin.RouterGroup)
}

// Registry holds all registered modules and manages route registration.
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

// Has reports whether a module with the given name is already registered.
// Used by composition-time coalesce on duplicate slot publications
// (PR17, June 2026): when DescriptorJobs and DescriptorProviders both
// publish the same capability name through a shared Descriptor, the
// second Register call should coalesce silently rather than fail.
//
// IMPORTANT — SCOPE OF COALESCE: any matching name coalesces silently,
// including between distinct capability instances. The composition-time
// invariant is that capability names are unique; if two unrelated
// Descriptors report the same Name(), the second is dropped on the floor
// here and the duplicate-bug goes undiagnosed. Defensive checks against
// this scenario belong in the composition root (registry.go::WireRegistry
// logging + a pre-flight deduplication audit) — NOT in this primitive.
//
// Frozen edge case: if the registry is FROZEN and a caller invokes
// Register for an already-present name, this primitive does NOT short-
// circuit on freeze — Compose callers that want freeze-respecting
// idempotency should check r.Frozen separately. In the current
// architecture WireRegistry is the only composition root and freezes
// only after every Register call has completed, so this edge is
// unreachable in practice.
func (r *Registry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.modules {
		if existing.Name() == name {
			return true
		}
	}
	return false
}

// Freeze prevents any further module registration. Safe to call concurrently
// with Register — but should be called once, before StartAll.
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// GetEnabled returns all modules whose Enabled() closure returns true.
func (r *Registry) GetEnabled() []Module {
	r.mu.Lock()
	defer r.mu.Unlock()
	enabled := make([]Module, 0)
	for _, m := range r.modules {
		if m.Enabled() {
			enabled = append(enabled, m)
		}
	}
	return enabled
}

// RegisterAllRoutes registers routes for all enabled modules.
func (r *Registry) RegisterAllRoutes(apiGroup *gin.RouterGroup) {
	for _, m := range r.GetEnabled() {
		m.RegisterRoutes(apiGroup)
	}
}
