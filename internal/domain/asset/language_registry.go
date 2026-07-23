// Package asset — language_registry.go: canonical LanguageSpec +
// LanguageRegistry types. The pipeline's single source of truth
// for "what languages do we operate on?" and "what per-language
// capabilities does each of them have?".
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// LanguageSpec + LanguageRegistry types. Every other layer
// (config, application, infrastructure) consumes these types
// verbatim — no second representation, no shadow slice, no
// hard-coded `["it", "en", "es", ...]` literal.
//
// godlike/07 no-fake-availability: a constructed registry
// either holds the spec list (Read-only OK) or is nil. Callers
// MUST treat the nil case as "multilingual pipeline disabled"
// (empty EnabledLanguages()), not as "fall back to en".
package asset

import (
	"fmt"
)

// LanguageSpec is the canonical projection of a BCP-47 language
// code onto pipeline capabilities.
//
// yaml tags mirror the operator-facing YAML key names so
// cfg.MultilingualConfig.Languages can decode directly without
// an ad-hoc struct or custom mapper.
type LanguageSpec struct {
	// Code is the canonical BCP-47 tag (e.g. "it", "en",
	// "pt-BR", "zh-Hans"). Required.
	Code string `yaml:"code" json:"code"`

	// Enabled gates the language for ALL pipeline
	// capabilities — TranslateClips/GenerateTTS only apply
	// when Enabled is also true. Disabled codes are stored
	// as-is (audit trail) but excluded from
	// EnabledLanguages() results. Default false.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// TranslateClips enables the catalog translation
	// pathway (the TextTrackMaterializer + asset.text
	// chain). true → the language is a candidate for
	// fan-out translation jobs.
	TranslateClips bool `yaml:"translate_clips" json:"translate_clips"`

	// GenerateTTS enables the voiceover + edge-tts
	// synthesis pathway for the language. false disables
	// TTS-priced workloads even if other capabilities are
	// enabled (TTS is resource-heavy).
	GenerateTTS bool `yaml:"generate_tts" json:"generate_tts"`

	// EdgeTTSVoice is the canonical Microsoft Edge TTS voice
	// identifier for this language (e.g. "it-IT-DiegoNeural").
	// Used by the voiceover pipeline when GenerateTTS is true.
	EdgeTTSVoice string `yaml:"edge_tts_voice" json:"edge_tts_voice"`
}

// Validate enforces the minimal format invariants on the spec.
// godlike/07 fail-fast: every invalid spec is rejected at
// construction time (via NewLanguageRegistry), not silently
// dropped from the registry.
//
// The check is intentionally light — deep BCP-47 grammar
// validation belongs in a separate validator if needed. Here
// we only enforce: Code non-empty, length ≥ 2, and the first
// rune is an ASCII letter (allowing hyphens for subtagged
// codes like "pt-BR").
func (s LanguageSpec) Validate() error {
	if s.Code == "" {
		return fmt.Errorf("language_spec: code is required")
	}
	if len(s.Code) < 2 {
		return fmt.Errorf("language_spec: code %q is too short (min 2 chars)", s.Code)
	}
	first := s.Code[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return fmt.Errorf("language_spec: code %q must start with an ASCII letter", s.Code)
	}
	return nil
}

// LanguageRegistry is the canonical query surface for pipeline
// languages. godlike/06 SSOT: this interface is the SOLE
// canonical owner of "what languages are enabled?" + "what
// pipeline capabilities does each language have?". The whole
// pipeline queries this surface, NOT an in-memory `[]string`
// slice or a hardcoded fallback.
//
// Implementations MUST be Read-only after construction; mutating
// the registry at runtime is not supported (a future step adds
// a reload-from-config API if operators need it).
//
// Capability-filter contract (godlike/06 SSOT split) — THIS
// IS THE NON-NEGOTIABLE PART OF THE INTERFACE. Future
// implementers, maintainers, and reviewers MUST honour it:
//
// The registry is the canonical CAPABILITY SURFACE. It
// returns every spec with its full flag set; it does NOT
// pre-filter by TranslateClips, GenerateTTS, or any future
// capability. Each downstream consumer picks which
// capability it cares about at the query site:
//   - texttracks.Resolver filters by TranslateClips=true
//     before fanning out clip-translation jobs.
//   - a future voiceover consumer would filter by
//     GenerateTTS=true.
//   - an introspection CLI exposes every flag.
//
// Implementations MUST NOT add convenience methods like
// TranslateClipLanguages() / TTSLanguages() that smuggle a
// pre-applied capability filter back into the registry
// layer. Such methods are a godlike/06 SSOT violation
// requiring an explicit godlike-allowance exception.
// Consumers pick the capability at the query site — that
// is the architecture decision; the registry exposes raw
// capabilities only.
type LanguageRegistry interface {
	// EnabledLanguages returns every spec with Enabled=true,
	// in the YAML-declared order (no sorting; the operator's
	// declared order IS the priority order for most callers
	// — chains honour the first match wins rule).
	EnabledLanguages() []LanguageSpec

	// Resolve looks up a single spec by code. Returns
	// (spec, true) on hit; (zero, false) on miss.
	// godlike/07 honest lock: callers MUST treat the bool as
	// authoritative, not nil-check the spec.
	Resolve(code string) (LanguageSpec, bool)
}

