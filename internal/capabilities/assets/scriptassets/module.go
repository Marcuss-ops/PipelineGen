// Package scriptassets — module.go: the single canonical Build
// entrypoint for the script_assets capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (*ScriptAssetsDescriptor, error)
//
// The result is complete: missing mandatory dependencies return an
// error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor
// is ready to publish its catalog entry via the
// api.DescriptorProviders slot.
//
// Used by the composition root at internal/app/registry.go::WireRegistry.
//
// This module.go proves the DescriptorProviders slot pattern (the
// "richer" capability migration requested after the Generation
// precedent). The former HTTP catalog route
// (GET /api/script-assets/catalog) was retired: capability discovery
// is owned by GET /api/capabilities, while this descriptor's only
// composition-time effect is publishing the ScriptAssetsProvider into
// the canonical providers.Registry.
package scriptassets

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	appscriptassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scriptassets"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
//
// Logger is the only current field. nil → zap.NewNop() at composition.
//
// Future fields (e.g. a JobService port for catalog-driven background
// reconciliation) land here when needed. Per AGENTS.md **Modular edit
// patterns** §Pattern 0 (Port abstraction layer), new dependencies
// are added via the typed-port convention used elsewhere in
// internal/application/.
type Dependencies struct {
	// Logger is the canonical structured logger. nil at composition
	// is replaced with zap.NewNop() so wiring sites do not need a
	// nil-check. *zap.Logger satisfies the provider's Local logger
	// interface (Info + Warn, variadic any).
	Logger *zap.Logger
}

// ScriptAssetsDescriptor is the concrete capability Descriptor
// returned by Build. It satisfies api.DescriptorProviders via the
// explicit RegisterProviders method.
//
// The Provider field gives the composition root typed, non-cast
// access to the *ScriptAssetsProvider for any consumers that need it
// (e.g. composition-time routing of the descriptor into the assets
// bundle).
type ScriptAssetsDescriptor struct {
	// Service is exposed so non-HTTP callers (future internal
	// services, admin tools, tests) can drive the capability
	// without re-constructing the use-case layer.
	Service *appscriptassets.Service
	// Provider is exposed so the composition root can publish it
	// into the canonical providers.Registry via the
	// DescriptorProviders slot. Same nil-safety as Service.
	Provider *appscriptassets.ScriptAssetsProvider
}

// ── DescriptorProviders satisfaction ─────────────────────────
// RegisterProviders publishes the script_assets catalog entry into
// the canonical providers.Registry via the ProviderRegistrar port.
// Failure modes:
//
//   - reg == nil → typed error (cannot default to a no-op registrar;
//     silent-skip would hide composition-time wiring errors).
//   - d.Provider == nil → typed error (Build must always set Provider;
//     a nil Provider indicates a regression in Build itself).
//   - reg.Register(d.Provider) returns error → propagated verbatim
//     (ErrAlreadyRegistered / ErrFrozen / ErrEmptyName / ErrNilProvider).
//
// The composition root wraps the registry call inside tryRegisterModule,
// so a single failure surfaces with the SSOT "compose: module=X already
// registered" marker on the registry side AND the typed provider-side
// error (via RegisterProviders's return).
func (d *ScriptAssetsDescriptor) RegisterProviders(reg api.ProviderRegistrar) error {
	if reg == nil {
		return fmt.Errorf("script_assets: provider registrar is required (composition must wire providers.Registry)")
	}
	if d.Provider == nil {
		return fmt.Errorf("script_assets: provider not initialized (Build regression)")
	}
	return reg.Register(d.Provider)
}

// Build constructs the script_assets capability: ScriptAssetsProvider →
// Service. The returned Descriptor carries the Service + Provider slots
// so the composition root publishes the provider via the
// DescriptorProviders slot.
//
// Logger is replaced with zap.NewNop() when nil so wiring sites do
// not need to nil-check.
//
// The capability intentionally has NO mandatory namespace dep —
// ScriptAssets is a static catalog with no DB reads, no Qdrant
// queries, no LLM calls. This keeps Build's failure surface narrow:
// only Logger nil → fallback (never an error) and any panic from New*
// constructors (not expected — they're pure).
func Build(deps Dependencies) (*ScriptAssetsDescriptor, error) {
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	provider := appscriptassets.NewScriptAssetsProvider(zapLoggerAdapter{log})
	svc := appscriptassets.NewService(provider)

	return &ScriptAssetsDescriptor{
		Service:  svc,
		Provider: provider,
	}, nil
}

// zapLoggerAdapter bridges *zap.Logger (which has a strongly-typed
// field-based API) to the provider's minimal Info/Warn any-shape
// surface. Keeps the provider free of a direct zap dependency, which
// simplifies test stubbing.
//
// *zap.Logger.Info / Warn accept ...zap.Field; here we degrade to
// plain key/value pairs via fmt-style formatting (acceptable for the
// provider's stand-up implementation; production enrichment moves to
// structured zap fields in a follow-up PR).
type zapLoggerAdapter struct {
	l *zap.Logger
}

func (z zapLoggerAdapter) Info(msg string, kv ...any) {
	if z.l == nil {
		return
	}
	fields := make([]zap.Field, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, zap.Any(key, kv[i+1]))
	}
	z.l.Info(msg, fields...)
}

func (z zapLoggerAdapter) Warn(msg string, kv ...any) {
	if z.l == nil {
		return
	}
	fields := make([]zap.Field, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, zap.Any(key, kv[i+1]))
	}
	z.l.Warn(msg, fields...)
}
