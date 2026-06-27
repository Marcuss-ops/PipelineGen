// Package script — generation_envelope.go defines the canonical
// top-level request envelope for all script generation. It replaces
// the fragmented per-endpoint request types with a single contract.
//
//   GenerationEnvelopeV2 → [{GenerationItemV2 → SourceSpec + ScriptSpec + OutputSpec}]
//
// A single-item envelope maps to the previous /generate-from-clips,
// /generate-with-images, /generate-from-catalog, and /curate flows.
// A multi-item envelope maps to the previous /generate-batch flow.
//
// No durable field uses interface{}, any, or map[string]any.
package script

import (
	"encoding/json"
	"strconv"
)

// GenerationEnvelopeV2 is the canonical top-level request for all
// script generation. The worker unpacks the envelope, normalizes
// each item via the shared precedence chain, and executes them
// through the unified pipeline:
//
//   normalize → validate → resolve source → build plan → generate → postprocess → result
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
}

// Validate performs structural validation on the envelope.
// Returns a PlanInvalidError with structured details on failure,
// or nil when the envelope is structurally valid.
//
// Structural checks (non-exhaustive — the normalizer adds
// semantic defaults):
//   - Version must be 2.
//   - Items must be non-empty.
//   - Each item's Source.Type must be non-empty.
//   - Each item's Source must have the corresponding fields
//     (text → topic; clips → clip_ids; catalog/search → query).
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
		switch item.Source.Type {
		case SourceText:
			if item.Source.Topic == "" && item.Source.SourceText == "" {
				return &PlanInvalidError{
					ItemID: item.ID,
					Details: []string{
						ref + ": text source requires topic or source_text",
					},
				}
			}
		case SourceClips:
			if len(item.Source.ClipIDs) == 0 {
				return &PlanInvalidError{
					ItemID: item.ID,
					Details: []string{
						ref + ": clips source requires at least one clip_id",
					},
				}
			}
		case SourceCatalog, SourceSearch:
			if item.Source.Query == "" {
				return &PlanInvalidError{
					ItemID: item.ID,
					Details: []string{
						ref + ": " + string(item.Source.Type) + " source requires a query",
					},
				}
			}
		}
	}
	return nil
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


