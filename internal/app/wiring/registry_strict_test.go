// Package app — registry_strict_test.go: PR 2 (June 2026 —
// codex/registry-strict-uniqueness) invariant tests.
//
// Pins the strict-uniqueness contract on tryRegisterModuleStrict:
//  1. Empty module name → explicit error with "compose:" prefix.
//  2. Different instance + same name → explicit error ("already registered")
//     with composition-level metadata (capability + descriptor-type +
//     registration-point).
//  3. Same instance + same name → silent no-op (composition-time idempotency;
//     Module may be re-published across composition sites without error).
//  4. Post-Freeze registration → explicit error with "compose:" prefix.
//  5. Enriched error includes the WithRegistrationPoint tag verbatim.
//  6. Enriched error includes the descriptor type (fmt.Sprintf("%T", mod)).
//
// Reuses the `fakeModule` declared in registry_failfast_test.go (same
// package app). Each test uses a unique module name to avoid collisions
// when both files run side-by-side via `go test ./internal/app/...`.
package wiring

import (
	"testing"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/stretchr/testify/require"
)

func TestRegisterEmptyName_ReturnsError(t *testing.T) {
	reg := module.NewRegistry()
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: ""})
	require.Error(t, err, "empty module name must be rejected up front")
	require.Contains(t, err.Error(), "compose:",
		"wrapped error must carry the compose: prefix (pinned by TestTryRegisterModule_ErrorContainsSpecMarker)")
	require.Contains(t, err.Error(), "empty",
		"error must mention the empty-name sentinel")
}

func TestRegisterDifferentDescriptorsSameName_ReturnsError(t *testing.T) {
	reg := module.NewRegistry()
	a := &fakeModule{name: "fixture-shared"}
	b := &fakeModule{name: "fixture-shared"}
	require.NoError(t, tryRegisterModuleStrict(reg, nil, a),
		"first register must succeed")
	err := tryRegisterModuleStrict(reg, nil, b)
	require.Error(t, err, "different instance with same name must fail")
	require.Contains(t, err.Error(), "compose:",
		"wrapped error must carry compose: prefix")
	require.Contains(t, err.Error(), "already registered",
		"inner sentinel must survive the %w wrap")
	require.Contains(t, err.Error(), "fixture-shared",
		"capability name must appear in the wrapped error")
}

func TestRegisterSameInstanceMultipleSlots_NoError(t *testing.T) {
	reg := module.NewRegistry()
	a := &fakeModule{name: "fixture-republish"}
	require.NoError(t, tryRegisterModuleStrict(reg, nil, a),
		"first register must succeed")
	// PR 2 contract: same instance + same name is a SILENT no-op so the
	// same Module pointer can be re-published across composition sites
	// (Register + PublishSlots) without surfacing as an error.
	require.NoError(t, tryRegisterModuleStrict(reg, nil, a),
		"same instance + same name must be a no-op (PR 2 invariant)")
	// Verify Find surfaces the SAME pointer (single registration, not
	// duplicated).
	m, ok := reg.Find("fixture-republish")
	require.True(t, ok, "Find must surface the registered module")
	require.Same(t, a, m,
		"registered module pointer must be the original (single entry)")
}

func TestRegisterPostFreeze_ReturnsError(t *testing.T) {
	reg := module.NewRegistry()
	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-pre-freeze"}))
	reg.Freeze()
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-post-freeze"})
	require.Error(t, err, "register after Freeze must fail")
	require.Contains(t, err.Error(), "compose:",
		"wrapped error must carry compose: prefix")
	require.Contains(t, err.Error(), "frozen",
		"inner sentinel must survive the %w wrap")
	require.Contains(t, err.Error(), "fixture-post-freeze",
		"capability name must appear in the wrapped error")
}

func TestRegisterErrorIncludesRegistrationPoint(t *testing.T) {
	reg := module.NewRegistry()
	require.NoError(t, tryRegisterModuleStrict(reg, nil,
		&fakeModule{name: "fixture-pt"},
		WithRegistrationPoint("test.point")))
	err := tryRegisterModuleStrict(reg, nil,
		&fakeModule{name: "fixture-pt"},
		WithRegistrationPoint("test.point"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "test.point",
		"wrapped error must include the WithRegistrationPoint tag verbatim")
	require.Contains(t, err.Error(), "compose:",
		"compose: prefix must remain")
}

func TestRegisterErrorIncludesDescriptorType(t *testing.T) {
	reg := module.NewRegistry()
	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-type"}))
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-type"})
	require.Error(t, err)
	// fmt.Sprintf("%T", &fakeModule{...}) returns "*app.fakeModule"
	// because fakeModule is declared in package app.
	require.Contains(t, err.Error(), "*app.fakeModule",
		"wrapped error must include the descriptor type (fmt.Sprintf %%T form)")
	require.Contains(t, err.Error(), "compose:",
		"compose: prefix must remain")
}

func TestRegisterErrorDefaultPointIsUnknownWhenUntagged(t *testing.T) {
	// Backward-compat / contract check: existing WireRegistry call sites
	// that pre-date the strict wrapper pass 0 opts (3 positional args
	// after registry/register). The wrapper must compile in that shape
	// AND surface "unknown" as the registration-point default so an
	// operator can spot an untagged caller from the error text alone.
	reg := module.NewRegistry()
	require.NoError(t, tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-untagged"}))
	err := tryRegisterModuleStrict(reg, nil, &fakeModule{name: "fixture-untagged"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown",
		"untagged call sites must surface 'unknown' as the default registration-point")
	require.Contains(t, err.Error(), "compose:",
		"compose: prefix must remain")
}
