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
package api

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
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
// slot accepts. The real *appjobs.Service satisfies this (its
// RegisterHandler method has the matching signature); test stubs
// satisfy it too without depending on the concrete service.
//
// Capabilities that own worker-side logic (Generation owns the
// books.process and lessons.process handlers; Assets/module indexer
// owns media.reindex) implement DescriptorJobs and confirm at
// compile time that the canonical service satisfies JobRegistrar
// (assertion below).
type JobRegistrar interface {
	RegisterHandler(jobType string, handler any) error
}

// DescriptorJobs is the optional interface a Descriptor may implement
// to publish worker handlers into the canonical jobs service.
// Capabilities that own worker-side logic for background jobs
// implement this and the composition root type-asserts.
//
// The slot takes a JobRegistrar (typed port), not the concrete
// *appjobs.Service, so the composition root can inject the concrete
// service at descriptor-wiring time WITHOUT late-binding setters AND
// without coupling the capability to the concrete. Capabilities that
// don't have worker handlers can simply not implement DescriptorJobs.
type DescriptorJobs interface {
	RegisterJobHandlers(svc JobRegistrar) error
}

// ProviderRegistrar is the typed port that the catalog-publishing
// slot accepts. The real *providers.Registry satisfies this (its
// Register method has the matching signature); test stubs satisfy
// it too without depending on the concrete registry.
//
// Capabilities that publish catalog entries (asset providers like
// script_assets) into the canonical providers.Registry implement
// DescriptorProviders and confirm at compile time that the canonical
// registry satisfies ProviderRegistrar (assertion at the bottom of
// this file).
type ProviderRegistrar interface {
	// Register adds a provider under its Name(). Implementations MUST
	// reject duplicate names, nil providers, and empty names —
	// the registry sentinel errors ErrAlreadyRegistered / ErrNilProvider
	// / ErrEmptyName document these invariants. Frozen registries MUST
	// reject post-freeze Register calls with ErrFrozen.
	Register(p providers.Provider) error
}

// DescriptorProviders is the optional interface a Descriptor may
// implement to publish catalog entries (asset providers like
// script_assets) into the canonical providers.Registry. The
// composition root type-asserts for this interface; capabilities with
// no catalog contribution can simply not implement it.
//
// Pattern parity with DescriptorJobs (worker-handler publication vs
// provider-catalog publication) — proves the slot-extensibility claim
// of the Capability Standard beyond the original Generation precedent.
// DescriptorJobs is per-job runtime registration; DescriptorProviders
// is a one-shot composition-time publication. Both slots coexist on
// the same Descriptor if a capability owns both kinds of side-effects.
type DescriptorProviders interface {
	RegisterProviders(reg ProviderRegistrar) error
}

// AsDescriptor adapts any existing Module into a Descriptor
// interface. Used when a capability pre-dates the Capability
// Standard and still wants its route-only wiring registered through
// the canonical Descriptor path (i.e. future-proofing against the
// richer Descriptor that future capabilities will return).
//
// New capabilities should return a Descriptor-native type directly.
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

// Compile-time assertion: the canonical worker-side jobs service
// satisfies the JobRegistrar port. Drift in RegisterHandler's
// signature, the package path, or the concrete service name is a
// build error, not a runtime nil-deref.
//
// The api package imports appjobs (not the other way around): api
// declares JobRegistrar; appjobs.Service satisfies it. There is no
// cycle because internal/capabilities/jobs/queue does not import
// internal/api (verified by the project's layering — see ARCHITECTURE.md
// §13 "pkg/ is leaf-only"; api lives at the transport layer, jobs
// lives at the application layer).
var _ JobRegistrar = (*appjobs.Service)(nil)

// Compile-time assertion: the canonical providers.Registry
// satisfies the ProviderRegistrar port. Drift in Register's
// signature or package path is a build error, not a runtime
// nil-deref.
//
// The api package imports providers (not the other way around):
// api declares ProviderRegistrar; providers.Registry satisfies it.
// No cycle because internal/capabilities/assets/providers does not
// import internal/api.
var _ ProviderRegistrar = (*providers.Registry)(nil)
