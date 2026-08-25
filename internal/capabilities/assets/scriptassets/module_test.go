// Package scriptassets — module_test.go: unit tests for the
// ScriptAssetsProvider + Build + DescriptorProviders slot.
//
// Tests cover:
//
//   - Provider identity (Name, Capabilities invariants)
//   - Provider.Search deterministic single-candidate projection
//   - empty-query + nil-receiver error paths
//   - Build returns a Descriptor that satisfies api.DescriptorProviders
//   - Build with nil Logger falls back to zap.NewNop (no panic)
//   - RegisterProviders propagates registry errors (ErrAlreadyRegistered
//     is the critical one a real composition root will hit if the
//     "script_assets" name is duplicated by another capability)
package scriptassets

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	appscriptassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scriptassets"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

// stubProviderRegistrar captures Register calls without leaking
// through providers.Registry. Simplifies the test surface — we don't
// need to construct a real Registry just to assert that
// RegisterProviders correctly forwards the *appscriptassets.ScriptAssetsProvider.
type stubProviderRegistrar struct {
	called bool
	got    providers.Provider
	err    error
}

func (s *stubProviderRegistrar) Register(p providers.Provider) error {
	s.called = true
	s.got = p
	return s.err
}

// nilLogger satisfies the provider's minimal Logger contract without
// pulling in zap. Lets tests run with no logger dependency.
type nilLogger struct{}

func (nilLogger) Info(string, ...any) {}
func (nilLogger) Warn(string, ...any) {}

// ── Provider identity ────────────────────────────────────────────

func TestScriptAssetsProvider_Name(t *testing.T) {
	p := appscriptassets.NewScriptAssetsProvider(nilLogger{})
	if got, want := p.Name(), "script_assets"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestScriptAssetsProvider_Capabilities(t *testing.T) {
	p := appscriptassets.NewScriptAssetsProvider(nilLogger{})
	caps := p.Capabilities()
	if len(caps) != 2 {
		t.Fatalf("Capabilities() returned %d caps, want 2", len(caps))
	}
	if caps[0] != providers.CapabilitySearch {
		t.Fatalf("Capabilities()[0] = %q, want %q (Search must come first)", caps[0], providers.CapabilitySearch)
	}
	if caps[1] != providers.CapabilityScript {
		t.Fatalf("Capabilities()[1] = %q, want %q", caps[1], providers.CapabilityScript)
	}
}

func TestScriptAssetsProvider_Capabilities_NoFetch(t *testing.T) {
	// CapabilityFetch must NOT be advertised — script-to-asset mapping
	// has no download stage; downstream media composition fetches the
	// resolved assets through their own providers.
	p := appscriptassets.NewScriptAssetsProvider(nilLogger{})
	for _, c := range p.Capabilities() {
		if c == providers.CapabilityFetch {
			t.Fatalf("Capabilities() unexpectedly includes CapabilityFetch — would break ByCapability lookups")
		}
	}
}

// ── Provider.Search behaviour ───────────────────────────────────

func TestScriptAssetsProvider_Search_ReturnsOneCandidatePerQuery(t *testing.T) {
	p := appscriptassets.NewScriptAssetsProvider(nilLogger{})
	res, err := p.Search(context.Background(), providers.SearchRequest{Query: "medieval castles"})
	if err != nil {
		t.Fatalf("Search returned unexpected error: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("Search returned %d candidates, want 1", len(res.Candidates))
	}
	if res.Candidates[0].SourceName != "script_assets" {
		t.Fatalf("candidate SourceName = %q, want %q", res.Candidates[0].SourceName, "script_assets")
	}
	if res.Candidates[0].SourceRef != "script_assets://medieval castles" {
		t.Fatalf("candidate SourceRef = %q, want %q", res.Candidates[0].SourceRef, "script_assets://medieval castles")
	}
	if res.NextPageToken != "" {
		t.Fatalf("NextPageToken = %q, want empty", res.NextPageToken)
	}
}

func TestScriptAssetsProvider_Search_EmptyQueryRejected(t *testing.T) {
	p := appscriptassets.NewScriptAssetsProvider(nilLogger{})
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: ""})
	if err == nil {
		t.Fatal("Search with empty query returned no error — must reject")
	}
}

func TestScriptAssetsProvider_Search_NilReceiverRejected(t *testing.T) {
	var p *appscriptassets.ScriptAssetsProvider
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: "topic"})
	if err == nil {
		t.Fatal("nil receiver Search returned no error — must reject")
	}
}

// ── Build() returns a valid Descriptor ─────────────────────────

func TestBuild_NilLogger_FallsBackToNoop(t *testing.T) {
	// No panic, no nil-deref. The fallback uses zap.NewNop() so
	// ScriptAssetsProvider's Info/Warn calls are safe no-ops.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Build with nil Logger panicked: %v", r)
		}
	}()
	desc, err := Build(Dependencies{Logger: nil})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if desc == nil {
		t.Fatal("Build returned nil descriptor")
	}
}

func TestBuild_PopulatesAllDescriptorFields(t *testing.T) {
	d, err := Build(Dependencies{})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("Build returned nil descriptor")
	}
	if d.Service == nil {
		t.Fatal("desd.Service is nil")
	}
	if d.Service.Provider() == nil {
		t.Fatal("Service.Provider() returned nil")
	}
	if d.Provider == nil {
		t.Fatal("desd.Provider is nil")
	}
	if d.Provider.Name() != "script_assets" {
		t.Fatalf("Provider.Name() = %q, want %q", d.Provider.Name(), "script_assets")
	}
}

// ── DescriptorProviders slot: RegisterProviders behaviour ─────

func TestRegisterProviders_CallsRegistrarWithDescriptorProvider(t *testing.T) {
	d, err := Build(Dependencies{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	stub := &stubProviderRegistrar{}
	if err := d.RegisterProviders(stub); err != nil {
		t.Fatalf("RegisterProviders returned unexpected error: %v", err)
	}
	if !stub.called {
		t.Fatal("RegisterProviders did not call registrar.Register")
	}
	if stub.got == nil {
		t.Fatal("RegisterProviders forwarded a nil provider")
	}
	if stub.got.Name() != "script_assets" {
		t.Fatalf("Registrar received provider with Name %q, want %q", stub.got.Name(), "script_assets")
	}
}

func TestRegisterProviders_PropagatesRegistryErrors(t *testing.T) {
	d, err := Build(Dependencies{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	stub := &stubProviderRegistrar{err: providers.ErrAlreadyRegistered}
	if err := d.RegisterProviders(stub); !errors.Is(err, providers.ErrAlreadyRegistered) {
		t.Fatalf("RegisterProviders err = %v, want chain including ErrAlreadyRegistered", err)
	}
}

func TestRegisterProviders_NilRegistrar_Rejected(t *testing.T) {
	d, err := Build(Dependencies{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := d.RegisterProviders(nil); err == nil {
		t.Fatal("RegisterProviders with nil registrar returned nil error — must reject")
	}
}

// Compile-time interface assertions (defense in depth — surface
// interface drift at build time, not at first composition). The api
// package owns the canonical ProviderRegistrar + DescriptorProviders
// interfaces; matching against the canonical types here proves the
// ScriptAssetsDescriptor satisfies the slot the composition root will
// type-assert on.
var (
	_ providers.SearchProvider = (*appscriptassets.ScriptAssetsProvider)(nil)
	_ api.DescriptorProviders  = (*ScriptAssetsDescriptor)(nil)
)
