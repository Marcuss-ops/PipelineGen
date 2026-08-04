package api

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// RuntimeRouteDescriptor is the composition-time description of one HTTP
// route module owned by a capability. Module is the executable route owner;
// the descriptor carries the capability and registration point used for
// diagnostics and generated inventories.
type RuntimeRouteDescriptor struct {
	Capability        string
	RegistrationPoint string
	Module            Module
}

// JobHandlerDescriptor describes one worker handler owned by a capability.
type JobHandlerDescriptor struct {
	Type    string
	Handler any
}

// RuntimeModule is the immutable output of a capability Build operation.
type RuntimeModule struct {
	Name        string
	Routes      []RuntimeRouteDescriptor
	JobHandlers []JobHandlerDescriptor
	Lifecycle   LifecycleHooks
}

// CapabilityModule is the canonical runtime-builder contract. A capability
// builds a complete descriptor before the composition root publishes it.
type CapabilityModule interface {
	Name() string
	Build(BuildContext) (RuntimeModule, error)
}

// BuildContext contains composition-time inputs. Dependencies remain
// capability-owned; this context carries only shared publication ports.
type BuildContext struct {
	JobRegistrar JobRegistrar
}

// StaticCapabilityModule adapts an already-built API module while a
// capability-specific builder is being moved out of internal/app. It is a
// real CapabilityModule, not a second registration path: the composition root
// can use it to publish the same RuntimeModule shape during the cutover.
type StaticCapabilityModule struct {
	Capability string
	Point      string
	Module     Module
	Jobs       []JobHandlerDescriptor
}

func (m StaticCapabilityModule) Name() string { return m.Capability }

func (m StaticCapabilityModule) Build(BuildContext) (RuntimeModule, error) {
	descriptor := RuntimeModule{
		Name: m.Capability,
		Routes: []RuntimeRouteDescriptor{{
			Capability: m.Capability, RegistrationPoint: m.Point, Module: m.Module,
		}},
		JobHandlers: append([]JobHandlerDescriptor(nil), m.Jobs...),
	}
	if err := ValidateRuntimeModule(descriptor); err != nil {
		return RuntimeModule{}, err
	}
	return descriptor, nil
}

// DescriptorJobHandlers is the richer job publication slot. It allows the
// composition root to validate and publish the complete job graph from
// descriptors instead of invoking an opaque registration callback.
type DescriptorJobHandlers interface {
	JobHandlers() []JobHandlerDescriptor
}

// RuntimeModuleFor wraps an existing route Module in the runtime descriptor
// contract. It is the compatibility bridge used while capability-specific
// builders move out of internal/app.
func RuntimeModuleFor(capability, point string, m Module) (RuntimeModule, error) {
	return StaticCapabilityModule{Capability: capability, Point: point, Module: m}.Build(BuildContext{})
}

// ValidateRuntimeModule rejects incomplete or duplicate-prone descriptors
// before any route or job is published.
func ValidateRuntimeModule(m RuntimeModule) error {
	if m.Name == "" {
		return fmt.Errorf("runtime module name is empty")
	}
	if len(m.Routes) == 0 {
		return fmt.Errorf("runtime module %q has no routes", m.Name)
	}
	seenRoutes := make(map[string]struct{}, len(m.Routes))
	for i, route := range m.Routes {
		if route.Module == nil {
			return fmt.Errorf("runtime module %q route %d has nil module", m.Name, i)
		}
		name := route.Module.Name()
		if name == "" {
			return fmt.Errorf("runtime module %q route %d has empty module name", m.Name, i)
		}
		if _, ok := seenRoutes[name]; ok {
			return fmt.Errorf("runtime module %q has duplicate route module %q", m.Name, name)
		}
		seenRoutes[name] = struct{}{}
	}
	seenJobs := make(map[string]struct{}, len(m.JobHandlers))
	for i, job := range m.JobHandlers {
		if job.Type == "" {
			return fmt.Errorf("runtime module %q job %d has empty type", m.Name, i)
		}
		if job.Handler == nil {
			return fmt.Errorf("runtime module %q job %q has nil handler", m.Name, job.Type)
		}
		if _, ok := seenJobs[job.Type]; ok {
			return fmt.Errorf("runtime module %q has duplicate job type %q", m.Name, job.Type)
		}
		seenJobs[job.Type] = struct{}{}
	}
	return nil
}

