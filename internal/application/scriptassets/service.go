// Package scriptassets — service.go: the canonical Service for the
// script_assets capability. The Service is the thin orchestration
// layer between the HTTP handler (handler.go) and the
// ScriptAssetsProvider (provider.go).
//
// Today the Service has ONE public method, Catalog(), which returns
// the provider's static catalog so handlers can advertise it via
// GET /script-assets/catalog. Production enrichment (Search
// forwarding to ScriptAssetsProvider.Search, per-language catalog
// filtering) lands in a follow-up PR — the stand-up Service proves
// only the Build + DescriptorProviders slot contract, which is the
// scope of this migration.
package scriptassets

// Service is the canonical orchestration layer for script_assets.
// Composition root accesses the canonical instance via the
// ScriptAssetsDescriptor typed field exposed by module.go::Build —
// non-composition callers (tests, future internal services) construct
// it directly via NewService.
type Service struct {
	provider *ScriptAssetsProvider
}

// Compile-time assertion that *Service exposes the Provider() accessor
// the composition root needs to wire DescriptorProviders publication.
// Drift surfaces here as a build error.
var _ providerAccessor = (*Service)(nil)

// providerAccessor is the minimal interface composition roots use to
// pull the *ScriptAssetsProvider out of the Service for the
// DescriptorProviders publication step. Defining it as a private
// package-local interface keeps the public API surface narrow while
// still surfacing drift at the assert below.
type providerAccessor interface {
	Provider() *ScriptAssetsProvider
}

// NewService constructs a Service from a pre-built ScriptAssetsProvider.
// The provider is required (a nil provider cannot be published into the
// catalog even if the HTTP layer is alive).
func NewService(provider *ScriptAssetsProvider) *Service {
	return &Service{provider: provider}
}

// Provider returns the underlying ScriptAssetsProvider so the
// composition root can publish it into the canonical
// providers.Registry via the DescriptorProviders slot.
// Products of the canonical Build() function expose this through
// the typed ScriptAssetsDescriptor.Provider field — tests that construct
// Service via NewService must call Provider() explicitly.
func (s *Service) Provider() *ScriptAssetsProvider {
	if s == nil {
		return nil
	}
	return s.provider
}

// Catalog returns the provider's static capability list — one
// string per Capability. Stable order matches Capabilities() in
// provider.go. Today the stand-up catalog advertises two entries
// (Search + Script); the handler renders this verbatim as JSON.
func (s *Service) Catalog() []string {
	if s == nil || s.provider == nil {
		return nil
	}
	caps := s.provider.Capabilities()
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	return out
}
