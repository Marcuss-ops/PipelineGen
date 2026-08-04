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
	mu             sync.Mutex
	modules        []Module
	runtimeModules []RuntimeModule
	frozen         bool
}

// NewRegistry creates a new module registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make([]Module, 0),
	}
}

// Register adds a module to the registry. Returns a sentinel error when:
//
//   - the module is nil                          ("nil module passed to Registry.Register")
//   - the module name is empty                   ("module name is empty")
//   - the registry is frozen                     ("registry is frozen: cannot register module %q")
//   - a DIFFERENT instance with the same name is already registered
//     ("module %q already registered")
//
// PR 2 (June 2026 — codex/registry-strict-uniqueness) invariants:
//  1. Empty-name validation: a module with Name() == "" is rejected up front,
//     so a misconfigured capability cannot silently pollute GetEnabled().
//  2. Same-instance no-op: re-registering the SAME Module pointer is a
//     silent no-op. This supports the composition-time contract that a
//     single Descriptor can be re-published (once via Register, once via
//     a slot publication such as DescriptorJobs/DescriptorProviders)
//     without surfacing as an error. A DIFFERENT instance with the same
//     name still errors. Tests pin this contract (see
//     internal/app/registry_strict_test.go).
func (r *Registry) Register(m Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m == nil {
		return fmt.Errorf("nil module passed to Registry.Register")
	}
	name := m.Name()
	if name == "" {
		return fmt.Errorf("module name is empty")
	}
	if r.frozen {
		return fmt.Errorf("registry is frozen: cannot register module %q", name)
	}
	for _, existing := range r.modules {
		if existing == m {
			// Same-instance re-registration: silent no-op (PR 2 invariant).
			return nil
		}
		if existing.Name() == name {
			return fmt.Errorf("module %q already registered", name)
		}
	}
	r.modules = append(r.modules, m)
	return nil
}

// Find returns the Module registered under the given name, or false if no
// module is registered with that name. READ-ONLY inspection helper.
//
// PR 2 NOTE: Find is NOT a coalescing path. The previous tryRegisterModule
// coalescer was deleted in PR 1 (commit 81e79728); Find exists solely so
// tests can pin the "registered exactly once per name" invariant. The
// composition root never calls Find — it routes new registrations through
// Register and inspects the registered set through GetEnabled.
func (r *Registry) Find(name string) (Module, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.modules {
		if m.Name() == name {
			return m, true
		}
	}
	return nil, false
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

// RegisterAllRoutes registers routes from the immutable runtime descriptors.
// A registry populated by the composition root therefore uses the same
// descriptor snapshot for publication and inspection. The module slice fallback
// preserves direct test/legacy registrations that predate RuntimeModule.
func (r *Registry) RegisterAllRoutes(apiGroup *gin.RouterGroup) {
	r.mu.Lock()
	descriptors := append([]RuntimeModule(nil), r.runtimeModules...)
	legacy := append([]Module(nil), r.modules...)
	r.mu.Unlock()

	if len(descriptors) > 0 {
		for _, descriptor := range descriptors {
			for _, route := range descriptor.Routes {
				if route.Module != nil && route.Module.Enabled() {
					route.Module.RegisterRoutes(apiGroup)
				}
			}
		}
		return
	}
	for _, m := range legacy {
		if m.Enabled() {
			m.RegisterRoutes(apiGroup)
		}
	}
}
