// Package script — output_spec.go defines the canonical ScriptSpec
// (HOW to generate) and OutputSpec (WHAT to produce) contracts.
//
// OutputSpec carries two active postprocessor toggles (ExtractEntities
// and GenerateMetadata) modelled as Toggle tri-state values. The
// Toggle type accepts both canonical string values ("default",
// "enabled", "disabled") and legacy booleans for wire compatibility.
package script

import (
	"bytes"
	"encoding/json"
)

// Toggle is the canonical tri-state for OutputSpec postprocessor
// flags. The precedence chain is:
//
//	ToggleDefault  — caller did not specify; defer to preset/config/safety
//	ToggleEnabled  — caller explicitly enabled this processor
//	ToggleDisabled — caller explicitly disabled this processor
//
// Resolve() algorithm:
//
//	if caller != ToggleDefault: caller
//	elif preset != ToggleDefault: preset
//	elif config != ToggleDefault: config
//	else: safety
type Toggle string

const (
	// ToggleDefault — no preference; downstream layers decide.
	ToggleDefault Toggle = "default"
	// ToggleEnabled — explicitly enabled.
	ToggleEnabled Toggle = "enabled"
	// ToggleDisabled — explicitly disabled.
	ToggleDisabled Toggle = "disabled"
)

// ToggleFromBool converts a legacy bool to the canonical Toggle
// (true → ToggleEnabled, false → ToggleDisabled). Used by callers
// that haven't migrated to explicit Toggle values.
func ToggleFromBool(b bool) Toggle {
	if b {
		return ToggleEnabled
	}
	return ToggleDisabled
}

// Resolve applies the precedence chain to a sequence of Toggles
// (caller, preset, config, safety) and returns the resolved value.
func (t Toggle) Resolve(caller, preset, config, safety Toggle) Toggle {
	if caller != ToggleDefault {
		return caller
	}
	if preset != ToggleDefault {
		return preset
	}
	if config != ToggleDefault {
		return config
	}
	return safety
}

// AsBool collapses the resolved toggle to a boolean. Only ToggleEnabled
// resolves to true. ToggleDisabled + ToggleDefault + the Go zero value
// Toggle("") all resolve to false.
//
// Semantics (godlike/07 NO-FAKE-AVAILABILITY): caller-omitted is
// treated as "no opt-in" (false) at the gate boundary. The legacy
// bool=true intent maps to ToggleEnabled → AsBool=true; the legacy
// bool=false intent maps to ToggleDisabled OR ToggleDefault → either
// of which resolves to false. Pre-PR-3 callers sending {}/caller-omitted
// see identical false semantics at the gate (zero bool = false both
// before and after the Toggle migration).
//
// Forward-pointer: applySafetyDefaults converts ToggleDefault →
// ToggleEnabled in the worker, so the post-safety-default
// HasAnyPostprocessor=AsBool() OR returns true and the registered
// postprocessors run for caller-omitted payloads. The preflight
// gate evaluates pre-safety and therefore does NOT block caller-omitted
// (mismatch with required-service is forward-pointer
// PR-PREFLIGHT-RUN-AFTER-SAFETY).
func (t Toggle) AsBool() bool {
	return t == ToggleEnabled
}

// UnmarshalJSON is dispatched by Go's json package ONLY when the
// method name is exactly "UnmarshalJSON" — this is the canonical
// json.Unmarshaler interface (golang.org/pkg/encoding/json). Accepts
// the canonical Toggle string form (3 valid values per the const
// block) AND the legacy bool form (true → ToggleEnabled,
// false → ToggleDisabled), plus JSON null → ToggleDefault (caller
// explicit nulls []resolve to "no preference", the same as omitting
// the field). Returns an error if the input is neither string, bool,
// nor null, OR if the string value is not one of the 3 canonical
// tokens.
//
// godlike/06 SSOT: this wire-shape adapter lives on the domain type
// itself (single canonical owner) so HTTP DTO layers and outbound
// marshalers do NOT need duplicate compat shims.
func (t *Toggle) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = ToggleDefault
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		switch Toggle(asString) {
		case ToggleDefault, ToggleEnabled, ToggleDisabled:
			*t = Toggle(asString)
			return nil
		}
		return &toggleInvalidStringError{raw: asString}
	}
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*t = ToggleFromBool(asBool)
		return nil
	}
	return &toggleInvalidTypeError{data: data}
}

// MarshalJSON (canonical json.Marshaler interface name — Go's json
// package dispatches only by exact name) emits the canonical Toggle
// string form. ToggleDefault MARSHALS AS THE STRING "default" —
// outbound wire-shape includes the explicit "default" token for
// fields that were unset (no override applied). For inbound compat,
// JSON `"key": "default"` strings also round-trip correctly.
func (t Toggle) MarshalJSON() ([]byte, error) {
	switch t {
	case ToggleDefault, ToggleEnabled, ToggleDisabled:
		return json.Marshal(string(t))
	}
	return nil, &toggleInvalidStringError{raw: string(t)}
}

