// Package app — Registries-and-SSOT fail-fast regression test.
//
// Spec "Registries and Single Source of Truth" (§"Uniqueness") requires
// composition to fail on duplicate module names. The previous
// registerModule helper logged Warn and continued; this file pins
// the new tryRegisterModule behaviour.
//
// Coverage:
//  1. TestTryRegisterModule_DuplicateFails — same name → error.
//  2. TestTryRegisterModule_FreezeFails    — frozen registry → error.
//  3. TestTryRegisterModule_DistinctOK     — distinct names → no error.
//  4. TestTryRegisterModule_ErrorContainsSpecMarker — error text
//     references the spec §"Uniqueness" rule so a future log-msg-only
//     regression still fails the gate.
package wiring

import (
	"strings"
	"testing"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeModule is a minimal module.Module implementation for the
// duplicate-detection regression test. It does NOT depend on any
// feature flag or origin — just satisfies the 3-method interface
// so we can drive the registry bounds.
type fakeModule struct{ name string }

func (f *fakeModule) Name() string                    { return f.name }
func (f *fakeModule) Enabled() bool                   { return true }
func (f *fakeModule) RegisterRoutes(*gin.RouterGroup) {}

func TestTryRegisterModule_DuplicateFails(t *testing.T) {
	reg := module.NewRegistry()

	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-dup"}),
		"first register must succeed")
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-dup"})
	require.Error(t, err, "second register with same name must fail")
	require.Contains(t, err.Error(), "already registered",
		"error text must mention the duplicate-detection sentinel")
}

func TestTryRegisterModule_FreezeFails(t *testing.T) {
	reg := module.NewRegistry()

	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-pre-freeze"}))
	reg.Freeze()

	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-post-freeze"})
	require.Error(t, err, "register after Freeze must fail")
}

func TestTryRegisterModule_DistinctOK(t *testing.T) {
	reg := module.NewRegistry()

	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-a"}))
	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-b"}))
	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-c"}))
}

func TestTryRegisterModule_ErrorContainsSpecMarker(t *testing.T) {
	// The wrapped error carries the composition prefix ("compose:")
	// plus the inner sentinel from module.Registry. Both halves
	// matter for diagnostics — if a future refactor drops the
	// "compose:" prefix the wrap-shareability degrades silently.
	reg := module.NewRegistry()

	_ = tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-marker"})
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-marker"})
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "compose:"),
		"wrapped error must start with compose: prefix (got %q)", err.Error())
	require.Contains(t, err.Error(), "fixture-marker",
		"wrapped error must reference the offending module name")
}

// TestTryRegisterModule_ProductionPathFailsOnDuplicate verifies that
// the production tryRegisterModuleStrict fails fast on duplicate names.
// After PR 1 (commit 81e79728) the permissive tryRegisterModule was
// deleted; tryRegisterModuleStrict is now the ONLY registration path.
func TestTryRegisterModule_ProductionPathFailsOnDuplicate(t *testing.T) {
	reg := module.NewRegistry()

	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "prod-dup"}),
		"first registration must succeed")
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "prod-dup"})
	require.Error(t, err, "production path must fail on duplicate name")
	require.Contains(t, err.Error(), "already registered")
}
