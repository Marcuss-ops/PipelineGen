// Package app — central Register composition point (Blocco C1-Step 2).
//
// This file is the canonical single composition point that routes
// Module / Provider / (forward) Job-handler registrations into the
// three PipelineGen registries. Per Capability Standard
// (godlike/04 + AGENTS.md Pattern 0 + Wave C1):
//
//   - registerCapabilities is the ONLY function in the codebase
//     that takes *module.Registry, *providers.Registry
//     as parameters AND may mutate them via the .Register method.
//     (PR-AUDIT-7 removed *jobs.Registry — job handler binding
//     is via c3ValidateRuntimeGraph, not this composition point.)
//   - Its helpers (registerHTTPModules + registerProviders +
//     registerHTTPModules + the strict-uniqueness helpers
//     tryRegisterModuleStrict + tryRegisterModule, all relocated
//     from registry_registration.go in this PR) are the ONLY
//     functions in internal/app/** that may call any
//     registry .Register(...) method on the THREE canonical
//     registry types.
//
// Forward-protection: capability_registry_gate_test.go walks
// internal/app/**.go with ripgrep and FAILS the build if any
// production file outside capability_registry.go contains a
// typed-registry mutation call at the wrong site. Variable-name
// calls (e.g. `registry.Register(mod)`, `pr.RegisterSearch(adapter)`,
// `provReg.RegisterFetch(...)`) are gate-safe because the
// gate pattern requires the typed registry prefix that is
// only present as a parameter-type annotation, NOT inline
// at the call site.
//
// The file is named capability_registry.go (per the Blocco
// C1-Step 2 mandate) and the function is named
// registerCapabilities (lowercase, package-private — only
// the composition root in registry.go calls it).
package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"

	"go.uber.org/zap"
)

// ── Public surface (only registerCapabilities is exposed cross-file) ──

// CapabilityDeps groups every artefact that registerCapabilities
// routes into the canonical registries. Per-family typed slice so a
// future registration family (BackgroundJob, MetricsCollector)
// adds a slice field, never a new direct .Register call outside
// this file.
//
// Today two families are wired:
//
//   - HTTPModules: route-only api.Module values published via
//     the strict-uniqueness gate (tryRegisterModuleStrict).
//   - Providers: asset-search + asset-fetch adapters published
//     into providers.Registry via its Register* methods.
//
// PR-AUDIT-7 (July 2026): the Jobs slice and ForwardJobHandlerEntry
// were removed — job handler binding is the responsibility of
// c3ValidateRuntimeGraph in registry.go, which binds handlers via
// def.Type directly. The forward-only surface was never wired and
// served only as dead documentation.
type CapabilityDeps struct {
	HTTPModules []TrackedHTTPModule
	Providers   []TrackedProviderEntry
}

// TrackedHTTPModule couples a route Module with the registration-
// point tag (matches the strict-uniqueness WithRegistrationPoint
// convention). The Point is surfaced in error messages so an
// operator can pin the exact call site responsible for a
// duplicate/freeze failure.
type TrackedHTTPModule struct {
	Module module.Module
	Point  string
}

// TrackedProviderEntry identifies a single provider registration
// slot on the providers.Registry. The Kind discriminator picks the
// concrete Register* method because providers.Registry exposes
// Search and Fetch as separate methods (and we want fail-fast
// type-safety per slot — the FetchProvider adapter cannot satisfy
// SearchProvider and vice versa, so the discriminator is intentional,
// not optional).
type TrackedProviderEntry struct {
	Id     string
	Kind   ProviderKind
	Search providers.SearchProvider // used when Kind == ProviderKindSearch
	Fetch  providers.FetchProvider  // used when Kind == ProviderKindFetch
}

// ProviderKind identifies which Register* method on
// providers.Registry the entry routes into.
type ProviderKind int

const (
	// ProviderKindSearch routes to providers.Registry.RegisterSearch.
	ProviderKindSearch ProviderKind = iota
	// ProviderKindFetch routes to providers.Registry.RegisterFetch.
	ProviderKindFetch
)

// registerCapabilities is the canonical single composition point
// that mutates the three PipelineGen registries during composition.
//
// Returns the FIRST error encountered (composition-time fail-closed).
// Nil-safety:
//   - reg == nil ⇒ hard error (composition bug — callers should
//     error before this gate if the api.Registry is missing).
//   - provReg == nil ⇒ no-op for the Providers slice (skip).
//
// On success the api.Registry is left in its post-write state
// (caller-controlled freeze) and providers.Registry is Freeze()d
// — the Freeze is the canonical composition-time gate closing
// the write side and opening the runtime read side (Reviewer Q8
// invariant from Blocco C1 PR1).
func registerCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps) error {
	if reg == nil {
		return fmt.Errorf("registerCapabilities: nil api.Registry (composition bug)")
	}
	if err := registerHTTPModules(reg, deps.HTTPModules); err != nil {
		return fmt.Errorf("registerCapabilities: http-modules: %w", err)
	}
	if err := registerProviders(provReg, deps.Providers); err != nil {
		return fmt.Errorf("registerCapabilities: providers: %w", err)
	}
	return nil
}

// registerHTTPModules dispatches every TrackedHTTPModule through
// tryRegisterModuleStrict. The ONLY caller of this function is
// registerCapabilities, and tryRegisterModuleStrict is the ONLY
// caller of the api.Registry mutation surface in internal/app/**.
//
// Per-step registerX functions in registry_internal_modules.go +
// registry_public_modules.go + registry_assets.go + wire_script.go
// already call tryRegisterModuleStrict inline (their modules were
// registered during Steps 2–5 of WireRegistry before this canonical
// aggregator landed). Those callsites do NOT violate the gate
// because the typed registration call itself is only present
// inside tryRegisterModuleStrict's body, and that body lives in
// THIS file.
func registerHTTPModules(reg *module.Registry, mods []TrackedHTTPModule) error {
	for _, m := range mods {
		if m.Module == nil {
			continue
		}
		if err := tryRegisterModuleStrict(reg, zap.NewNop(), m.Module, WithRegistrationPoint(m.Point)); err != nil {
			return err
		}
	}
	return nil
}

