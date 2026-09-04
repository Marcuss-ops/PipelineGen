package registry

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

type PreparedCapabilities struct {
	HTTPModules []TrackedHTTPModule
}

type CapabilityDeps struct {
	HTTPModules []TrackedHTTPModule
	Providers   PreparedCapabilities
}

type TrackedHTTPModule struct {
	Module module.Module
	Point  string
}

type TrackedProviderEntry struct {
	Id     string
	Kind   ProviderKind
	Search providers.SearchProvider
	Fetch  providers.FetchProvider
}

type ProviderKind int

const (
	ProviderKindSearch ProviderKind = iota
	ProviderKindFetch
)

// BootstrapProviderRegistry is the sole provider mutation phase. It registers
// all providers and descriptor-owned providers, then freezes the registry.
func BootstrapProviderRegistry(provReg *providers.Registry, entries []TrackedProviderEntry, descriptors []module.DescriptorProviders) error {
	if provReg == nil {
		if len(entries) > 0 || len(descriptors) > 0 {
			return fmt.Errorf("bootstrapProviderRegistry: provider registry is required")
		}
		return nil
	}
	if provReg.IsFrozen() {
		return fmt.Errorf("bootstrapProviderRegistry: %w", providers.ErrFrozen)
	}
	if err := RegisterProviders(provReg, entries); err != nil {
		return fmt.Errorf("bootstrapProviderRegistry: providers: %w", err)
	}
	if err := RegisterProviderDescriptors(provReg, descriptors); err != nil {
		return fmt.Errorf("bootstrapProviderRegistry: provider-descriptors: %w", err)
	}
	provReg.Freeze()
	return nil
}

// RegisterCapabilities publishes prepared HTTP modules and optionally validates
// the completed runtime graph. Provider mutation is forbidden at this phase.
func RegisterCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps, validate func() error) error {
	if reg == nil {
		return fmt.Errorf("registerCapabilities: nil api.Registry (composition bug)")
	}
	if provReg != nil && !provReg.IsFrozen() {
		return fmt.Errorf("registerCapabilities: provider registry must be frozen before final publication")
	}
	modules := append([]TrackedHTTPModule{}, deps.HTTPModules...)
	modules = append(modules, deps.Providers.HTTPModules...)
	if err := RegisterHTTPModules(reg, modules); err != nil {
		return fmt.Errorf("registerCapabilities: http-modules: %w", err)
	}
	if len(modules) > 0 && validate != nil {
		if err := validate(); err != nil {
			return fmt.Errorf("registerCapabilities: runtime validation: %w", err)
		}
	}
	return nil
}

func RegisterHTTPModules(reg *module.Registry, mods []TrackedHTTPModule) error {
	for _, tracked := range mods {
		if tracked.Module == nil {
			continue
		}
		if err := TryRegisterModuleStrict(reg, zap.NewNop(), tracked.Module, WithRegistrationPoint(tracked.Point)); err != nil {
			return err
		}
	}
	return nil
}

func RegisterProviders(provReg *providers.Registry, entries []TrackedProviderEntry) error {
	if provReg == nil {
		if len(entries) > 0 {
			return fmt.Errorf("registerProviders: provider registry is required for %d entries", len(entries))
		}
		return nil
	}
	for _, entry := range entries {
		if entry.Id == "" {
			return fmt.Errorf("registerProviders: provider id is empty")
		}
		switch entry.Kind {
		case ProviderKindSearch:
			if entry.Search == nil {
				return fmt.Errorf("registerProviders: search %q is nil", entry.Id)
			}
			if err := provReg.RegisterSearch(entry.Search); err != nil {
				return fmt.Errorf("registerProviders: search %q: %w", entry.Id, err)
			}
		case ProviderKindFetch:
			if entry.Fetch == nil {
				return fmt.Errorf("registerProviders: fetch %q is nil", entry.Id)
			}
			if err := provReg.RegisterFetch(entry.Fetch); err != nil {
				return fmt.Errorf("registerProviders: fetch %q: %w", entry.Id, err)
			}
		default:
			return fmt.Errorf("registerProviders: provider %q has unknown kind %d", entry.Id, entry.Kind)
		}
	}
	return nil
}

func RegisterProviderDescriptors(provReg *providers.Registry, descriptors []module.DescriptorProviders) error {
	if provReg == nil {
		if len(descriptors) > 0 {
			return fmt.Errorf("registerProviderDescriptors: provider registry is required for %d descriptors", len(descriptors))
		}
		return nil
	}
	for i, descriptor := range descriptors {
		if descriptor == nil {
			return fmt.Errorf("registerProviderDescriptors: descriptor %d is nil", i)
		}
		if err := descriptor.RegisterProviders(providerRegistrarAdapter{reg: provReg}); err != nil {
			return fmt.Errorf("registerProviderDescriptors: descriptor %d: %w", i, err)
		}
	}
	return nil
}

type StrictOption func(*strictRegCtx)
type strictRegCtx struct{ point string }

func WithRegistrationPoint(point string) StrictOption {
	return func(ctx *strictRegCtx) {
		if point != "" {
			ctx.point = point
		}
	}
}

func collectRegPoint(opts []StrictOption) string {
	var ctx strictRegCtx
	for _, option := range opts {
		if option != nil {
			option(&ctx)
		}
	}
	if ctx.point == "" {
		return "unknown"
	}
	return ctx.point
}

func TryRegisterModuleStrict(reg *module.Registry, log *zap.Logger, mod module.Module, opts ...StrictOption) error {
	point := collectRegPoint(opts)
	if reg == nil {
		return fmt.Errorf("compose: nil api.Registry passed to strict-register (registration-point=%s)", point)
	}
	if mod == nil {
		return fmt.Errorf("compose: nil module passed (registration-point=%s)", point)
	}
	runtimeModule, buildErr := module.RuntimeModuleFor(mod.Name(), point, mod)
	if buildErr != nil {
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w", mod.Name(), mod, point, buildErr)
	}
	if err := reg.RegisterRuntimeModule(runtimeModule); err != nil {
		if log != nil {
			log.Warn("strict-register failed", zap.String("module", mod.Name()), zap.String("registration-point", point), zap.Error(err))
		}
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w", mod.Name(), mod, point, err)
	}
	return nil
}

func TryRegisterModule(reg *module.Registry, log *zap.Logger, mod module.Module) error {
	return TryRegisterModuleStrict(reg, log, mod)
}

type providerRegistrarAdapter struct {
	reg *providers.Registry
}

func (a providerRegistrarAdapter) Register(p any) error {
	if a.reg == nil {
		return fmt.Errorf("provider registry not wired")
	}
	if prov, ok := p.(providers.Provider); ok {
		return a.reg.Register(prov)
	}
	return fmt.Errorf("cannot register %T as provider", p)
}
