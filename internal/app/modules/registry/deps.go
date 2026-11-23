package registry

// deps.go owns the typed input contracts of the registry module.
//
// RegisterCapabilities took a registry, a provider registry, the capability
// bundle and a validation callback; TryRegisterModuleStrict took a registry,
// a logger and a module plus variadic registration options. Both lists mix
// unrelated layers, so the caller had to know the order by heart. Named fields
// make the composition sites read as declarations (BON-03 step B).

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

// CapabilityRegistrationDeps is the input contract of RegisterCapabilities.
// Validate may be nil: it is a caller-supplied runtime-graph check, not a
// dependency of the registration itself.
type CapabilityRegistrationDeps struct {
	Registry     *module.Registry
	Providers    *providers.Registry
	Capabilities CapabilityDeps
	Validate     func() error
}

// RegisterModuleDeps is the input contract of TryRegisterModuleStrict.
// Registration options stay variadic because they are per-call modifiers, not
// dependencies.
type RegisterModuleDeps struct {
	Registry *module.Registry
	Log      *zap.Logger
	Module   module.Module
}
