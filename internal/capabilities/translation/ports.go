// Package translation — Fase 9 step 1 of the Spina Dorsale (July 2026):
// canonical application-layer port surface for text translation.
//
// This file declares the structural contract that every translation
// provider (today: *ollama.Generator.Wrap; future: deepl,
// google-translate, gcloud-translate) MUST satisfy. No provider is
// wired here yet — composition root wiring lands in a successive
// commit once the port surface freezes.
//
// ── Relationship to the canonical domain types ──────────────────────────
//
// `internal/capabilities/translation/translation.go` (Fase 0 della Spina
// Dorsale) already declares domain-layer DTOs of overlapping concept:
//
//	domain.TranslationCommand  { SourceText, SourceLanguage, TargetLanguage,
//	                             ContentKind, Preserve, ModelPolicy(enum) }
//	domain.TranslationResult   { Text, SourceLanguage, TargetLanguage,
//	                             Provider, Model, CacheStatus }
//	domain.ModelPolicy         enum string: fast | quality | auto
//
// Those domain types are the cross-domain canonical contract (one
// owner per fact at the domain boundary, per godlike/06). The types
// declared HERE are the application-layer wiring contract — a
// different scope, a different owner per fact at the wiring boundary,
// per AGENTS.md Pattern 0.
//
// Field names diverge deliberately from the domain types because the
// application-layer port is the surface used by provider adapters and
// composition roots: shorter PascalCase identifiers (`SourceLang`
// vs `SourceLanguage`) are the convention there. A future step in
// Fase 9 will reconcile the two surfaces via an explicit
// application-layer adapter that converts domain.TranslationCommand
// to this file's TranslationCommand before forwarding to providers —
// tracked as forward-pointer (see "Migration to TranslationPort"
// in `internal/capabilities/translation/migration.go` once it lands).
//
// ── API surface ────────────────────────────────────────────────────────
//
//	TranslationPort   (interface)
//	TranslationCommand (DTO — application-layer)
//	TranslationResult  (DTO — application-layer)
//	ModelPolicy        (struct — provider+model+generation params)
//	unimplemented      (forward-compat stub — gRPC pattern)
//
// Compile-time assertion at the bottom of the file pins the contract:
// a future drift in TranslationPort's signature triggers a build
// failure instead of a runtime panic.
package translation

import (
	"context"
	"errors"
)

// ── TranslationPort ───────────────────────────────────────────────────────

// TranslationPort is the canonical application-layer surface for
// translating text. All provider implementations MUST satisfy this
// port via compile-time assertion at the bottom of their file:
//
//	var _ TranslationPort = (*ollamaAdapter)(nil)
//
// Today the wire-side port surface still uses the legacy stragglers
// in `internal/capabilities/scripts/usecase/services.go`:
//
//	TextTranslationService   (3-arg: ctx, text, lang -> string)
//	TranslatorService        (4-arg: ctx, text, lang, model -> string)
//
// and the concrete *ollama.Generator exposes `TranslateText` +
// `TranslateTextWithModel` directly. Migration of those callers
// onto TranslationPort is a Fase 9 follow-up; this file freezes the
// target signature today so the migration has a stable destination.
type TranslationPort interface {
	// Translate runs a translation round-trip.
	//
	// cmd.ModelPolicy == nil is a valid input: it means "use the
	// server default policy for this cmd.Source/Target language
	// pair + content kind". Implementations MUST handle nil
	// ModelPolicy explicitly (no panic-on-deref).
	//
	// Implementations SHOULD honour cmd.ModelHints when non-empty
	// (e.g. preserve_formatting, preserve_scene_markers). Hints are
	// caller-intent flags, not provider directives; missing keys are
	// provider-default.
	Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error)
}

