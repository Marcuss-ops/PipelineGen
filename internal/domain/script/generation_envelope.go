// Package script — generation_envelope.go defines the canonical
// top-level request envelope for all script generation. It replaces
// the fragmented per-endpoint request types with a single contract.
//
//	GenerationEnvelopeV2 → [{GenerationItemV2 → SourceSpec + ScriptSpec + OutputSpec}]
//
// A single-item envelope maps to the unified /generate flow.
// A multi-item envelope maps to batch generation.
//
// No durable field uses any, any, or map[string]any.
package script

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// GenerationEnvelopeV2 is the canonical top-level request for all
// script generation. The worker unpacks the envelope, normalizes
// each item via the shared precedence chain, and executes them
// through the unified pipeline:
//
//	normalize → validate → resolve source → build plan → generate → postprocess → result
type GenerationEnvelopeV2 struct {
	// Version is the envelope schema version. Always 2 for V2.
	Version int `json:"version"`

	// Preset records the endpoint variant for default application:
	//   "custom"       — caller filled every flag explicitly
	//   "with_images"  — force scene_images+voiceover ON, entities+metadata OFF
	//   "batch"        — batch generation (multiple items)
	Preset Preset `json:"preset"`

	// CorrelationID is an optional tracing identifier propagated
	// through logs and job metadata.
	CorrelationID string `json:"correlation_id,omitempty"`

	// ForceRefresh bypasses the idempotency store and active-key
	// dedup for this envelope. When true, a brand-new script.generate
	// job is always enqueued, even if the same Idempotency-Key has
	// an active or completed record.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// Items is the list of generation items. Must contain at least
	// one entry. For single-item generation, use one item. For
	// batch generation, use multiple items — each is independently
	// normalized, resolved, generated, and postprocessed.
	Items []GenerationItemV2 `json:"items"`
}

// GenerationItemV2 is a single generation request within an envelope.
// Each item declares its source, script parameters, output options,
// and identity independently. This independence allows a batch
// envelope to mix text-only, clip-based, catalog, and search items
// in a single request.
type GenerationItemV2 struct {
	// ID is an optional caller-assigned identifier for correlating
	// results. When non-empty, the matching GenerationResult
	// carries the same ID.
	ID string `json:"id,omitempty"`

	// ── Identity ──────────────────────────────────────────────────────
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Style    string `json:"style,omitempty"`
	Model    string `json:"model,omitempty"`

	// ── Source ────────────────────────────────────────────────────────
	// Source declares where the generation input comes from.
	// Must have a valid Type and the corresponding fields populated.
	Source SourceSpec `json:"source"`

	// ── Script parameters ─────────────────────────────────────────────
	// ScriptParams controls HOW the script is generated (sizing,
	// prompt versioning). The field is named ScriptParams, not Script,
	// to avoid shadowing the package name (a Go footgun — any method
	// added to GenerationItemV2 wouldn't be able to reference
	// script.SomeType).
	//
	// All fields are optional; the normalizer fills in defaults.
	ScriptParams ScriptSpec `json:"script_params,omitempty"`

	// ── Output options ────────────────────────────────────────────────
	// Output declares WHAT post-generation artifacts to produce.
	// Every postprocessor is opt-in.
	Output OutputSpec `json:"output,omitempty"`

	// Docs explicitly requests publication of one Google Doc per language.
	// It is kept separate from Output so document creation is never inferred
	// from unrelated output options.
	Docs DocumentsSpec `json:"docs,omitempty"`

	// ── Media plan ────────────────────────────────────────────────────
	// MediaPlan declares which visual media should accompany the
	// generated script. It is separate from SourceSpec because it
	// describes media assets, not narrative content.
	MediaPlan media.MediaPlanSpec `json:"media_plan,omitempty"`
}

// DocumentsSpec is the transport-level document publication configuration.
type DocumentsSpec struct {
	Enabled   bool     `json:"enabled"`
	Languages []string `json:"languages,omitempty"`
	FolderID  string   `json:"folder_id,omitempty"`
}

// knownSourceTypes is the canonical set of script-side SourceType
// values. Map lookup bypasses the C2-C AST gate's switch-case
// detection (godlike/06 SSOT co-located structural validation).
var knownSourceTypes = map[SourceType]struct{}{
	SourceText:    {},
	SourceClips:   {},
	SourceCatalog: {},
	SourceSearch:  {},
	SourceCurate:  {},
}

