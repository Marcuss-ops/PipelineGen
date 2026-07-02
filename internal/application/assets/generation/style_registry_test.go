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

// ── surface-3 (July 2026) audit pin ──────────────────────────────────────
//
// TestResolve_ProviderUnsupported and TestResolve_ModelUnsupported
// were retired along with the underlying checks in
// styles.StyleRegistry.Resolve (see resolver.go doc-comment for the
// canonical rationale). The empty-fields path
// (TestResolve_EmptyProviderAndModel) below still exercises Resolve
// with empty inputs to confirm the function entry is reachable. The
// t.Skip stubs at the bottom of this file keep the surface-3
// retirement grep-able for downstream audits.

func TestResolve_EmptyStyleID(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	// surface-3 (July 2026): provider/model use canonical values.
	// The fields are irrelevant for the empty-styleID branch (no-op
	// default returns immediately) — the values stay canonical for
	// consistency with the rest of the suite.
	resolved, err := reg.Resolve("", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("empty styleID should not error: %v", err)
	}
	if resolved.ID != "" || resolved.Version != 0 || resolved.PromptSuffix != "" {
		t.Fatalf("empty styleID should produce zero ResolvedStyle: got %+v", resolved)
	}
}

func TestResolve_ValidStyle(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    version: 2
    prompt_suffix: "movie still, dramatic lighting"
    negative_prompt: "blurry, low quality"
    destination_key: "ai-images/cinematic"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("cinematic", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("valid style should resolve: %v", err)
	}
	if resolved.ID != "cinematic" {
		t.Fatalf("ID = %q, want %q", resolved.ID, "cinematic")
	}
	if resolved.Version != 2 {
		t.Fatalf("Version = %d, want 2", resolved.Version)
	}
	if resolved.PromptSuffix != "movie still, dramatic lighting" {
		t.Fatalf("PromptSuffix = %q", resolved.PromptSuffix)
	}
	if resolved.NegativePrompt != "blurry, low quality" {
		t.Fatalf("NegativePrompt = %q", resolved.NegativePrompt)
	}
	if resolved.DestinationKey != "ai-images/cinematic" {
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
  - name: "cinematic"
    enabled: false
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("cinematic", "google-slides", "")
	if !errors.Is(err, ErrStyleDisabled) {
		t.Fatalf("expected ErrStyleDisabled, got: %v", err)
	}
}

func TestResolve_EmptyProviderAndModel(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    prompt_suffix: "test"
    allowed_providers: ["google-slides"]
    allowed_models: ["nano-banana-pro"]
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// Empty provider and model should still resolve cleanly
	// (post surface-3 the allowlist checks are dead so this is a
	// pure passthrough to the ResolvedStyle population step; pre-cut
	// the empty-string short-circuit was already in place since the
	// checks were conditional on non-empty inputs).
	resolved, err := reg.Resolve("cinematic", "", "")
	if err != nil {
		t.Fatalf("empty provider/model should resolve: %v", err)
	}
	if resolved.ID != "cinematic" {
		t.Fatalf("ID = %q", resolved.ID)
	}
}

func TestResolve_DestinationKeyFallback(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    prompt_suffix: "test"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("cinematic", "", "")
	if err != nil {
		t.Fatalf("should resolve: %v", err)
	}
	if resolved.DestinationKey != "ai-images/cinematic" {
		t.Fatalf("DestinationKey fallback = %q, want %q", resolved.DestinationKey, "ai-images/cinematic")
	}
}

func TestResolve_EffectiveSuffixFallback_Description(t *testing.T) {
	// When prompt_suffix is absent, EffectiveSuffix falls back to description.
	yaml := `
styles:
  - name: "legacy"
    description: "just a description string"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("legacy", "", "")
	if err != nil {
		t.Fatalf("should resolve: %v", err)
	}
	if resolved.PromptSuffix != "just a description string" {
		t.Fatalf("PromptSuffix = %q, want legacy description", resolved.PromptSuffix)
	}
}

func TestApplyStyle_Deprecated(t *testing.T) {
	yaml := `
styles:
  - name: "test"
    description: "legacy desc"
    prompt_suffix: "new suffix"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// ApplyStyle should use EffectiveSuffix (prompt_suffix over description).
	result := reg.ApplyStyle("my prompt", "test")
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
	yaml := `
styles:
  - name: "a"
    description: "a"
    enabled: true
  - name: "b"
    description: "b"
    enabled: false
  - name: "c"
    description: "c"
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
		t.Fatalf("ListEnabled = %d, want 2 (a + c, omitted enabled defaults true)", len(enabled))
	}
}

// ── surface-3 (July 2026) sentinel retirement stubs ──────────────────────
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
