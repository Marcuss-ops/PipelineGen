// Package asset — language_registry_test.go: hermetic SSOT
// tests for the LanguageSpec + LanguageRegistry APIs.
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026).
package metadata

import (
	"reflect"
	"testing"
)

// TestNewLanguageRegistry_EnabledLanguagesOrderPreserved pins
// the contract at the registry layer: godlike/06 SSOT says
// EnabledLanguages() returns every spec with Enabled=true,
// in the YAML-declared order. TranslateClips / GenerateTTS
// capability flags are NOT filtered here — they are
// downstream consumer concerns (Resolver.CandidateLanguages
// filters by TranslateClips; a future voiceover consumer
// would filter by GenerateTTS). The registry is the SSOT
// for "what languages exist?"; consumers pick which
// capabilities they care about.
//
// Disabled entries (Enabled=false) ARE excluded — the
// boolean is the registry-level gate.
func TestNewLanguageRegistry_EnabledLanguagesOrderPreserved(t *testing.T) {
	specs := []LanguageSpec{
		{Code: "it", Enabled: true, TranslateClips: true},
		{Code: "en", Enabled: false, TranslateClips: true}, // disabled — excluded
		{Code: "fr", Enabled: true, TranslateClips: false}, // translate off — INCLUDED
		{Code: "es", Enabled: true, TranslateClips: true},
		{Code: "pt-BR", Enabled: true, TranslateClips: true},
	}
	reg, err := NewLanguageRegistry(specs)
	if err != nil {
		t.Fatalf("NewLanguageRegistry: %v", err)
	}
	got := reg.EnabledLanguages()
	gotCodes := []string{}
	for _, s := range got {
		gotCodes = append(gotCodes, s.Code)
	}
	wantCodes := []string{"it", "fr", "es", "pt-BR"}
	if !reflect.DeepEqual(gotCodes, wantCodes) {
		t.Fatalf("EnabledLanguages order: got %v, want %v", gotCodes, wantCodes)
	}
}

// TestNewLanguageRegistry_DuplicateRejected pins the SSOT
// invariant: two entries with the same Code are an ambiguous
// projection and rejected at construction time.
func TestNewLanguageRegistry_DuplicateRejected(t *testing.T) {
	specs := []LanguageSpec{
		{Code: "it", Enabled: true},
		{Code: "it", Enabled: false},
	}
	_, err := NewLanguageRegistry(specs)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
}

// TestNewLanguageRegistry_InvalidRejected pins the
// Validate() rejections: empty Code, length<2, non-letter
// prefix.
func TestNewLanguageRegistry_InvalidRejected(t *testing.T) {
	cases := []struct {
		name string
		spec LanguageSpec
	}{
		{"empty_code", LanguageSpec{Code: ""}},
		{"too_short", LanguageSpec{Code: "i"}},
		{"digit_prefix", LanguageSpec{Code: "1it"}},
		{"underscore", LanguageSpec{Code: "_it"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewLanguageRegistry([]LanguageSpec{c.spec}); err == nil {
				t.Fatalf("expected validation error for %+v, got nil", c.spec)
			}
		})
	}
}

// TestNewLanguageRegistry_ResolveHitMiss pins the
// Resolve(code) contract: returns (spec, true) on hit;
// (zero, false) on miss.
func TestNewLanguageRegistry_ResolveHitMiss(t *testing.T) {
	specs := []LanguageSpec{
		{Code: "it", Enabled: true, TranslateClips: true, GenerateTTS: false},
		{Code: "en", Enabled: true, TranslateClips: true, GenerateTTS: true},
	}
	reg, err := NewLanguageRegistry(specs)
	if err != nil {
		t.Fatalf("NewLanguageRegistry: %v", err)
	}
	s, ok := reg.Resolve("it")
	if !ok {
		t.Fatal("Resolve(it): expected hit, got miss")
	}
	if s.Code != "it" || !s.Enabled || !s.TranslateClips || s.GenerateTTS {
		t.Fatalf("Resolve(it): spec integrity failed, got %+v", s)
	}
	if _, ok := reg.Resolve("klingon"); ok {
		t.Fatal("Resolve(klingon): expected miss, got hit")
	}
}

// TestNewLanguageRegistry_DisabledStillResolvable pins that
// disabled entries are stored in the registry (for audit +
// YAML introspection) but excluded from EnabledLanguages().
func TestNewLanguageRegistry_DisabledStillResolvable(t *testing.T) {
	specs := []LanguageSpec{
		{Code: "it", Enabled: false, TranslateClips: true},
	}
	reg, err := NewLanguageRegistry(specs)
	if err != nil {
		t.Fatalf("NewLanguageRegistry: %v", err)
	}
	s, ok := reg.Resolve("it")
	if !ok || s.Enabled {
		t.Fatalf("Resolve(it): expected (disabled, true), got (%+v, %v)", s, ok)
	}
	if got := reg.EnabledLanguages(); len(got) != 0 {
		t.Fatalf("EnabledLanguages: expected empty, got %v", got)
	}
}

// TestNewLanguageRegistryFromCodes_Defaults pins the
// legacy-CSV back-compat constructor: every code lands with
// Enabled=true, TranslateClips=true, GenerateTTS=true.
func TestNewLanguageRegistryFromCodes_Defaults(t *testing.T) {
	codes := []string{"it", "en", "fr"}
	reg, err := NewLanguageRegistryFromCodes(codes)
	if err != nil {
		t.Fatalf("NewLanguageRegistryFromCodes: %v", err)
	}
	for _, code := range codes {
		s, ok := reg.Resolve(code)
		if !ok {
			t.Errorf("Resolve(%s): expected hit, got miss", code)
			continue
		}
		if !s.Enabled || !s.TranslateClips || !s.GenerateTTS {
			t.Errorf("Resolve(%s): expected Enabled+Translate+TTS=true, got %+v", code, s)
		}
	}
}

// TestNewLanguageRegistryFromCodes_EmptyAllowsDisabled pins
// that the empty-code-list path yields an empty registry
// (NOT a nil panic on EnabledLanguages).
func TestNewLanguageRegistryFromCodes_Empty(t *testing.T) {
	reg, err := NewLanguageRegistryFromCodes(nil)
	if err != nil {
		t.Fatalf("NewLanguageRegistryFromCodes(nil): %v", err)
	}
	if got := reg.EnabledLanguages(); len(got) != 0 {
		t.Fatalf("EnabledLanguages: expected empty, got %v", got)
	}
}

// TestEmptyLanguageRegistry_Empty pins the disabled-pipeline
// helper: all queries return empty / miss.
func TestEmptyLanguageRegistry_Empty(t *testing.T) {
	reg := EmptyLanguageRegistry()
	if got := reg.EnabledLanguages(); len(got) != 0 {
		t.Fatalf("EnabledLanguages: expected empty, got %v", got)
	}
	if _, ok := reg.Resolve("it"); ok {
		t.Fatal("Resolve(it): expected miss, got hit")
	}
}