// MetadataGenerator is the canonical application port for generating and
// translating video metadata.
type MetadataGenerator interface {
	GenerateVideoMetadataWithModel(ctx context.Context, title, model string) (string, []string, error)
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// ── TranslationCommand ────────────────────────────────────────────────────

// TranslationCommand is the application-layer DTO sent through
// TranslationPort.Translate.
//
// The struct uses concise PascalCase field names (SourceLang /
// TargetLang / Text) which is the application-layer convention; the
// JSON wire shape is owned by gateway-edge structs at the API
// boundary, NOT at the port level (port is in-process only).
//
// Distinct from `domain.TranslationCommand`:
//
//   - SourceLang ↔ domain.SourceLanguage  (same semantic, shorter name)
//   - TargetLang ↔ domain.TargetLanguage  (same semantic, shorter name)
//   - Text       ↔ domain.SourceText      (same semantic, shorter name)
//
// ContentKind + PreservationPolicy + enum-string ModelPolicy from the
// domain surface are NOT reflected here. Phase 9 step-2 adapters will
// map domain.TranslationCommand → TranslationCommand at the
// application-layer boundary, applying defaults from cmd.ModelHints
// + app-level ModelPolicy (the struct) so callers don't need to know
// provider internals.
//
// Fase 9 step 3 (July 2026, Spina Dorsale): json tags added on every
// field for forward-compat wire-shape contracts; users of the (in-process)
// port surface are unaffected (Go struct tags do not change call sites),
// but external test suites that JSON-marshal/round-trip the struct will
// now see the documented wire names with omitempty edges. The
// `model_policy,omitempty` tag is the canonical back-compat gate for
// any future gateway-edge struct that mirrors TranslationCommand onto
// the API surface (godlike/07 no-fake-availability: a non-nil but
// zero-value ModelPolicy must NOT be shipped over the wire).
type TranslationCommand struct {
	// SourceLang is the BCP-47 language tag of the source text.
	// Empty means auto-detect (provider-specific behaviour).
	SourceLang string `json:"source_lang,omitempty"`

	// TargetLang is the BCP-47 language tag to translate into.
	// Required. Empty is a contract violation — implementations
	// MAY return an error.
	TargetLang string `json:"target_lang,omitempty"`

	// Text is the source text to translate. Required.
	Text string `json:"text,omitempty"`

	// ModelHints is an optional caller-intent map.
	//
	// Recognised keys (provider-specific handling):
	//   - "preserve_formatting"   → keep markdown/HTML/line-breaks
	//   - "preserve_entities"     → keep proper nouns untranslated
	//   - "preserve_scene_markers" → keep scene boundary markers
	//   - "no_cache"              → bypass cache lookup
	//   - "deterministic"         → prefer temperature=0
	//
	// Unknown keys are ignored without error (forward-compat). Empty
	// map is the default; nil is treated identically to empty.
	//
	// The 5 documented keys above are convention only —
	// no constants are exposed in this package yet. A future PR
	// should add `internal/capabilities/translation/hints.go` with
	// `HintPreserveFormatting = "preserve_formatting"` consts (and the
	// other 4) so providers and scripts can't drift on misspelled keys.
	// Out of scope for Fase 9 step 1 (definitions + contract only).
	ModelHints map[string]string `json:"model_hints,omitempty"`

	// ModelPolicy is the optional provider+model+generation control.
	// nil = server default (pick by content length + TargetLang
	// heuristics). Implementations MUST handle nil without panic.
	//
	// Distinct from the domain enum `domain.ModelPolicy` (fast|quality|auto):
	// this struct exposes the implementation-level knobs (exact provider,
	// exact model, generation params) that callers USUALLY do not need
	// to set. Most callers leave ModelPolicy == nil and trust the
	// server default — the enum in domain is the caller-intent surface.
	//
	// json:"model_policy,omitempty" is the canonical back-compat gate:
	// a nil-ModelPolicy (the common case) is omitted from any wire
	// serialisation; a non-nil-but-zero-value ModelPolicy also serialises
	// as missing (omitempty), so callers MUST serialise explicitly with
	// `json.Marshal` + a non-zero struct to surface their model choice
	// over the wire.
	ModelPolicy *ModelPolicy `json:"model_policy,omitempty"`
}

// ── TranslationResult ─────────────────────────────────────────────────────

// TranslationResult is the application-layer DTO returned by
// TranslationPort.Translate.
//
// Field names use the application-layer convention (TranslatedText
// vs domain's Text). Used by upstream consumers (scripts, voiceover,
// books, metadata) to surface translation outcomes + provenance
// (model, provider, cache status) for observability and audit.
//
// ── Spec scope + deliberate extension (Fase 9 step 1) ────────────────
//
// User spec listed 3 minimum fields:
//
//	TranslatedText / Confidence / UsedModel
//
// This struct ships 7. The 4 added fields are provenance metadata
// required for observability, audit, and cache auditing at the
// application-layer wiring boundary:
//
//	UsedProvider / SourceLang / TargetLang / CacheStatus
//
// Distinct from `domain.TranslationResult` (Text, SourceLanguage,
// TargetLanguage, Provider, Model, CacheStatus). Mirror fields:
//
//	TranslatedText ↔ domain.Text
//	SourceLang     ↔ domain.SourceLanguage (detected or echoed)
//	TargetLang     ↔ domain.TargetLanguage
//	UsedProvider   ↔ domain.Provider
//	UsedModel      ↔ domain.Model
//	CacheStatus    ↔ domain.CacheStatus
//
// Confidence is application-layer only — domain doesn't track it
// because provider scores are implementation-specific. Future
// adapter converts domain.TranslationResult → TranslationResult by
// translating field names + adding Confidence from provider output.
//
// ── Forward-pointer (canonical tracker entry) ────────────────────────
//
// The reconciliation between domain.TranslationResult and this struct
// (field-by-field mirror forward + reverse converters) is **NOT**
// tracked here as a Go-side artifact. Per godlike/07, the canonical
// tracker entry will land in one of:
//
//   - architecture/deprecations.yaml#TRANSLATION-PORT-DOMAIN-RECONCILE
//     (when Fase 9 step-2 starts; today no breakage, so no record needed yet)
//   - architecture/current.yaml#fase-9-followups
//     (status=pending, owner=application/translation, deadline=Fase 9 step-2)
//
// Until that entry exists, the migration backlogs forever. A future
// agent finding this file before Fase 9 step-2 lands should add the
// canonical tracker entry FIRST, then proceed with the reconciliation.
type TranslationResult struct {
	// TranslatedText is the translated output text.
	// Empty when the provider returned an empty/error-prone result
	// (callers surface it via TranslationStatus or a separate
	// failure marker — see the scripts package's
	// ScriptArtlistClipSuggestion.TranslationError).
	TranslatedText string `json:"translated_text,omitempty"`

	// Confidence is a 0..1 provider self-reported confidence.
	// 0 = unknown (provider did not return a score); 1 = high confidence.
	// Provider-specific; consumers should treat it as a soft signal.
	Confidence float64 `json:"confidence,omitempty"`

	// UsedModel is the resolved effective model name
	// (e.g. "llama3:8b", "deepl-pro"). Empty is allowed if the
	// implementation cannot determine it post-hoc.
	UsedModel string `json:"used_model,omitempty"`

	// UsedProvider is the resolved provider identifier
	// (e.g. "ollama", "deepl", "google-translate").
	UsedProvider string `json:"used_provider,omitempty"`

	// SourceLang is the detected or provided source language tag.
	// Echoes cmd.SourceLang when caller supplied one.
	SourceLang string `json:"source_lang,omitempty"`

	// TargetLang is the target language tag. Echoes cmd.TargetLang.
	TargetLang string `json:"target_lang,omitempty"`

	// CacheStatus is "hit" | "miss" | "bypass" — opaque to callers
	// but useful for observability metrics + cache auditing. Empty
	// when the implementation cannot determine it.
	CacheStatus string `json:"cache_status,omitempty"`
}

// ── ModelPolicy ───────────────────────────────────────────────────────────

// ModelPolicy is the application-layer struct that controls provider
// selection + generation behaviour. Optional in TranslationCommand;
// nil means "server default".
//
// The server's default policy depends on the command's input
// characteristics (SourceLang/TargetLang pair + Text length +
// TranslationKind metadata if any). Today the server default
// approximates:
//
//	short metadata  → fast model (e.g. ollama gemma3:4b)
//	long book/script → quality model (e.g. ollama llama3:70b)
//	voiceover        → quality model (verbatim preservation)
//
// Distinct from the domain enum `domain.ModelPolicy` (fast|quality|auto):
// the application-layer struct is the *implementation-level* control
// surface, exposing the exact provider/model/generation params that
// power users may want to override. The domain enum is the
// *caller-intent* surface. Phase 9 step-2 adapters in the provider
// layer will translate the domain enum into this struct, applying
// provider-specific defaults (Temperature, MaxTokens).
type ModelPolicy struct {
	// Provider is the provider identifier to route the call to.
	// Today only "ollama" is wired; future providers: "deepl",
	// "google-translate", "gcloud-translate".
	// Empty when unspecified (server picks).
	Provider string `json:"provider,omitempty"`

	// Model is the provider-specific model name.
	// Examples: "llama3:8b", "gemma3:4b", "deepl-pro".
	// Empty when unspecified (server picks within chosen Provider).
	Model string `json:"model,omitempty"`

	// Temperature is the generation temperature override; 0 means
	// "use provider default" (NOT "deterministic"). For deterministic
	// behaviour, callers send cmd.ModelHints["deterministic"] instead.
	Temperature float64 `json:"temperature,omitempty"`

	// MaxTokens is the hard cap on output tokens; 0 means "use
	// provider default". Implementations clamp to provider limits.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// ── unimplemented stub (forward-compatibility sentinel) ───────────────────

// ErrUnimplemented is the sentinel returned by unimplemented.Translate.
// Signals "this TranslationPort is stubbed — no provider is wired".
// Callers should treat this as a configuration/dependency error
// (the operator forgot to wire the provider) and surface it loudly —
// NOT as a silent success or empty fallback.
var ErrUnimplemented = errors.New("translation: port not implemented (no provider wired)")

// unimplemented is the gRPC-style forward-compatibility stub for
// TranslationPort.
//
// Pattern (gRPC's `Unimplemented<Service>Server`): embedding
// `unimplemented` in a partial implementation gives forward-compat
// default behaviour — any un-overridden method returns a clear
// error instead of nil (which would have been a silent failure).
//
// Future adapter scaffolds can embed it as:
//
//	type ollamaAdapter struct {
//	    translation.TranslatorFunc  // applies the same call shape
//	    unimplemented               // forward-compat: any future
//	                                 // method added to TranslationPort
//	                                 // returns ErrUnimplemented
//	                                 // until explicitly overridden
//	}
//
// Lowercase `unimplemented` is intentional: the type is package-
// internal scaffolding for forward-compat assertions. External
// adapters embed it via the package boundary identical to local
// usage (Go allows embedding package-internal types within the
// embedding adapter's referencing package only — that's the
// scoping the lowercase gives us).
type unimplemented struct{}

// Translate returns ErrUnimplemented + zero-value TranslationResult.
// Embedding this in a partial adapter forces callers to either
// override Translate OR see ErrUnimplemented at runtime — never a
// silent success.
func (unimplemented) Translate(context.Context, TranslationCommand) (TranslationResult, error) {
	return TranslationResult{}, ErrUnimplemented
}

// ── compile-time assertion ────────────────────────────────────────────────

// Compile-time assertion: *unimplemented satisfies TranslationPort.
// This catches signature drift at build time:
//
//   - adding a method to TranslationPort → *unimplemented no longer
//     satisfies the interface → this assertion fails (good — forces
//     the contract author to provide a default for every new method
//     or risk compilation errors).
//   - changing Translate's signature → same as above.
//
// Future concrete adapters should add similar assertions at the
// bottom of their file:
//
//	var _ TranslationPort = (*ollamaAdapter)(nil)
//	var _ TranslationPort = (*deeplAdapter)(nil)
//
// That's the AGENTS.md Pattern 0 invariant: every provider adapter
// pins its compile-time compatibility at its inheritance boundary.
var _ TranslationPort = (*unimplemented)(nil)
