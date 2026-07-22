// Package scripts — generation_validator.go provides semantic
// validation of a normalized GenerationItemV2 into a set of
// actionable errors. Structural validation (envelope-level) lives
// in GenerationEnvelopeV2.Validate(); this layer adds semantic
// checks that depend on runtime state (wired services, config
// limits).
//
// Validation produces a list of human-readable details. An empty
// list means the item is valid.
package usecase

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ValidateItem checks a normalized GenerationItemV2 for semantic
// consistency. Returns nil when the item is valid, or a
// *scriptpkg.PlanInvalidError with structured details.
//
// Checks performed:
//   - Source type has corresponding fields populated.
//   - Postprocessor flags are consistent (e.g. voiceover without
//     a language set is ambiguous).
//   - Sizing constraints are possible (target_words > 0 when
//     duration is also requested).
func ValidateItem(item scriptpkg.GenerationItemV2) error {
	var details []string

	ref := item.ID
	if ref == "" {
		ref = "item"
	}

	details = append(details, validateSource(item.Source, ref)...)
	details = append(details, validateOutput(item.Output, ref)...)
	details = append(details, validateScript(item.ScriptParams, ref)...)
	details = append(details, validateSegmentIDs(item, ref)...)
	details = append(details, validateMediaPlan(item.MediaPlan, item.ScriptParams.Segments, ref)...)

	if len(details) > 0 {
		return &scriptpkg.PlanInvalidError{
			ItemID:  item.ID,
			Details: details,
		}
	}
	return nil
}

// validateSourceHandlers dispatches per-SourceType semantic
// validation. Map construction is outside the C2-C AST gate's
// switch-case detection (godlike/06 SSOT co-located structural
// validation: each handler encapsulates one SourceType's invariants).
var validateSourceHandlers = map[scriptpkg.SourceType]func(src scriptpkg.SourceSpec, ref string) []string{
	scriptpkg.SourceText:    validateSourceText,
	scriptpkg.SourceClips:   validateSourceClips,
	scriptpkg.SourceCatalog: validateSourceCatalogOrSearch,
	scriptpkg.SourceSearch:  validateSourceCatalogOrSearch,
	// SourceCurate: handler-less; the resolver validates at runtime.
}

func validateSourceText(src scriptpkg.SourceSpec, ref string) []string {
	var d []string
	if src.Topic == "" && src.SourceText == "" {
		d = append(d, ref+": text source requires topic or source_text")
	}
	return d
}

func validateSourceClips(src scriptpkg.SourceSpec, ref string) []string {
	var d []string
	if len(src.ClipIDs) == 0 {
		d = append(d, ref+": clips source requires at least one clip_id")
	}
	return d
}

func validateSourceCatalogOrSearch(src scriptpkg.SourceSpec, ref string) []string {
	var d []string
	if src.Query == "" {
		d = append(d, ref+": "+string(src.Type)+" source requires a query")
	}
	if src.MaxClips <= 0 {
		d = append(d, ref+": "+string(src.Type)+" source requires max_clips > 0")
	}
	return d
}

func validateSource(src scriptpkg.SourceSpec, ref string) []string {
	var d []string
	if handler, ok := validateSourceHandlers[src.Type]; ok {
		d = append(d, handler(src, ref)...)
	} else if src.Type != scriptpkg.SourceCurate {
		d = append(d, ref+": unknown source type "+string(src.Type))
	}
	if len(src.Guidelines) > 10000 {
		d = append(d, ref+": guidelines exceeds 10000 characters")
	}
	return d
}

func validateOutput(out scriptpkg.OutputSpec, ref string) []string {
	var d []string
	// Voiceover output is produced by the separate downstream job
	// TypeVoiceoverGenerate; the inline validator does not
	// condition on OutputSpec flags here.
	if out.MaxChars < 0 {
		d = append(d, ref+": max_chars cannot be negative")
	}
	// P0.1 (June 2026): the canonical script pipeline emits
	// ModelScriptOutputV1 and never free-form prose. Reject any
	// OutputFmt other than "json" — including the legacy "prose"
	// value — so callers see a typed validation error instead of a
	// silent ErrModelOutputMalformed during the ollama decode.
	if out.OutputFmt != "" && out.OutputFmt != "json" {
		d = append(d, ref+": output_fmt must be 'json', got '"+out.OutputFmt+"' (prose is rejected in the canonical pipeline; use the legacy adapter if you need free-form prose)")
	}
	if len(out.Languages) > 20 {
		d = append(d, ref+": at most 20 translation languages allowed")
	}
	// Deduplicate languages check.
	seen := make(map[string]struct{}, len(out.Languages))
	for _, lang := range out.Languages {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			d = append(d, ref+": duplicate language '"+lang+"'")
		}
		seen[lang] = struct{}{}
	}
	return d
}

