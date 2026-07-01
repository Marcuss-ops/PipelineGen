package asset

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

// ── EffectiveSuffix semantics ──────────────────────────────────────────

func TestGenerationStyle_EffectiveSuffix_PromptSuffixPreferred(t *testing.T) {
	s := GenerationStyle{
		Name:         "cinematic",
		Description:  "legacy desc",
		PromptSuffix: "modern suffix",
	}
	if got := s.EffectiveSuffix(); got != "modern suffix" {
		t.Fatalf("EffectiveSuffix = %q, want %q (PromptSuffix takes precedence)", got, "modern suffix")
	}
}

func TestGenerationStyle_EffectiveSuffix_DescriptionFallback(t *testing.T) {
	s := GenerationStyle{
		Name:        "legacy",
		Description: "just a description string",
	}
	if got := s.EffectiveSuffix(); got != "just a description string" {
		t.Fatalf("EffectiveSuffix = %q, want legacy description", got)
	}
}

// ── IsEnabled *bool semantics ──────────────────────────────────────────

func TestGenerationStyle_IsEnabled_AbsentIsTrue(t *testing.T) {
	// nil pointer (absent from YAML) -> enabled default.
	s := GenerationStyle{Name: "cinematic"}
	if !s.IsEnabled() {
		t.Fatal("absent Enabled should default to true")
	}
}

func TestGenerationStyle_IsEnabled_ExplicitTrue(t *testing.T) {
	tru := true
	s := GenerationStyle{Name: "cinematic", Enabled: &tru}
	if !s.IsEnabled() {
		t.Fatal("Enabled=true should report true")
	}
}

func TestGenerationStyle_IsEnabled_ExplicitFalse(t *testing.T) {
	fal := false
	s := GenerationStyle{Name: "cinematic", Enabled: &fal}
	if s.IsEnabled() {
		t.Fatal("Enabled=false should report false")
	}
}

// ── YAML round-trip — Enabled pointer + new fields preserve identity ──