// registerProviders dispatches every TrackedProviderEntry to its
// register method. The ONLY caller of this function is
// registerCapabilities, and this is the ONLY function in
// internal/app/** that calls the providers registry's search/fetch
// mutation methods (gate enforced by capability_registry_gate_test.go;
// the temporary variable-name-prefixed calls in registry_late_bindings.go
// were removed at the same time this function landed).
func registerProviders(provReg *providers.Registry, entries []TrackedProviderEntry) error {
	if provReg == nil {
		return nil
	}
	for _, e := range entries {
		if e.Id == "" {
			continue
		}
		switch e.Kind {
		case ProviderKindSearch:
			if e.Search == nil {
				continue
			}
			if err := provReg.RegisterSearch(e.Search); err != nil {
				return fmt.Errorf("registerProviders: search %q: %w", e.Id, err)
			}
		case ProviderKindFetch:
			if e.Fetch == nil {
				continue
			}
			if err := provReg.RegisterFetch(e.Fetch); err != nil {
				return fmt.Errorf("registerProviders: fetch %q: %w", e.Id, err)
			}
		}
	}
	// Freeze the providers registry as the absolute LAST mutation
	// (Reviewer Q8 invariant from the original Wave 14 close-out).
	// Idempotent: a second Freeze() is a no-op because
	// *providers.Registry guards its `frozen` bit. Putting the
	// Freeze here also means a single test can pin the "all
	// providers registered then frozen" sequencing in one place.
	provReg.Freeze()
	return nil
}

// ── Strict-uniqueness helpers (RELOCATED here from
//    registry_registration.go, deleted in this PR) ──────────────

// strictOption is the composition-site metadata tag passed to
// tryRegisterModuleStrict via WithRegistrationPoint. The tag is
// surfaced in error messages so an operator can pin the exact
// WireRegistry block responsible for a duplicate/register/freeze
// failure. Composition sites that omit the tag default to "unknown".
type strictOption func(*strictRegCtx)

type strictRegCtx struct {
	point string
}

// WithRegistrationPoint tags the next tryRegisterModuleStrict call
// with the composition site in WireRegistry that issued it (e.g.,
// "register.Generation", "register.Assets"). The tag is surfaced in
// error messages so an operator can pin the exact call site that
// emitted a duplicate or freeze failure. Composition sites that don't
// tag default to "unknown".
func WithRegistrationPoint(point string) strictOption {
	return func(c *strictRegCtx) {
		if point != "" {
			c.point = point
		}
	}
}

func collectRegPoint(opts []strictOption) string {
	var c strictRegCtx
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	if c.point == "" {
		return "unknown"
	}
	return c.point
}

// tryRegisterModuleStrict is the composition-time registration path.
// It is the ONLY composition-time helper in this file that calls
// the api.Registry mutation method (variable name `registry`).
//
// PR 2 (June 2026 — codex/registry-strict-uniqueness) invariant set:
//   - nil registry → explicit error ("compose: nil api.Registry ...").
//   - nil module → explicit error ("compose: nil module ...").
//   - nil module name → existing sentinel ("module name is empty").
//   - post-freeze → existing sentinel ("registry is frozen").
//   - same instance, same name → silent no-op (composition-time
//     idempotency pinned by TestRegisterSameInstanceMultipleSlots_NoError).
//   - different instance, same name → explicit error ("already registered").
//
// The composed error carries three composition-level fields required
// by the branch spec ("Inserire nel messaggio: nome capability; tipo
// descriptor; punto di registrazione"):
//
//	compose: capability=%q, descriptor-type=%T, registration-point=%s: <inner>
//
// The "compose:" prefix is pinned by
// TestTryRegisterModule_ErrorContainsSpecMarker in
// internal/app/registry_failfast_test.go; do not change without
// updating the test marker.
func tryRegisterModuleStrict(registry *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	if registry == nil {
		// Composition-bug guard: a nil registry is never expected at
		// composition time. Surface the bug here so WireRegistry fails
		// fast with a clear operator message.
		return fmt.Errorf("compose: nil api.Registry passed to strict-register (registration-point=%s)", collectRegPoint(opts))
	}
	if mod == nil {
		return fmt.Errorf("compose: nil module passed (registration-point=%s)", collectRegPoint(opts))
	}
	if err := registry.Register(mod); err != nil {
		if log != nil {
			log.Warn("strict-register failed",
				zap.String("module", mod.Name()),
				zap.String("registration-point", collectRegPoint(opts)),
				zap.Error(err))
		}
		// Pin "compose:" prefix; spec fields = capability + descriptor-type +
		// registration-point. Inner %w preserves the sentinel substrings
		// pinned by failfast tests ("already registered", "frozen",
		// "module name is empty").
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w",
			mod.Name(), mod, collectRegPoint(opts), err)
	}
	return nil
}

// tryRegisterModule is the production-path register helper retained
// for fail-fast regression coverage (registry_failfast_test.go tests
// "production path fails on duplicate"). Delegates to the strict
// variant so all register paths share one composition-time failure
// rule; the PR 1 (June 2026) coalescing Has-check deletion is the
// reason this is now a thin one-line passthrough.
func tryRegisterModule(registry *module.Registry, log *zap.Logger, mod module.Module) error {
	return tryRegisterModuleStrict(registry, log, mod)
}