// RegisterRuntimeModule atomically publishes all routes and the descriptor.
// Duplicate validation happens before either registry slice is mutated.
func (r *Registry) RegisterRuntimeModule(m RuntimeModule) error {
	if err := ValidateRuntimeModule(m); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("registry is frozen: cannot register runtime module %q", m.Name)
	}

	for _, existing := range r.runtimeModules {
		if existing.Name != m.Name {
			continue
		}
		if sameRuntimeModule(existing, m) {
			return nil
		}
		return fmt.Errorf("runtime module %q already registered", m.Name)
	}
	for _, route := range m.Routes {
		for _, existing := range r.modules {
			if existing == route.Module {
				continue
			}
			if existing.Name() == route.Module.Name() {
				return fmt.Errorf("module %q already registered", route.Module.Name())
			}
		}
	}
	for _, route := range m.Routes {
		alreadyRegistered := false
		for _, existing := range r.modules {
			if existing == route.Module {
				alreadyRegistered = true
				break
			}
		}
		if !alreadyRegistered {
			r.modules = append(r.modules, route.Module)
		}
	}
	r.runtimeModules = append(r.runtimeModules, cloneRuntimeModule(m))
	return nil
}

func sameRuntimeModule(a, b RuntimeModule) bool {
	if len(a.Routes) != len(b.Routes) || len(a.JobHandlers) != len(b.JobHandlers) {
		return false
	}
	for i := range a.Routes {
		if a.Routes[i].Module != b.Routes[i].Module {
			return false
		}
	}
	for i := range a.JobHandlers {
		// Handler values may be functions or other non-comparable values;
		// type equality is sufficient for idempotent descriptor publication.
		if a.JobHandlers[i].Type != b.JobHandlers[i].Type {
			return false
		}
	}
	return true
}

func cloneRuntimeModule(m RuntimeModule) RuntimeModule {
	m.Routes = append([]RuntimeRouteDescriptor(nil), m.Routes...)
	m.JobHandlers = append([]JobHandlerDescriptor(nil), m.JobHandlers...)
	return m
}

// RegisterRuntimeJobHandlers validates the full descriptor before publishing
// any handler, preventing partial job graphs on malformed descriptors.
func RegisterRuntimeJobHandlers(svc JobRegistrar, descriptor DescriptorJobHandlers) error {
	if svc == nil {
		return fmt.Errorf("runtime job registrar is nil")
	}
	if descriptor == nil {
		return fmt.Errorf("runtime job descriptor is nil")
	}
	handlers := descriptor.JobHandlers()
	seen := make(map[string]struct{}, len(handlers))
	for i, job := range handlers {
		if job.Type == "" || job.Handler == nil {
			return fmt.Errorf("runtime job descriptor handler %d is incomplete", i)
		}
		if _, ok := seen[job.Type]; ok {
			return fmt.Errorf("runtime job descriptor contains duplicate job type %q", job.Type)
		}
		seen[job.Type] = struct{}{}
	}
	for _, job := range handlers {
		if err := svc.RegisterHandler(job.Type, job.Handler); err != nil {
			return fmt.Errorf("register runtime job %q: %w", job.Type, err)
		}
	}
	return nil
}

// RegisterCapabilityModule builds and publishes a native capability module.
// This is the composition-root entry point for capabilities that have moved
// their builder out of internal/app.
func (r *Registry) RegisterCapabilityModule(capability CapabilityModule, ctx BuildContext) error {
	if capability == nil {
		return fmt.Errorf("runtime capability module is nil")
	}
	if capability.Name() == "" {
		return fmt.Errorf("runtime capability module name is empty")
	}
	descriptor, err := capability.Build(ctx)
	if err != nil {
		return fmt.Errorf("build capability %q: %w", capability.Name(), err)
	}
	if descriptor.Name == "" {
		descriptor.Name = capability.Name()
	}
	return r.RegisterRuntimeModule(descriptor)
}

// RegisterRuntimeRoutes is the explicit compatibility entry point for route
// modules that have not yet migrated to a native CapabilityModule builder.
func RegisterRuntimeRoutes(reg *Registry, capability, point string, m Module) error {
	descriptor, err := RuntimeModuleFor(capability, point, m)
	if err != nil {
		return err
	}
	return reg.RegisterRuntimeModule(descriptor)
}

// RuntimeModules returns a deep snapshot of descriptors published into the
// registry, so callers cannot mutate registry-owned slices.
func (r *Registry) RuntimeModules() []RuntimeModule {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RuntimeModule, len(r.runtimeModules))
	for i, module := range r.runtimeModules {
		out[i] = cloneRuntimeModule(module)
	}
	return out
}

var _ interface{ RegisterRoutes(*gin.RouterGroup) } = (*RouteModule)(nil)
