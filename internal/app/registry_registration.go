// Package app — composition-time registration helpers (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. These helpers are the fail-fast
// guard surface for every registerX function in WireRegistry. The
// composition root delegates register calls to these helpers so any
// duplicate registration, nil-module, or post-freeze attempt is
// surfaced as a hard error instead of silently degrading.
//
// PR 2 (June 2026 — codex/registry-strict-uniqueness) invariants:
//   - nil module → explicit error (was NPE before PR 2).
//   - empty module name → explicit error ("module name is empty").
//   - post-freeze → existing sentinel ("registry is frozen").
//   - same instance, same name → silent no-op (composition-time
//     idempotency; PR 2 contract pinned by
//     TestRegisterSameInstanceMultipleSlots_NoError).
//   - different instance, same name → explicit error ("already
//     registered").
//
// The "compose:" error prefix + 3-field structured context are pinned
// by failing tests in internal/app/registry_failfast_test.go —
// do not change without updating the test marker.
package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"go.uber.org/zap"
)

// strictOption is the composition-site metadata tag passed to
// tryRegisterModuleStrict via WithRegistrationPoint. The tag is
// surfaced in error messages so an operator can pin the exact
// WireRegistry block responsible for a duplicate/register/freeze
// failure. Composition sites that omit the tag default to "unknown".
type strictOption func(*strictRegCtx)

type strictRegCtx struct {
	point string
}

// WithRegistrationPoint tags the next tryRegisterModuleStrict call with
// the composition site in WireRegistry that issued it (e.g.,
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
// It is the ONLY composition-time helper for publishing a Module into
// the api.Registry; the previous permissive tryRegisterModule was
// deleted in PR 1 (commit 81e79728) so duplicate module publication
// surfaces as a hard error instead of silently dropping the duplicate
// on a Debug log.
//
// Cross-slot publication (DescriptorJobs / DescriptorProviders
// publishing the same capability name through a shared Descriptor)
// registers to DISTINCT registries (module.Registry vs Jobs.Service
// vs providers.Registry), so the strict path is safe.
//
// PR 2 (June 2026 — codex/registry-strict-uniqueness) invariant set:
//   - nil module → explicit error (was NPE before PR 2).
//   - empty module name → explicit error ("module name is empty").
//   - post-freeze → existing sentinel ("registry is frozen").
//   - same instance, same name → silent no-op (composition-time
//     idempotency; PR 2 contract pinned by
//     TestRegisterSameInstanceMultipleSlots_NoError).
//   - different instance, same name → explicit error ("already registered").
//
// The composed error carries three composition-level fields required by
// the branch spec ("Inserire nel messaggio: nome capability; tipo
// descriptor; punto di registrazione"):
//
//	compose: capability=%q, descriptor-type=%T, registration-point=%s: <inner>
//
// The "compose:" prefix is pinned by
// TestTryRegisterModule_ErrorContainsSpecMarker in
// internal/app/registry_failfast_test.go; do not change without updating
// the test marker.
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
// reason this is now a thin one-line passthrough (per the test's
// own spec marker "After the composition-fix (June 2026)...\u0022).
func tryRegisterModule(registry *module.Registry, log *zap.Logger, mod module.Module) error {
	return tryRegisterModuleStrict(registry, log, mod)
}