// toggleInvalidStringError signals a non-canonical Toggle string
// during wire (un)marshaling. Reachable only via unmarshaling invalid
// data (operator-facing API fails closed) or via a buggy migration
// (compile-time string Set ensures the const block is exhaustive).
type toggleInvalidStringError struct {
	raw string
}

func (e *toggleInvalidStringError) Error() string {
	return "script: invalid Toggle string value: " + e.raw +
		" (canonical: \"default\", \"enabled\", \"disabled\")"
}

// toggleInvalidTypeError signals a wire payload that is neither string
// nor bool during SafeUnmarshalJSON.
type toggleInvalidTypeError struct {
	data []byte
}

func (e *toggleInvalidTypeError) Error() string {
	return "script: Toggle wire payload must be string or bool; got: " +
		string(bytes.TrimSpace(e.data))
}

// ── ScriptSpec ─────────────────────────────────────────────────────

// ScriptSegment is the canonical per-block payload entry
// (PR-CS-1, July 2026). Each script is decomposed into an ordered
// list of segments with optional per-segment source_text. DoD-driven
// contract (godlike/06 SSOT):
//   - Topic is required at runtime (validator enforces non-empty).
//   - source_text present → rewrite / improve that text only.
//   - source_text absent → write the segment from Topic + global source.
//   - target_words > 0 → use it; else fall back to
//     ScriptSpec.SegmentWords, else to ScriptSpec.TargetWords,
//     else default 80.
//
// The runtime mutex with SegmentTopics is enforced at the validator
// layer (DoD #8). ScriptSpec.Segments is the SOLE canonical owner;
// SourceSpec and Item layers consume via generator-normalizer copies.
type ScriptSegment struct {
	ID          string `json:"id,omitempty"`
	Topic       string `json:"topic"`
	SourceText  string `json:"source_text,omitempty"`
	TargetWords int    `json:"target_words,omitempty"`
}

// ScriptSpec controls the generation behaviour: sizing, style, and
// prompt versioning. Identity fields (Language, Tone, Model) live
// on GenerationItemV2; the normalizer merges them into the resolved
// plan.
//
// PR-CS-1 (July 2026): Segments is the per-block payload. When
// present:
//   - it is MUTUALLY EXCLUSIVE with SegmentTopics at runtime
//     (validator surfaces ErrSegmentsAndTopicTopicsBothSet on conflict).
//   - each segment MUST have a non-empty Topic (validator enforces).
//   - the engine prompt renders one block per segment in order.
//
// SegmentTopics remains the legacy alias — used when caller omits
// Segments.
type ScriptSpec struct {
	TargetWords         int             `json:"target_words,omitempty"`
	Duration            int             `json:"duration,omitempty"`
	MinWords            int             `json:"min_words,omitempty"`
	SegmentWords        int             `json:"segment_words,omitempty"`
	SegmentTopics       []string        `json:"segment_topics,omitempty"`
	Segments            []ScriptSegment `json:"segments,omitempty"`
	SentencesPerImage   int             `json:"sentences_per_image,omitempty"`
	ImagesPerScene      int             `json:"images_per_scene,omitempty"`
	Style               string          `json:"style,omitempty"`
	Guidelines          string          `json:"guidelines,omitempty"`
	TranscriptPolicy    string          `json:"transcript_policy,omitempty"`
	OrderingStrategy    string          `json:"ordering_strategy,omitempty"`
	PromptVersion       string          `json:"prompt_version,omitempty"`
	EditorPromptVersion string          `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string          `json:"qa_prompt_version,omitempty"`
	// PlannerVersion is the scene-planning algorithm version. It is
	// part of the generation fingerprint so changes to the planner
	// invalidate cached results.
	PlannerVersion string `json:"planner_version,omitempty"`
	ForceRefresh   bool   `json:"force_refresh,omitempty"`
	UseMemory      bool   `json:"use_memory,omitempty"`
	// SkipQualityGate keeps the editorial quality metrics but prevents a
	// request-scoped gate failure from blocking persistence. It is
	// intentionally opt-in and never changes the default gate behavior.
	SkipQualityGate bool `json:"skip_quality_gate,omitempty"`
}

// ── OutputSpec ─────────────────────────────────────────────────────

// OutputSpec declares which post-generation artifacts to produce.
// ExtractEntities and GenerateMetadata are Toggle tri-state values.
// Caller-explicit ToggleDisabled survives the applySafetyDefaults +
// ApplyPreset chain. SaveToDB is a bool persistence flag.
type OutputSpec struct {
	// ── Postprocessors (Toggle tri-state) ──────────────────────────
	//
	// ExtractEntities is an ACTIVE inline postprocessor
	// (ProcessorEntities) registered conditionally on
	// DefaultPolicyFor("entities") == ProcessorRequired. Caller
	// explicit ToggleDisabled is preserved through the resolution
	// chain.
	ExtractEntities Toggle `json:"extract_entities,omitempty"`

	// GenerateMetadata is an ACTIVE inline postprocessor
	// (ProcessorMetadata). See ExtractEntities comment for
	// Toggle semantics.
	GenerateMetadata Toggle `json:"generate_metadata,omitempty"`

	StockEnabled  Toggle              `json:"stock_enabled,omitempty"`
	StockBindings []StockBindingInput `json:"stock_bindings,omitempty"`

	// ── Persistence (bool — out of PR-3 scope per action plan) ──
	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// ── Voiceover options ────────────────────────────────────────────
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Document options ─────────────────────────────────────────────
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Formatting ──────────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"`

	// ── Translations ────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// PR-TRANSLATE-SCRIPT-SPEC PR-5+PR-6 (2026-07-09): the canonical
	// opt-in trigger for the TranslationProcessor. When non-empty,
	// buildPostprocessorList appends ProcessorTranslation between
	// metadata and clip_bindings in the EXECUTION order so the
	// translated SpecScene is visible to the downstream clip binder
	// (localised Drive links + clip titles). Empty string is the
	// "no translation requested" sentinel — caller-omission is
	// distinguishable from caller-explicit-empty because callers
	// that want "explicit no translation" pass TranslateTo="". The
	// resolution chain (caller > preset > config > safety) applies
	// unchanged from PR-3.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: a caller that supplies
	// TranslateTo="en" (the script's primary language) intentionally
	// bypasses translation (translator would no-op into ErrTranslationEqualToSource)
	// — this is the canonical explicit opt-in for "I already wrote in
	// the target language, don't waste LLM tokens". The processor
	// surfaces the no-op soft-warning + the bounded-reason metric so
	// operator dashboards can distinguish "translator idle" from
	// "translator absent".
	//
	// godlike/06 SSOT (one-canonical-owner-per-fact): TranslateTo lives
	// ONLY here on OutputSpec. BuildPlan copies it onto the
	// canonical ResolvedGenerationPlan so the postprocessor reads
	// a single source (the plan); no duplicate expression in
	// ResolvedGenerationPlan or in processor_translation.go.
	TranslateTo string `json:"translate_to,omitempty"`
}

