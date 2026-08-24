// Package api — module_descriptor.go: the canonical Capability Standard contract.
//
// Every capability exposes exactly one build function:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The Descriptor is consumed by the composition root
// (internal/app/registry.go::WireRegistry) and registered into the
// api.Registry. Today Descriptor is structurally equal to Module
// (the route contract); richer slots (Jobs, Events, Providers,
// HealthChecks, LifecycleHooks) are layered in as optional embedded
// interfaces or struct fields in successive migrations without
// breaking existing implementations.
//
// See ARCHITECTURE.md §Capability Standard for the target shape; see
// AGENTS.md **Modular edit patterns** §Pattern 0 for the lateral
// ruling (no setters, no late-binding).
package httpserver

import (
	"context"
)

// Descriptor is the canonical contract returned by a capability's
// Build(deps) function. It embeds Module so legacy route-only
// implementations satisfy Descriptor for free. Capabilities that
// grow richer features add optional embedded interfaces (e.g.
// DescriptorLifecycle, DescriptorJobs) without changing this core shape.
type Descriptor interface {
	Module
}

// DescriptorLifecycle is an optional interface a Descriptor may
// implement to participate in composition-managed Start/Stop. The
// composition root type-asserts for this interface; the capability
// can simply not implement it when it has no background goroutines
// or external clients to manage.
type DescriptorLifecycle interface {
	Lifecycle() LifecycleHooks
}

// LifecycleHooks is the optional lifecycle contract for capabilities
// that need Start/Stop. Either field may be nil to indicate
// "no-op at that phase".
type LifecycleHooks struct {
	Start func(ctx context.Context) error
	Stop  func(ctx context.Context) error
}

// JobRegistrar is the typed port that the worker-side publication
// slot accepts.
type JobRegistrar interface {
	RegisterHandler(jobType string, handler any) error
}

// DescriptorJobs is the optional interface a Descriptor may implement
// to publish worker handlers into the canonical jobs service.
type DescriptorJobs interface {
	RegisterJobHandlers(svc JobRegistrar) error
}

// ProviderRegistrar is the typed port that the catalog-publishing
// slot accepts.
type ProviderRegistrar interface {
	Register(p any) error
}

// DescriptorProviders is the optional interface a Descriptor may
// implement to publish catalog entries into the canonical providers.Registry.
type DescriptorProviders interface {
	RegisterProviders(reg ProviderRegistrar) error
}

// AsDescriptor adapts any existing Module into a Descriptor
// interface. Used when a capability pre-dates the Capability
// Standard and still wants its route-only wiring registered through
// the canonical Descriptor path (i.e. future-proofing against the
// richer Descriptor that future capabilities will return).
func AsDescriptor(m Module) Descriptor {
	if m == nil {
		return nil
	}
	if d, ok := m.(Descriptor); ok {
		return d
	}
	return descriptorAdapter{Module: m}
}

// descriptorAdapter is the trivial embedding adapter returned by
// AsDescriptor for non-Descriptor Modules. The empty Lifecycle
// methods make the type a valid concrete Descriptor.
type descriptorAdapter struct {
	Module
}

// Lifecycle returns the zero-value LifecycleHooks. The composition
// root treats this as "no lifecycle hooks".
func (descriptorAdapter) Lifecycle() LifecycleHooks { return LifecycleHooks{} }