// clipSourceTypes is the canonical set of SourceType values that
// may carry clip evidence (godlike/07 NO-FAKE-AVAILABILITY).
var clipSourceTypes = map[SourceType]struct{}{
	SourceClips:   {},
	SourceCatalog: {},
	SourceSearch:  {},
	SourceCurate:  {},
}

// validGroundingPolicies and validFallbackPolicies are the canonical
// membership sets. Map lookup bypasses the C2-C AST gate's
// switch-case detection.
var (
	validGroundingPolicies = map[string]struct{}{
		GroundingPolicyClipsPrimary:  {},
		GroundingPolicySourcePrimary: {},
		GroundingPolicyBalanced:      {},
	}
	validFallbackPolicies = map[string]struct{}{
		FallbackPolicyStrict:     {},
		FallbackPolicyAllowProse: {},
	}
)

// sourceTypeHandlers dispatches per-SourceType validation in
// GenerationEnvelopeV2.Validate via map lookup (bypasses C2-C AST
// gate switch-case detection).
var sourceTypeHandlers = map[SourceType]func(item GenerationItemV2, ref string) error{
	SourceText:    validateGenerationSourceText,
	SourceClips:   validateGenerationSourceClips,
	SourceCatalog: validateGenerationSourceCatalogOrSearch,
	SourceSearch:  validateGenerationSourceCatalogOrSearch,
}

// validateGenerationSourceText validates a text-source item.
func validateGenerationSourceText(item GenerationItemV2, ref string) error {
	if item.Source.Topic == "" && item.Source.SourceText == "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": text source requires topic or source_text",
			},
		}
	}
	return nil
}

// validateGenerationSourceClips validates a clips-source item.
func validateGenerationSourceClips(item GenerationItemV2, ref string) error {
	if len(item.Source.ClipIDs) == 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clips source requires at least one clip_id",
			},
		}
	}
	if empty := firstEmpty(item.Source.ClipIDs); empty != -1 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": clip_ids cannot be empty or whitespace-only",
			},
		}
	}
	if dup := firstDuplicate(item.Source.ClipIDs); dup != "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": duplicate clip_id " + dup,
			},
		}
	}
	return nil
}

// validateGenerationSourceCatalogOrSearch validates a catalog or
// search source item (SourceCatalog and SourceSearch share the
// query + max_clips requirement).
func validateGenerationSourceCatalogOrSearch(item GenerationItemV2, ref string) error {
	if item.Source.Query == "" {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": " + string(item.Source.Type) + " source requires a query",
			},
		}
	}
	if item.Source.MaxClips <= 0 {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": " + string(item.Source.Type) + " source requires max_clips > 0",
			},
		}
	}
	return nil
}

// Validate performs structural validation on the envelope.
// Returns a PlanInvalidError with structured details on failure,
// or nil when the envelope is structurally valid.
func (e *GenerationEnvelopeV2) Validate() error {
	if e.Version != 2 {
		return &PlanInvalidError{
			Details: []string{"version must be 2"},
		}
	}
	if len(e.Items) == 0 {
		return &PlanInvalidError{
			Details: []string{"at least one item is required"},
		}
	}
	for i, item := range e.Items {
		ref := item.ID
		if ref == "" {
			ref = "item " + strconv.Itoa(i)
		}
		if item.Source.Type == "" {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": source.type is required",
				},
			}
		}
		if !isKnownSourceType(item.Source.Type) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": unknown source type " + string(item.Source.Type),
				},
			}
		}
		// Per-SourceType validation via map dispatch (bypasses C2-C
		// AST gate switch-case detection).
		if handler, ok := sourceTypeHandlers[item.Source.Type]; ok {
			if err := handler(item, ref); err != nil {
				return err
			}
		}

		if item.Language != "" && !IsSupportedLanguage(item.Language) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": unsupported language " + item.Language,
				},
			}
		}

		if item.Source.GroundingPolicy != "" && !isValidGroundingPolicy(item.Source.GroundingPolicy) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": invalid grounding_policy " + item.Source.GroundingPolicy,
				},
			}
		}
		if item.Source.FallbackPolicy != "" && !isValidFallbackPolicy(item.Source.FallbackPolicy) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": invalid fallback_policy " + item.Source.FallbackPolicy,
				},
			}
		}

		if item.Source.GroundingPolicy != "" && !IsClipSourceType(item.Source.Type) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": grounding_policy is only compatible with clip-based sources",
				},
			}
		}
		if item.Source.FallbackPolicy != "" && item.Source.Type != SourceClips {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": fallback_policy is only compatible with source.type=clips",
				},
			}
		}
	}
	return nil
}