// HasAnyPostprocessor returns true when at least one active postprocessor
// flag is non-disabled (ToggleEnabled or ToggleDefault resolve to true;
// ToggleDisabled resolves to false). SaveToDB is intentionally out of scope.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities.AsBool() ||
		o.GenerateMetadata.AsBool()
}

// UnmarshalJSON decodes OutputSpec with Toggle tri-state support.
// It performs a 2-pass decode:
//  1. Alias-decode via Alias OutputSpec (recursion-safe) so standard
//     json.Unmarshal handles struct field assignment via Toggle's
//     UnmarshalJSON method (bool/string/null forms).
//  2. Raw-map pre-pass to default OMITTED Toggle keys to ToggleDefault.
//     Go's default decode leaves absent Toggle fields at the zero value
//     Toggle(""), which AsBool() would treat as true. This method fixes
//     that by defaulting omitted keys to ToggleDefault.
//
// If the raw-map pre-pass fails, pass-1 alias values are preserved.
func (o *OutputSpec) UnmarshalJSON(data []byte) error {
	// Step 1: alias-decode to populate all fields via Toggle's
	// UnmarshalJSON (handles string/bool/null forms correctly).
	type Alias OutputSpec
	var tmp Alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*o = OutputSpec(tmp)

	// Step 2: raw-map pre-pass to detect OMITTED Toggle keys
	// individually (no clobber of pass-1 values). If the payload is
	// not a JSON object, skip the raw-map step — pass-1 alias values
	// are preserved (which may be Toggle("") for unmappable data,
	// but that's safer than collapsing to ToggleDefault and erasing
	// any valid string-form keys the alias pass managed to read).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if _, ok := raw["extract_entities"]; !ok {
			o.ExtractEntities = ToggleDefault
		}
		if _, ok := raw["generate_metadata"]; !ok {
			o.GenerateMetadata = ToggleDefault
		}
		if _, ok := raw["stock_enabled"]; !ok {
			o.StockEnabled = ToggleDefault
		}
	}
	return nil
}

// MarshalJSON is the canonical godlike/06 SSOT owner of OutputSpec's
// wire-shape egress. It converts every ToggleDefault value to the
// empty string before delegating to the alias marshaler — the alias
// (which has no custom MarshalJSON method) emits Toggle as a plain
// string, and the standard json:",omitempty" tag suppresses the key
// when the underlying string is empty. This restores the
// outbound-compact wire-shape that pre-PR-3 callers enjoy (5 toggle
// keys collapsed to ONLY the explicitly-set ones).
//
// godlike/06 SSOT: this lives ONLY here (canonical) — HTTP DTOs and
// any future JSON middleware MUST NOT duplicate it.
func (o OutputSpec) MarshalJSON() ([]byte, error) {
	type Alias OutputSpec
	tmp := Alias(o)

	// Collapse ToggleDefault → "" so omitempty kicks in. The other
	// 3 canonical states (ToggleEnabled/ToggleDisabled plus any
	// non-canonical value) are marshaled as-is.
	hideIfDefault := func(t *Toggle) {
		if *t == ToggleDefault {
			*t = ""
		}
	}

	hideIfDefault(&tmp.ExtractEntities)
	hideIfDefault(&tmp.GenerateMetadata)
	hideIfDefault(&tmp.StockEnabled)

	return json.Marshal(tmp)
}