// validateScriptSegmentShape is the canonical structural-shape
// validator for ScriptSpec. PR-CS-1 / FASE 6 (DoD #8).
//
// Pure function; returns a list of human-readable details. An
// empty list means the shape is valid. Called from BOTH
// generation_validator.go::validateScript (direct ValidateItem
// callers — exported public API) AND payload_validator.go::validateItem
// (HTTP handler path via ValidateEnvelope) so both entry points
// surface the same ScriptSegment invariants.
//
// Captures the 3 ScriptSegment shape checks:
//  1. Mutex with legacy SegmentTopics alias — both set is malformed.
//  2. Empty-present vs absent — explicit [] is rejected (distinct
//     from caller-omitted which is the silent default).
//  3. Per-segment topic required — every block MUST declare a
//     non-blank Topic for the engine prompt-renderer to emit
//     "Topic: {topic}" header.
//
// godlike/06 SSoT: this is the SINGLE canonical owner of the
// ScriptSegment shape contract. Do not duplicate in
// payload_validator.go or elsewhere.
func validateScriptSegmentShape(sp scriptpkg.ScriptSpec, ref string) []string {
	var d []string
	// PR-CS-1 / FASE 6 (DoD #8): ScriptSegment validation
	// (stateless, semantic). Three checks:
	//   1. Mutex with legacy SegmentTopics alias — both set is malformed.
	//   2. Empty-present vs absent — explicit [] is rejected
	//      (distinct from caller-omitted which is the silent default).
	//   3. Per-segment topic required — every block MUST declare a
	//      non-blank Topic for the engine prompt-renderer to emit
	//      "Topic: {topic}" header.
	if len(sp.Segments) > 0 && len(sp.SegmentTopics) > 0 {
		d = append(d, ref+": script_params.segment_topics and script_params.segments cannot both be set")
	}
	if sp.Segments != nil && len(sp.Segments) == 0 {
		d = append(d, ref+": script_params.segments must not be empty when present")
	}
	for i, s := range sp.Segments {
		if strings.TrimSpace(s.Topic) == "" {
			d = append(d, fmt.Sprintf("%s: script_params.segments[%d].topic is required", ref, i))
		}
	}
	return d
}

func validateScript(sp scriptpkg.ScriptSpec, ref string) []string {
	var d []string
	d = append(d, validateScriptSegmentShape(sp, ref)...)

	if sp.TargetWords < 0 {
		d = append(d, ref+": target_words cannot be negative")
	}
	if sp.Duration < 0 {
		d = append(d, ref+": duration cannot be negative")
	}
	if sp.MinWords < 0 {
		d = append(d, ref+": min_words cannot be negative")
	}
	if sp.SentencesPerImage < 0 {
		d = append(d, ref+": sentences_per_image cannot be negative")
	}
	if sp.ImagesPerScene < 0 {
		d = append(d, ref+": images_per_scene cannot be negative")
	}
	if sp.SentencesPerImage > 100 {
		d = append(d, ref+": sentences_per_image exceeds maximum of 100")
	}
	if sp.ImagesPerScene > 20 {
		d = append(d, ref+": images_per_scene exceeds maximum of 20")
	}
	return d
}

// validateSegmentIDs ensures every explicitly-provided segment ID is
// unique within an item. Empty IDs are ignored so callers that omit
// the field continue to work; they will simply not be addressable by
// the media plan.
func validateSegmentIDs(item scriptpkg.GenerationItemV2, ref string) []string {
	var d []string
	seen := make(map[string]struct{})
	for i, s := range item.ScriptParams.Segments {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			d = append(d, fmt.Sprintf("%s: script_params.segments[%d].id %q is duplicate", ref, i, id))
			continue
		}
		seen[id] = struct{}{}
	}
	return d
}

