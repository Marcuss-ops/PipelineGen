package wiring

import (
	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

type PreparedCapabilities = registrywiring.PreparedCapabilities
type CapabilityDeps = registrywiring.CapabilityDeps
type TrackedHTTPModule = registrywiring.TrackedHTTPModule
type TrackedProviderEntry = registrywiring.TrackedProviderEntry
type ProviderKind = registrywiring.ProviderKind

const (
	ProviderKindSearch = registrywiring.ProviderKindSearch
	ProviderKindFetch  = registrywiring.ProviderKindFetch
)

type strictOption = registrywiring.StrictOption

func bootstrapProviderRegistry(provReg *providers.Registry, entries []TrackedProviderEntry, descriptors []module.DescriptorProviders) error {
	return registrywiring.BootstrapProviderRegistry(provReg, entries, descriptors)
}

func registerCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps) error {
	return registrywiring.RegisterCapabilities(reg, provReg, deps, registrywiring.ValidateRuntimeGraph)
}

func registerHTTPModules(reg *module.Registry, mods []TrackedHTTPModule) error {
	return registrywiring.RegisterHTTPModules(reg, mods)
}

func registerProviders(provReg *providers.Registry, entries []TrackedProviderEntry) error {
	return registrywiring.RegisterProviders(provReg, entries)
}

func registerProviderDescriptors(provReg *providers.Registry, descriptors []module.DescriptorProviders) error {
	return registrywiring.RegisterProviderDescriptors(provReg, descriptors)
}

func WithRegistrationPoint(point string) strictOption {
	return registrywiring.WithRegistrationPoint(point)
}

func tryRegisterModuleStrict(reg *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	return registrywiring.TryRegisterModuleStrict(reg, log, mod, opts...)
}

func tryRegisterModule(reg *module.Registry, log *zap.Logger, mod module.Module) error {
	return registrywiring.TryRegisterModule(reg, log, mod)
}
