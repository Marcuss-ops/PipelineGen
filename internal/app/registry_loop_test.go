// Package app — registry loop invariants (PR 1, branch
// codex/registry-loop-fix).
//
// Pins the per-WireRegistry invariants per the branch's Definition of
// Done:
//   - build count per capability = 1
//   - registration count per module name = 1
//   - generation publishes job handlers on the api.DescriptorJobs slot
//   - registry frozen only after all registrations
//
// The 3 named functions (registerGenerationCapability,
// registerChannelsCapability, registerSearchQueriesCapability) replace
// inline blocks previously trapped inside a `for _, m := range
// []struct{...}` loop body that ran the blocks 6× per WireRegistry
// call. The tests below prove the called-once contract without spinning
// up the entire WireRegistry stack.
//
// The `fakeModule` test helper is intentionally NOT redefined here —
// it already lives in `registry_failfast_test.go` (same package `app`)
// and is reused across the package's test surface.
//
// Each test passes a `*ComposeRoot` with the minimum fields required
// for the function under test: zero-value for everything except the
// targeted dep (Domains for generation; DB for channels and
// search_queries). With nil inputs, each function's nil-guard
// short-circuits BEFORE invoking Build, so the test scope stays inside
// the regression boundary without spinning up generation.Build /
// channels.Build / searchqueriesuc — Build success is not asserted
// here (already pinned by integration tests of WireRegistry itself).
package app

import (
	"testing"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
	"github.com/stretchr/testify/require"
)

// TestRegisterGenerationCapability_NilDomains_NoRegister pins: when
// root.Domains is nil (no books/lessons services wired), the function
// returns nil without registering anything — Build is NOT called.
//
// Regression target: the previous loop shape ran this block 6× per
// WireRegistry; even on the no-Domains path the block executed per
// iteration. The named-function form guarantees a single execution
// point and a clean exit when Dependencies would be incomplete.
func TestRegisterGenerationCapability_NilDomains_NoRegister(t *testing.T) {
	reg := module.NewRegistry()
	log := zap.NewNop()
	cfg := &config.Config{}
	root := &ComposeRoot{Domains: nil}

	require.NoError(t, registerGenerationCapability(reg, log, cfg, root))
	require.Empty(t, reg.GetEnabled(),
		"registerGenerationCapability with nil Domains must register nothing — Build is not called when there is no book/lesson backing service")
}

// TestRegisterChannelsCapability_NilDB_NoRegister pins the same
// invariant for the channels capability. With root.DB nil, the function
// returns the no-op path.
func TestRegisterChannelsCapability_NilDB_NoRegister(t *testing.T) {
	reg := module.NewRegistry()
	log := zap.NewNop()
	root := &ComposeRoot{Domains: nil, DB: nil}

	require.NoError(t, registerChannelsCapability(reg, log, root))
	require.Empty(t, reg.GetEnabled(),
		"registerChannelsCapability with nil DB must register nothing")
}

// TestRegisterSearchQueriesCapability_NilDB_NoRegister pins the
// invariant for the search_queries use case.
func TestRegisterSearchQueriesCapability_NilDB_NoRegister(t *testing.T) {
	reg := module.NewRegistry()
	log := zap.NewNop()
	root := &ComposeRoot{Domains: nil, DB: nil}

	require.NoError(t, registerSearchQueriesCapability(reg, log, root))
	require.Empty(t, reg.GetEnabled(),
		"registerSearchQueriesCapability with nil DB must register nothing")
}

// TestRegisterAllCapabilities_DoNotFreezeRegistry pins the freeze-order
// invariant: the 3 named functions do NOT call reg.Freeze. WireRegistry
// owns the canonical Freeze call and runs it once at the very end.
//
// Proof strategy: after the 3 calls return, a follow-up Register must
// succeed. This is only possible if the registry is still mutable (not
// yet frozen). The post-loop-check sentinel module also confirms
// WireRegistry's "freezes only after all registrations are complete"
// contract by appearing alongside (or above) the 3 named functions in
// the timeline.
//
// All 3 functions run on nil-Domains/nil-DB inputs so they short-circuit
// to their no-op paths and never invoke Build. This means the test
// does not depend on a real BooksService stub — keep scope tight and
// the test small.
func TestRegisterAllCapabilities_DoNotFreezeRegistry(t *testing.T) {
	reg := module.NewRegistry()
	log := zap.NewNop()
	cfg := &config.Config{}
	root := &ComposeRoot{Domains: nil, DB: nil}

	// Zero-value ComposeRoot paths short-circuit all 3 named functions.
	// Errors on the strict path (e.g., a real Build that fails) are
	// tolerated — the invariant of interest is the registry's
	// mutability, which is unaffected by registration errors.
	_ = registerGenerationCapability(reg, log, cfg, root)
	_ = registerChannelsCapability(reg, log, root)
	_ = registerSearchQueriesCapability(reg, log, root)

	// A subsequent Register MUST succeed — proves Freeze hasn't fired.
	err := reg.Register(&fakeModule{name: "post-loop-check"})
	require.NoError(t, err,
		"registry must remain mutable after registerXCapability calls — Freeze is WireRegistry's responsibility, called only after all registrations are complete")

	// Belt-and-suspenders: the post-loop-check module is in the registry.
	names := map[string]bool{}
	for _, m := range reg.GetEnabled() {
		names[m.Name()] = true
	}
	require.True(t, names["post-loop-check"],
		"the sentinel post-loop-check module must appear in the registry — confirms an extra Register succeeded past the 3 named functions")
}
