package asset

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

// ── StyleID: typed-string identity ─────────────────────────────────────

func TestStyleID_BackedByStringSemantics(t *testing.T) {
	// StyleID is the typed wrapper around the canonical style name.
	// Operators use string literals at the wire boundary; internal
	// callers MUST consume StyleID so future rename lands as compile
	// error, not runtime panic (AGENTS.md Pattern 0).
	id := StyleID("test-style")
	if string(id) != "test-style" {
		t.Fatalf("string conversion = %q, want %q", string(id), "test-style")
	}
	if id == "" {
		t.Fatal("StyleID should not compare equal to empty string literal")
	}
}

// ── StyleVersion: typed-counter ─────────────────────────────────────────

func TestStyleVersion_ZeroIsUnversioned(t *testing.T) {
	// 0 means unversioned (legacy entry).
	var v StyleVersion
	if v != 0 {
		t.Fatalf("zero value = %d, want 0", v)
	}
}

func TestStyleVersion_PositiveBumped(t *testing.T) {
	v := StyleVersion(3)
	if int(v) != 3 {
		t.Fatalf("StyleVersion(3) = %d, want 3", int(v))
	}
	if v == 0 {
		t.Fatal("StyleVersion(3) must not equal zero")
	}
}

// ── StyleDefinition: yaml + json round-trip ─────────────────────────────

func TestStyleDefinition_YAML_RoundTrip_NameAndVersion(t *testing.T) {
	src := StyleDefinition{
		// ID is not serialized (yaml:"-") — registry.Load sets it
		// from Name post-unmarshal. Skip here.
		Version:        StyleVersion(2),
		Name:           "test-style",
		DisplayName:    "Test Style",
		PromptSuffix:   "test suffix, photorealistic",
		NegativePrompt: "blurry, low quality",
		DestinationKey: "ai-images/test-style",
		Enabled:        true,
	}

	data, err := yaml.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Unmarshal into the alias chain to lock the cross-type wire
	// format byte-stable.
	var dst GenerationStyle
	if err := yaml.Unmarshal(data, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if dst.Name != src.Name {
		t.Errorf("Name = %q, want %q", dst.Name, src.Name)
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
	if dst.DestinationKey != src.DestinationKey {
		t.Errorf("DestinationKey = %q, want %q", dst.DestinationKey, src.DestinationKey)
	}
	if !dst.Enabled {
		t.Errorf("Enabled = false, want true")
	}
}

func TestStyleDefinition_YAML_AbsentEnabledDefaultsFalse(t *testing.T) {
	// Silent flip: omitting the `enabled:` key in YAML defaults to
	// false (was true under the legacy tri-state *bool semantics).
	// Existing config pins enabled explicitly so production is
	// unaffected; this test locks the new contract.
	const yamlBody = `
name: test-style
version: 2
display_name: Test Style
prompt_suffix: test suffix
`
	var dst StyleDefinition
	if err := yaml.Unmarshal([]byte(yamlBody), &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst.Enabled {
		t.Fatal("absent Enabled must round-trip as false (silent-flip contract)")
	}
}

// ── Valid: fail-closed contract ─────────────────────────────────────────

func TestStyleDefinition_Valid_HappyPath(t *testing.T) {
	s := StyleDefinition{
		ID:           StyleID("test-style"),
		Version:      StyleVersion(2),
		Name:         "test-style",
		DisplayName:  "Test Style",
		PromptSuffix: "test suffix, photorealistic",
		Enabled:      true,
	}
	if err := s.Valid(); err != nil {
		t.Fatalf("Valid should pass on canonical happy-path: %v", err)
	}
}

func TestStyleDefinition_Valid_MissingID_Fails(t *testing.T) {
	s := StyleDefinition{
		DisplayName:  "Test Style",
		PromptSuffix: "test suffix",
	}
	err := s.Valid()
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !errors.Is(err, ErrStyleMissingID) {
		t.Fatalf("err = %v, want errors.Is(_, ErrStyleMissingID) = true", err)
	}
}

func TestStyleDefinition_Valid_MissingDisplayName_Fails(t *testing.T) {
	s := StyleDefinition{
		ID:           StyleID("test-style"),
		PromptSuffix: "test suffix",
	}
	err := s.Valid()
	if err == nil {
		t.Fatal("expected error for empty DisplayName")
	}
	if !errors.Is(err, ErrStyleMissingDisplayName) {
		t.Fatalf("err = %v, want errors.Is(_, ErrStyleMissingDisplayName) = true", err)
	}
}

func TestStyleDefinition_Valid_MissingPromptSuffix_Fails(t *testing.T) {
	s := StyleDefinition{
		ID:          StyleID("test-style"),
		DisplayName: "Test Style",
	}
	err := s.Valid()
	if err == nil {
		t.Fatal("expected error for empty PromptSuffix")
	}
	if !errors.Is(err, ErrStyleMissingPromptSuffix) {
		t.Fatalf("err = %v, want errors.Is(_, ErrStyleMissingPromptSuffix) = true", err)
	}
}

func TestStyleDefinition_Valid_WhitespaceDisplayNameFails(t *testing.T) {
	s := StyleDefinition{
		ID:           StyleID("test-style"),
		DisplayName:  "   ",
		PromptSuffix: "test suffix",
	}
	err := s.Valid()
	if !errors.Is(err, ErrStyleMissingDisplayName) {
		t.Fatalf("whitespace DisplayName must fail: got %v", err)
	}
}

func TestStyleDefinition_Valid_DisabledStyleIsStillValid(t *testing.T) {
	// Disabled styles are still well-formed (operators keep them on
	// disk for archival / A-B testing). Valid does NOT touch the
	// Enabled flag — same contract as the legacy Validate().
	s := StyleDefinition{
		ID:           StyleID("test-style"),
		DisplayName:  "Test Style",
		PromptSuffix: "test suffix",
		Enabled:      false,
	}
	if err := s.Valid(); err != nil {
		t.Fatalf("disabled style should still validate: %v", err)
	}
	if s.Enabled {
		t.Fatal("test sanity: Enabled=false must read false")
	}
}

// ── Alias chain: GenerationStyle ≡ StyleDefinition ─────────────────────

func TestGenerationStyle_AliasPointsTo_StyleDefinition(t *testing.T) {
	// The alias is the load-bearing surface for the existing 60+
	// callers. Within the asset package, the relationship is just
	// `type GenerationStyle = StyleDefinition`; the cross-package
	// alias chain (image/styles.StyleDefinition = asset.GenerationStyle
	// = asset.StyleDefinition) is exercised by the image/styles test
	// suite where the actual cross-package import exists.
	src := StyleDefinition{
		ID:           StyleID("test-style"),
		Name:         "test-style",
		DisplayName:  "Test Style",
		PromptSuffix: "test suffix",
		Enabled:      true,
	}

	// StyleDefinition → GenerationStyle (alias assignment, no copy).
	var g GenerationStyle = src
	if g.Name != src.Name || g.DisplayName != src.DisplayName || g.PromptSuffix != src.PromptSuffix {
		t.Fatalf("StyleDefinition → GenerationStyle alias lost data: got %+v want %+v", g, src)
	}

	// GenerationStyle → StyleDefinition (reverse alias, also no copy).
	var back StyleDefinition = g
	if back.ID != src.ID || back.Name != src.Name || back.Enabled != src.Enabled {
		t.Fatalf("GenerationStyle → StyleDefinition alias lost data: got %+v want %+v", back, src)
	}
}