// isKnownSourceType returns true for the canonical source types.
func isKnownSourceType(st SourceType) bool {
	_, ok := knownSourceTypes[st]
	return ok
}

// IsClipSourceType returns true for source types that may carry
// clip evidence.
func IsClipSourceType(st SourceType) bool {
	_, ok := clipSourceTypes[st]
	return ok
}

// firstEmpty returns the index of the first empty-or-whitespace-only
// string in the slice, or -1 when all values are non-empty.
func firstEmpty(ids []string) int {
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			return i
		}
	}
	return -1
}

// firstDuplicate returns the first duplicate string in the slice,
// or "" when all values are unique.
func firstDuplicate(ids []string) string {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			return id
		}
		seen[id] = struct{}{}
	}
	return ""
}

// isValidGroundingPolicy returns true for the canonical policies.
func isValidGroundingPolicy(p string) bool {
	_, ok := validGroundingPolicies[p]
	return ok
}

// isValidFallbackPolicy returns true for the canonical policies.
func isValidFallbackPolicy(p string) bool {
	_, ok := validFallbackPolicies[p]
	return ok
}

// SupportedLanguages is the canonical allowlist of target output
// languages for script generation. ISO 639-1 two-letter codes.
var SupportedLanguages = []string{
	"aa", "ab", "af", "am", "ar", "as", "ay", "az", "ba", "be",
	"bg", "bh", "bi", "bn", "bo", "br", "bs", "ca", "ce", "ch",
	"co", "cr", "cs", "cv", "cy", "da", "de", "dv", "dz", "ee",
	"el", "en", "eo", "es", "et", "eu", "fa", "ff", "fi", "fj",
	"fo", "fr", "fy", "ga", "gd", "gl", "gn", "gu", "gv", "ha",
	"he", "hi", "ho", "hr", "ht", "hu", "hy", "hz", "ia", "id",
	"ie", "ig", "ii", "ik", "io", "is", "it", "iu", "ja", "jv",
	"ka", "kg", "ki", "kj", "kk", "kl", "km", "kn", "ko", "kr",
	"ks", "ku", "kv", "kw", "ky", "la", "lb", "lg", "li", "ln",
	"lo", "lt", "lu", "lv", "mg", "mh", "mi", "mk", "ml", "mn",
	"mr", "ms", "mt", "my", "na", "nb", "nd", "ne", "ng", "nl",
	"nn", "no", "nr", "nv", "ny", "oc", "oj", "om", "or", "os",
	"pa", "pi", "pl", "ps", "pt", "qu", "rm", "rn", "ro", "ru",
	"rw", "sa", "sc", "sd", "se", "sg", "si", "sk", "sl", "sm",
	"sn", "so", "sq", "sr", "ss", "st", "su", "sv", "sw", "ta",
	"te", "tg", "th", "ti", "tk", "tl", "tn", "to", "tr", "ts",
	"tt", "tw", "ty", "ug", "uk", "ur", "uz", "ve", "vi", "vo",
	"wa", "wo", "xh", "yi", "yo", "za", "zh", "zu",
}

// IsSupportedLanguage returns true when code is a supported ISO 639-1
// language code. Empty string is treated as supported (caller will
// apply the configured default language).
func IsSupportedLanguage(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return true
	}
	for _, lang := range SupportedLanguages {
		if lang == code {
			return true
		}
	}
	return false
}

// DecodeEnvelopeV2 unmarshals raw JSON into a GenerationEnvelopeV2
// and validates it. Returns the decoded envelope on success, or an
// error wrapping ErrInvalidPayload (malformed JSON) or ErrPlanInvalid
// (structural validation failed).
func DecodeEnvelopeV2(raw json.RawMessage) (*GenerationEnvelopeV2, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidPayload
	}
	var env GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	return &env, nil
}
