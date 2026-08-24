// Package app owns the canonical composition-time registration phases.
package capabilities

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
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

// bootstrapProviderRegistry is the sole provider mutation phase. All provider
// adapters and descriptor-owned providers are registered before search graph
// composition, then the catalog is frozen exactly once.
func bootstrapProviderRegistry(provReg *providers.Registry, entries []TrackedProviderEntry, descriptors []module.DescriptorProviders) error {
	if provReg == nil {
		if len(entries) > 0 || len(descriptors) > 0 {
			return fmt.Errorf("bootstrapProviderRegistry: provider registry is required")
		}
		return nil
	}
	if provReg.IsFrozen() {
		return fmt.Errorf("bootstrapProviderRegistry: %w", providers.ErrFrozen)
	}
	if err := registerProviders(provReg, entries); err != nil {
		return fmt.Errorf("bootstrapProviderRegistry: providers: %w", err)
	}
	if err := registerProviderDescriptors(provReg, descriptors); err != nil {
		return fmt.Errorf("bootstrapProviderRegistry: provider-descriptors: %w", err)
	}
	provReg.Freeze()
	return nil
}

// registerCapabilities publishes prepared HTTP modules and validates the
// completed runtime graph. It deliberately cannot mutate providers.Registry.
func registerCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps) error {
	if reg == nil {
		return fmt.Errorf("registerCapabilities: nil api.Registry (composition bug)")
	}
	if provReg != nil && !provReg.IsFrozen() {
		return fmt.Errorf("registerCapabilities: provider registry must be frozen before final publication")
	}
	modules := append([]TrackedHTTPModule{}, deps.HTTPModules...)
	modules = append(modules, deps.Providers.HTTPModules...)
	if err := registerHTTPModules(reg, modules); err != nil {
		return fmt.Errorf("registerCapabilities: http-modules: %w", err)
	}
	if err := c3ValidateRuntimeGraph(); err != nil {
		return fmt.Errorf("registerCapabilities: runtime validation: %w", err)
	}
	return nil
}

func registerHTTPModules(reg *module.Registry, mods []TrackedHTTPModule) error {
	for _, tracked := range mods {
		if tracked.Module == nil {
			continue
		}
		if err := tryRegisterModuleStrict(reg, zap.NewNop(), tracked.Module, WithRegistrationPoint(tracked.Point)); err != nil {
			return err
		}
	}
	return nil
}

func registerProviders(provReg *providers.Registry, entries []TrackedProviderEntry) error {
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

func registerProviderDescriptors(provReg *providers.Registry, descriptors []module.DescriptorProviders) error {
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
		if err := descriptor.RegisterProviders(provReg); err != nil {
			return fmt.Errorf("registerProviderDescriptors: descriptor %d: %w", i, err)
		}
	}
	return nil
}

type strictOption func(*strictRegCtx)
type strictRegCtx struct{ point string }

func WithRegistrationPoint(point string) strictOption {
	return func(ctx *strictRegCtx) {
		if point != "" {
			ctx.point = point
		}
	}
}

func collectRegPoint(opts []strictOption) string {
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

func tryRegisterModuleStrict(registry *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	if registry == nil {
		return fmt.Errorf("compose: nil api.Registry passed to strict-register (registration-point=%s)", collectRegPoint(opts))
	}
	if mod == nil {
		return fmt.Errorf("compose: nil module passed (registration-point=%s)", collectRegPoint(opts))
	}
	runtimeModule, buildErr := module.RuntimeModuleFor(mod.Name(), collectRegPoint(opts), mod)
	if buildErr != nil {
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w", mod.Name(), mod, collectRegPoint(opts), buildErr)
	}
	if err := registry.RegisterRuntimeModule(runtimeModule); err != nil {
		if log != nil {
			log.Warn("strict-register failed", zap.String("module", mod.Name()), zap.String("registration-point", collectRegPoint(opts)), zap.Error(err))
		}
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w", mod.Name(), mod, collectRegPoint(opts), err)
	}
	return nil
}

func tryRegisterModule(registry *module.Registry, log *zap.Logger, mod module.Module) error {
	return tryRegisterModuleStrict(registry, log, mod)
}
