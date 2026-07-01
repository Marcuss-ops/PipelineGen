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

func TestResolve_EmptyStyleID(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("", "flux", "flux-1-dev")
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

	resolved, err := reg.Resolve("cinematic", "flux", "flux-1-dev")
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

	_, err = reg.Resolve("nonexistent", "flux", "")
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

	_, err = reg.Resolve("cinematic", "flux", "")
	if !errors.Is(err, ErrStyleDisabled) {
		t.Fatalf("expected ErrStyleDisabled, got: %v", err)
	}
}

func TestResolve_ProviderUnsupported(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    prompt_suffix: "test"
    allowed_providers: ["flux"]
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("cinematic", "google-slides", "")
	if !errors.Is(err, ErrStyleProviderUnsupported) {
		t.Fatalf("expected ErrStyleProviderUnsupported, got: %v", err)
	}

	// Same provider but different case should still succeed.
	resolved, err := reg.Resolve("cinematic", "Flux", "")
	if err != nil {
		t.Fatalf("Flux (case-insensitive) should resolve: %v", err)
	}
	if resolved.ID != "cinematic" {
		t.Fatalf("ID = %q", resolved.ID)
	}
}

func TestResolve_ModelUnsupported(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    prompt_suffix: "test"
    allowed_models: ["flux-1-dev"]
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("cinematic", "flux", "stable-diffusion-xl")
	if !errors.Is(err, ErrStyleModelUnsupported) {
		t.Fatalf("expected ErrStyleModelUnsupported, got: %v", err)
	}

	// Same model but different case should still succeed.
	resolved, err := reg.Resolve("cinematic", "flux", "FLUX-1-DEV")
	if err != nil {
		t.Fatalf("FLUX-1-DEV (case-insensitive) should resolve: %v", err)
	}
	if resolved.ID != "cinematic" {
		t.Fatalf("ID = %q", resolved.ID)
	}
}

func TestResolve_EmptyProviderAndModel(t *testing.T) {
	yaml := `
styles:
  - name: "cinematic"
    prompt_suffix: "test"
    allowed_providers: ["flux"]
    allowed_models: ["flux-1-dev"]
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// Empty provider and model should skip validation entirely.
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
