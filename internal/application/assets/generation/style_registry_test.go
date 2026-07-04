package generation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testYAML writes a minimal styles YAML to a temp file and returns the path.
func testYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test YAML: %v", err)
	}
	return path
}

// ── Step-1 (A1) audit pin ───────────────────────────────────────────────
//
// TestResolve_ProviderUnsupported and TestResolve_ModelUnsupported
// were retired earlier in surface-3 (July 2026) along with the
// underlying checks. The empty-fields path
// (TestResolve_EmptyProviderAndModel) below still exercises Resolve
// with empty inputs to confirm the function entry is reachable. The
// t.Skip stubs at the bottom of this file keep the surface-3
// retirement grep-able for downstream audits (godlike/06 audit-pin).

func TestResolve_EmptyStyleID(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	// Step-1 (A1, July 2026): provider/model inputs are still
	// passed for call-shape compat, but the resolver no longer
	// reads them (the per-style allowlist checks were retired in
	// surface-3, A1 drops the remaining surface-1 references).
	resolved, err := reg.Resolve("", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("empty styleID should not error: %v", err)
	}
	if resolved.ID != "" || resolved.Version != 0 || resolved.PromptSuffix != "" {
		t.Fatalf("empty styleID should produce zero ResolvedStyle: got %+v", resolved)
	}
}

func TestResolve_ValidStyle(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): fixture uses
	// `test-style` (no magic literal reuse). Description fall-back
	// is gone — prompt_suffix is the sole resolved suffix.
	yaml := `
styles:
  - name: "test-style"
    version: 2
    prompt_suffix: "test suffix, dramatic lighting"
    negative_prompt: "blurry, low quality"
    destination_key: "ai-images/test-style"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("test-style", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("valid style should resolve: %v", err)
	}
	if resolved.ID != "test-style" {
		t.Fatalf("ID = %q, want %q", resolved.ID, "test-style")
	}
	if resolved.Version != 2 {
		t.Fatalf("Version = %d, want 2", resolved.Version)
	}
	if resolved.PromptSuffix != "test suffix, dramatic lighting" {
		t.Fatalf("PromptSuffix = %q", resolved.PromptSuffix)
	}
	if resolved.NegativePrompt != "blurry, low quality" {
		t.Fatalf("NegativePrompt = %q", resolved.NegativePrompt)
	}
	if resolved.DestinationKey != "ai-images/test-style" {
		t.Fatalf("DestinationKey = %q", resolved.DestinationKey)
	}
}

func TestResolve_StyleNotFound(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("nonexistent", "google-slides", "")
	if !errors.Is(err, ErrStyleNotFound) {
		t.Fatalf("expected ErrStyleNotFound, got: %v", err)
	}
}

func TestResolve_DisabledStyle(t *testing.T) {
	yaml := `
styles:
  - name: "test-style"
    enabled: false
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("test-style", "google-slides", "")
	if !errors.Is(err, ErrStyleDisabled) {
		t.Fatalf("expected ErrStyleDisabled, got: %v", err)
	}
}

func TestResolve_EmptyProviderAndModel(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): allowed_providers /
	// allowed_models fields dropped from StyleDefinition. The
	// underlying checks were already retired in surface-3; this cut
	// simply drops the now-no-op fixture fields.
	yaml := `
styles:
  - name: "test-style"
    prompt_suffix: "test"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// Empty provider and model should still resolve cleanly
	// (post surface-3 the allowlist checks are dead so this is a
	// pure passthrough to the ResolvedStyle population step).
	resolved, err := reg.Resolve("test-style", "", "")
	if err != nil {
		t.Fatalf("empty provider/model should resolve: %v", err)
	}
	if resolved.ID != "test-style" {
		t.Fatalf("ID = %q", resolved.ID)
	}
}

func TestResolve_DestinationKeyFallback(t *testing.T) {
	yaml := `
styles:
  - name: "test-style"
    prompt_suffix: "test"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("test-style", "", "")
	if err != nil {
		t.Fatalf("should resolve: %v", err)
	}
	if resolved.DestinationKey != "ai-images/test-style" {
		t.Fatalf("DestinationKey fallback = %q, want %q", resolved.DestinationKey, "ai-images/test-style")
	}
}

func TestApplyStyle_Deprecated(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): Description fall-back
	// is gone — ApplyStyle now uses PromptSuffix directly. The
	// Description field on this fixture is intentionally absent
	// (it would error under the new YAML schema); we exercise
	// PromptSuffix over a would-be legacy field via prompt-only.
	yaml := `
styles:
  - name: "apply-test"
    prompt_suffix: "new suffix"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// ApplyStyle should use PromptSuffix (sole resolved suffix).
	result := reg.ApplyStyle("my prompt", "apply-test")
	if result != "my prompt, new suffix" {
		t.Fatalf("ApplyStyle = %q, want %q", result, "my prompt, new suffix")
	}

	// Unknown style should return unchanged prompt (legacy silent fallback).
	result = reg.ApplyStyle("my prompt", "unknown")
	if result != "my prompt" {
		t.Fatalf("ApplyStyle unknown = %q, want unchanged", result)
	}
}

func TestListEnabled(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): silent flip absent
	// → false. c must specify `enabled: true` explicitly (was
	// previously absent-and-defaults-true under the tri-state
	// pointer). The fixture exercises the new contract.
	yaml := `
styles:
  - name: "a"
    prompt_suffix: "a test suffix"
    enabled: true
  - name: "b"
    prompt_suffix: "b test suffix"
    enabled: false
  - name: "c"
    prompt_suffix: "c test suffix"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	all := reg.List()
	if len(all) != 3 {
		t.Fatalf("List = %d, want 3", len(all))
	}

	enabled := reg.ListEnabled()
	if len(enabled) != 2 {
		t.Fatalf("ListEnabled = %d, want 2 (a + c)", len(enabled))
	}
}

// ── Surface-3 (July 2026) sentinel retirement stubs ──────────────────────
// TestResolve_ProviderUnsupported_Retired and
// TestResolve_ModelUnsupported_Retired are t.Skip stubs that document
// the surface-3 retirement of the underlying negative-path tests
// (TestResolve_ProviderUnsupported + TestResolve_ModelUnsupported).
// The stubs are NOT counted as failing tests — they fail-closed via
// t.Skip — and they keep the retirement grep-able for downstream
// audits (audit-pinning discipline per godlike/06).

func TestResolve_ProviderUnsupported_Retired(t *testing.T) {
	t.Skip("surface-3 (July 2026): per-style AllowedProviders check retired; ErrStyleProviderUnsupported never raised. See resolver.go doc-comment + sentinel non-nil contract TestStyleResolver_AllSentinelErrorsNonNil.")
}

func TestResolve_ModelUnsupported_Retired(t *testing.T) {
	t.Skip("surface-3 (July 2026): per-style AllowedModels check retired; ErrStyleModelUnsupported never raised. See resolver.go doc-comment + sentinel non-nil contract TestStyleResolver_AllSentinelErrorsNonNil.")
}