// staticLanguageRegistry is the canonical in-memory
// implementation.
type staticLanguageRegistry struct {
	specs  []LanguageSpec
	byCode map[string]LanguageSpec
}

// NewLanguageRegistry constructs a registry from a slice of
// specs. Validates each spec and rejects the entire batch on
// any error (godlike/07 fail-fast at boot). Preserves
// declaration order. Rejects duplicate codes (ambiguous
// projection — caller must collapse before calling).
func NewLanguageRegistry(specs []LanguageSpec) (LanguageRegistry, error) {
	byCode := make(map[string]LanguageSpec, len(specs))
	out := make([]LanguageSpec, 0, len(specs))
	for i, s := range specs {
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("language_registry[%d]: %w", i, err)
		}
		if _, dup := byCode[s.Code]; dup {
			return nil, fmt.Errorf("language_registry: duplicate code %q at index %d", s.Code, i)
		}
		byCode[s.Code] = s
		out = append(out, s)
	}
	return &staticLanguageRegistry{specs: out, byCode: byCode}, nil
}

// NewLanguageRegistryFromCodes constructs a default registry
// from a flat BCP-47 code list (every spec lands with
// Enabled=true, TranslateClips=true, GenerateTTS=true). Used
// by the legacy `materialize_languages: it,en,es,...` CSV
// back-compat path in cfg.MultilingualConfig AND by test
// fixtures that build a registry from a `[]string` literal.
//
// godlike/07 fail-fast: a duplicate or empty code is rejected
// the same way as a direct NewLanguageRegistry call.
func NewLanguageRegistryFromCodes(codes []string) (LanguageRegistry, error) {
	specs := make([]LanguageSpec, 0, len(codes))
	for _, c := range codes {
		specs = append(specs, LanguageSpec{
			Code:           c,
			Enabled:        true,
			TranslateClips: true,
			GenerateTTS:    true,
		})
	}
	return NewLanguageRegistry(specs)
}

// EmptyLanguageRegistry returns a registry with NO languages
// enabled. godlike/07 fail-closed default for the "multilingual
// pipeline disabled" case (the composition root constructs
// this when cfg.Media.Multilingual.Enabled is false; the
// pipeline surfaces empty candidate pools instead of
// silently falling back to en).
func EmptyLanguageRegistry() LanguageRegistry {
	return &staticLanguageRegistry{
		specs:  []LanguageSpec{},
		byCode: map[string]LanguageSpec{},
	}
}

// EnabledLanguages returns every spec with Enabled=true, in
// the YAML-declared order (no sorting; the operator's
// declared order IS the priority order for most callers —
// chains honour the first match wins rule).
//
// Capability-filter contract (mirrors the interface-level
// contract on LanguageRegistry): this method ONLY filters
// by Enabled=true. The TranslateClips / GenerateTTS flags
// are returned verbatim so consumers can pick which
// capability they care about at the query site:
//   - texttracks.Resolver.CandidateLanguages filters by
//     TranslateClips=true to fan out clip-translation jobs.
//   - a future voiceover consumer would filter by
//     GenerateTTS=true.
//   - an introspection CLI filter-by-anything would
//     expose every flag.
//
// Consumers MUST NOT call EnabledLanguages() and assume a
// single canonical ordering beyond Enabled — the spec is
// the capability surface, not a fan-out action list.
func (r *staticLanguageRegistry) EnabledLanguages() []LanguageSpec {
	out := make([]LanguageSpec, 0, len(r.specs))
	for _, s := range r.specs {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// Resolve looks up a single spec by code. Returns
// (spec, true) on hit; (zero, false) on miss. godlike/07
// honest lock: callers MUST treat the bool as
// authoritative, not nil-check the spec.
//
// Capability-filter contract (mirrors the interface-level
// contract on LanguageRegistry): the returned spec carries
// the FULL capability flag set (Enabled / TranslateClips /
// GenerateTTS / ...future). The method does NOT pre-filter
// by any capability — the consumer chooses which capability
// it gates on at the query site. Consumers MUST NOT call
// Resolve and assume "the spec is already filtered to my
// capability" — it isn't; it IS the canonical capability
// surface for that code.
//
// Enabled=false entries are returned verbatim — the
// consumer MUST check `spec.Enabled` before treating the
// returned spec as a fan-out candidate. Disabled codes are
// registered (audit trail — e.g. operator removed "en"
// from the YAML) but excluded from
// EnabledLanguages(); Resolve still surfaces them so a
// CLI / introspection tool can show "this code was
// previously registered, current status: disabled".
func (r *staticLanguageRegistry) Resolve(code string) (LanguageSpec, bool) {
	s, ok := r.byCode[code]
	return s, ok
}