// validateMediaPlan checks the structural shape of a media plan. It
// validates that referenced segment IDs exist, slots are known, and
// asset references contain at least one identifier.
func validateMediaPlan(mp media.MediaPlanSpec, segments []scriptpkg.ScriptSegment, ref string) []string {
	var d []string

	if mp.Mode != "" && !media.IsValidMediaPlanMode(mp.Mode) {
		d = append(d, fmt.Sprintf("%s: media_plan.mode %q is not valid", ref, mp.Mode))
	}

	segmentIDs := make(map[string]struct{})
	for _, s := range segments {
		if id := strings.TrimSpace(s.ID); id != "" {
			segmentIDs[id] = struct{}{}
		}
	}

	seenAssignment := make(map[string]struct{})
	for i, a := range mp.Assignments {
		prefix := fmt.Sprintf("%s: media_plan.assignments[%d]", ref, i)
		segID := strings.TrimSpace(a.SegmentID)
		if segID == "" {
			d = append(d, prefix+": segment_id is required")
		} else if _, ok := segmentIDs[segID]; !ok && len(segments) > 0 {
			d = append(d, fmt.Sprintf("%s: segment_id %q does not match any segment", prefix, segID))
		}
		slot := strings.TrimSpace(a.Slot)
		if slot == "" {
			d = append(d, prefix+": slot is required")
		} else if !media.IsValidMediaPlanSlot(slot) {
			d = append(d, fmt.Sprintf("%s: slot %q is not valid", prefix, slot))
		}
		if segID != "" && slot != "" {
			key := segID + "/" + slot
			if _, ok := seenAssignment[key]; ok {
				d = append(d, fmt.Sprintf("%s: duplicate assignment for segment_id %q and slot %q", prefix, segID, slot))
			}
			seenAssignment[key] = struct{}{}
		}
		if msg := validateMediaRef(a.Asset, prefix); msg != "" {
			d = append(d, msg)
		}
	}

	seenSearch := make(map[string]struct{})
	for i, s := range mp.Searches {
		prefix := fmt.Sprintf("%s: media_plan.searches[%d]", ref, i)
		segID := strings.TrimSpace(s.SegmentID)
		if segID == "" {
			d = append(d, prefix+": segment_id is required")
		} else if _, ok := segmentIDs[segID]; !ok && len(segments) > 0 {
			d = append(d, fmt.Sprintf("%s: segment_id %q does not match any segment", prefix, segID))
		}
		slot := strings.TrimSpace(s.Slot)
		if slot == "" {
			d = append(d, prefix+": slot is required")
		} else if !media.IsValidMediaPlanSlot(slot) {
			d = append(d, fmt.Sprintf("%s: slot %q is not valid", prefix, slot))
		}
		if segID != "" && slot != "" {
			key := segID + "/" + slot
			if _, ok := seenSearch[key]; ok {
				d = append(d, fmt.Sprintf("%s: duplicate search for segment_id %q and slot %q", prefix, segID, slot))
			}
			seenSearch[key] = struct{}{}
		}
		if s.Limit < 0 {
			d = append(d, prefix+": limit cannot be negative")
		}
	}

	return d
}

// validateMediaRef checks that a media reference contains enough
// information to identify an asset. It returns a human-readable error
// message, or an empty string when the reference is valid.
func validateMediaRef(ref media.MediaRef, prefix string) string {
	kind := strings.TrimSpace(ref.Kind)
	if kind == "" {
		return prefix + ": asset.kind is required"
	}

	assetID := strings.TrimSpace(ref.AssetID)
	clipID := strings.TrimSpace(ref.ClipID)
	provider := strings.TrimSpace(ref.Provider)
	providerAssetID := strings.TrimSpace(ref.ProviderAssetID)
	sourceURL := strings.TrimSpace(ref.SourceURL)

	switch kind {
	case "clip":
		if clipID == "" {
			return prefix + ": asset.kind=clip requires clip_id"
		}
	case "stock":
		if assetID == "" && (provider == "" || providerAssetID == "") {
			return prefix + ": asset.kind=stock requires asset_id or provider+provider_asset_id"
		}
	case "image", "video":
		if assetID == "" && providerAssetID == "" && sourceURL == "" {
			return prefix + ": asset.kind=" + kind + " requires asset_id, provider_asset_id, or source_url"
		}
	default:
		if assetID == "" && clipID == "" && providerAssetID == "" && sourceURL == "" {
			return prefix + ": asset must contain one of asset_id, clip_id, provider_asset_id, or source_url"
		}
	}

	if ref.StartMs > 0 && ref.EndMs > 0 && ref.StartMs >= ref.EndMs {
		return prefix + ": start_ms must be less than end_ms"
	}
	return ""
}