func TestGenerationStyle_YAMLRoundTrip_NewShape(t *testing.T) {
	tru := true
	src := GenerationStyle{
		Name:           "cinematic",
		Description:    "legacy desc",
		Version:        3,
		DisplayName:    "Cinematic",
		PromptSuffix:   "movie still",
		NegativePrompt: "blurry",
		DefaultWidth:   1920,
		DefaultHeight:  1080,
		AllowedProviders: []string{"flux", "google-slides"},
		AllowedModels:    []string{"flux-1-dev"},
		Tags:             []string{"cinematic", "movie"},
		DestinationKey:   "ai-images/cinematic",
		Enabled:          &tru,
	}

	data, err := yaml.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var dst GenerationStyle
	if err := yaml.Unmarshal(data, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if dst.Name != src.Name {
		t.Errorf("Name = %q, want %q", dst.Name, src.Name)
	}
	if dst.Description != src.Description {
		t.Errorf("Description = %q, want %q", dst.Description, src.Description)
	}
	if dst.Version != src.Version {
		t.Errorf("Version = %d, want %d", dst.Version, src.Version)
	}
	if dst.DisplayName != src.DisplayName {
		t.Errorf("DisplayName = %q, want %q", dst.DisplayName, src.DisplayName)
	}
	if dst.PromptSuffix != src.PromptSuffix {
		t.Errorf("PromptSuffix = %q, want %q", dst.PromptSuffix, src.PromptSuffix)
	}
	if dst.NegativePrompt != src.NegativePrompt {
		t.Errorf("NegativePrompt = %q, want %q", dst.NegativePrompt, src.NegativePrompt)
	}
	if dst.DefaultWidth != src.DefaultWidth || dst.DefaultHeight != src.DefaultHeight {
		t.Errorf("Default dims = %dx%d, want %dx%d", dst.DefaultWidth, dst.DefaultHeight, src.DefaultWidth, src.DefaultHeight)
	}
	if dst.DestinationKey != src.DestinationKey {
		t.Errorf("DestinationKey = %q, want %q", dst.DestinationKey, src.DestinationKey)
	}
	if dst.Enabled == nil || !*dst.Enabled {
		t.Errorf("Enabled = %v, want *true", dst.Enabled)
	}
	// Allow *string slices may round-trip empty when nil, but for non-empty
	// slices we want them preserved.
	if len(dst.AllowedProviders) != 2 || dst.AllowedProviders[0] != "flux" {
		t.Errorf("AllowedProviders = %v, want [flux google-slides]", dst.AllowedProviders)
	}
	if len(dst.AllowedModels) != 1 || dst.AllowedModels[0] != "flux-1-dev" {
		t.Errorf("AllowedModels = %v, want [flux-1-dev]", dst.AllowedModels)
	}
}

//nolint:funlen // explicit YAML string + multi-field assertions
func TestGenerationStyle_YAMLRoundTrip_EnabledAbsentVsExplicitFalse(t *testing.T) {
	// Case 1: enabled: false explicitly.
	yamlFalse := `
name: cinematic
display_name: Cinematic
prompt_suffix: movie still
enabled: false
`
	var dstFalse GenerationStyle
	if err := yaml.Unmarshal([]byte(yamlFalse), &dstFalse); err != nil {
		t.Fatalf("unmarshal enabled=false: %v", err)
	}
	if dstFalse.Enabled == nil {
		t.Fatal("explicit false should produce non-nil Enabled")
	}
	if *dstFalse.Enabled {
		t.Fatal("Enabled should be false after round-trip")
	}
	// Absent case 2: omitempty drops Enabled entirely.
	yamlAbsent := `
name: cinematic
display_name: Cinematic
prompt_suffix: movie still
`
	var dstAbsent GenerationStyle
	if err := yaml.Unmarshal([]byte(yamlAbsent), &dstAbsent); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if dstAbsent.Enabled != nil {
		t.Fatal("absent enabled should round-trip as nil Enabled (omitempty)")
	}
	if !dstAbsent.IsEnabled() {
		t.Fatal("IsEnabled() on absent Enabled should be true (backward-compatible default)")
	}
	// Case 3: explicit true.
	yamlTrue := `
name: cinematic
display_name: Cinematic
prompt_suffix: movie still
enabled: true
`
	var dstTrue GenerationStyle
	if err := yaml.Unmarshal([]byte(yamlTrue), &dstTrue); err != nil {
		t.Fatalf("unmarshal enabled=true: %v", err)
	}
	if dstTrue.Enabled == nil || !*dstTrue.Enabled {
		t.Fatal("explicit true must round-trip as *true")
	}
}

// ── Validate (fail-closed contract, FASE 1C+1D closure) ────────────────

func TestGenerationStyle_Validate_FullShape_Success(t *testing.T) {
	s := GenerationStyle{
		Name:         "cinematic",
		DisplayName:  "Cinematic",
		PromptSuffix: "movie still",
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate should pass for full shape: %v", err)
	}
}

func TestGenerationStyle_Validate_LegacyShapeWithDescription_Success(t *testing.T) {
	// Legacy entries have DisplayName + Description (no PromptSuffix).
	// EffectiveSuffix() falls back to Description, so Validate passes
	// (preserves backward compat with "name + description only" YAML).
	s := GenerationStyle{
		Name:        "legacy",
		DisplayName: "Legacy",
		Description: "legacy description only",
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate should pass for legacy DisplayName+Description shape: %v", err)
	}
}

func TestGenerationStyle_Validate_NoDisplayName_Fails(t *testing.T) {
	s := GenerationStyle{
		Name:         "cinematic",
		PromptSuffix: "movie still",
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for missing DisplayName")
	}
	if !errors.Is(err, ErrStyleMissingDisplayName) {
		t.Fatalf("err = %v, want errors.Is(_, ErrStyleMissingDisplayName) = true", err)
	}
}

func TestGenerationStyle_Validate_NoDisplayName_WhitespaceFails(t *testing.T) {
	// Whitespace-only DisplayName is treated as missing.
	s := GenerationStyle{
		Name:         "cinematic",
		DisplayName:  "   ",
		PromptSuffix: "movie still",
	}
	err := s.Validate()
	if !errors.Is(err, ErrStyleMissingDisplayName) {
		t.Fatalf("whitespace-only DisplayName must fail: %v", err)
	}
}

func TestGenerationStyle_Validate_NoSuffixAnywhere_Fails(t *testing.T) {
	// No DisplayName? No, present here. No PromptSuffix, no Description?
	// Then EffectiveSuffix() == "" → ErrStyleMissingSuffix.
	s := GenerationStyle{
		Name:        "cinematic",
		DisplayName: "Cinematic",
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error when EffectiveSuffix() is empty")
	}
	if !errors.Is(err, ErrStyleMissingSuffix) {
		t.Fatalf("err = %v, want errors.Is(_, ErrStyleMissingSuffix) = true", err)
	}
}

func TestGenerationStyle_Validate_DisabledStillValid(t *testing.T) {
	// Disabled styles are still well-formed (operators keep them on
	// disk for archival). Validate does NOT touch the Enabled flag.
	fal := false
	s := GenerationStyle{
		Name:         "cinematic",
		DisplayName:  "Cinematic",
		PromptSuffix: "movie still",
		Enabled:      &fal,
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("disabled style should still validate: %v", err)
	}
	if s.IsEnabled() {
		t.Fatal("test sanity: Enabled=false must report false via IsEnabled()")
	}
}

func TestGenerationStyle_Validate_ErrorMessageIncludesStyleName(t *testing.T) {
	// Operators reading the error log need to identify which style
	// failed without grepping the YAML.
	s := GenerationStyle{Name: "broken-style"}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" {
		t.Fatal("err.Error() should not be empty")
	}
	// Quick substring check: the style name should appear in the message.
	if !contains(gotSlice(err.Error()), "broken-style") {
		t.Fatalf("err message %q must include style name %q", err.Error(), "broken-style")
	}
}

// gotSlice is a tiny helper to keep the test readable.
func gotSlice(s string) []string { return []string{s} }

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		for i := 0; i+len(needle) <= len(h); i++ {
			if h[i:i+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}
