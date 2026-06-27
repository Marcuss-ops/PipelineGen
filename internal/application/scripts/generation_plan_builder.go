// Package scripts — generation_plan_builder.go constructs a
// ResolvedGenerationPlan from a normalized GenerationItemV2.
// The builder is the single place where a V2 item becomes a plan
// that the engine consumes.
//
// At this point the item has already been through:
//  1. Structural validation (GenerationEnvelopeV2.Validate)
//  2. Preset application (ApplyPreset)
//  3. Config defaults (applyConfigDefaults)
//  4. Safety defaults (applySafetyDefaults)
//  5. Semantic validation (ValidateItem)
//
// The builder does not validate — it trusts its inputs.
package scripts

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildPlan constructs a ResolvedGenerationPlan from a validated,
// normalized GenerationItemV2. The plan is the canonical contract
// between the normalizer and the engine.
//
// ClipEvidence is intentionally left nil — the source resolver
// (PR3) fills it after resolving the actual clips. At this point
// the plan carries source text but not yet clip evidence.
//
// Field precedence (where both GenerationItemV2 and a sub-spec carry
// the same field name):
//
//	Topic        → item.Source.Topic (fallback: item.Title)
//	Guidelines   → item.Source.Guidelines (ScriptSpec.Guidelines is
//	               a separate concept — script-level editorial notes,
//	               not source-level writing constraints)
//	Style        → item.Style (top-level; ScriptSpec.Style is unused)
//
// Returns a fully populated plan. Every field that has a zero value
// has already been filled by the normalizer; the builder only maps
// fields from the item shape to the plan shape.
func BuildPlan(item scriptpkg.GenerationItemV2) scriptpkg.ResolvedGenerationPlan {
	// Topic: prefer the source-level topic when explicitly set;
	// otherwise fall back to the item title (which itself defaults
	// to source.topic in the normalizer).
	topic := item.Source.Topic
	if topic == "" {
		topic = item.Title
	}

	plan := scriptpkg.ResolvedGenerationPlan{
		ID:                  item.ID,
		Title:               item.Title,
		Topic:               topic,
		Language:            item.Language,
		Tone:                item.Tone,
		Model:               item.Model,
		Mode:                modeForSource(item.Source.Type),
		SourceText:          item.Source.SourceText,
		Guidelines:          item.Source.Guidelines,
		TargetWords:         item.ScriptParams.TargetWords,
		Duration:            item.ScriptParams.Duration,
		MinWords:            item.ScriptParams.MinWords,
		SentencesPerImage:   item.ScriptParams.SentencesPerImage,
		ImagesPerScene:      item.ScriptParams.ImagesPerScene,
		Style:               item.Style,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion,
		UseMemory:           item.ScriptParams.UseMemory,
		ForceRefresh:        item.ScriptParams.ForceRefresh,
		DriveFolderID:       item.Output.DriveFolderID,
		MaxChars:            item.Output.MaxChars,
		OutputFmt:           item.Output.OutputFmt,
		SaveToDB:            item.Output.SaveToDB,
		Languages:           append([]string(nil), item.Output.Languages...),
	}

	// Build postprocessor list from output flags.
	plan.Postprocessors = buildPostprocessorList(item.Output)

	// PR 2: split of the legacy ambiguous `Prompt` field.
	//   - RenderedPrompt carries real editorial instructions
	//     (topic, source text, guidelines, sizing).
	//   - The model-facing prompt body NEVER contains a fingerprint
	//     hash; fingerprints go to SourceFingerprint (cache-key
	//     input, not model input).
	plan.RenderedPrompt = buildEditorialPrompt(item)
	plan.SourceKind = string(item.Source.Type)
	plan.PromptProfile = "default-v1"

	return plan
}

// BuildPlans constructs a ResolvedGenerationPlan for every
// already-normalized item. The caller must pass normalized items
// (from NormalizeEnvelope), not raw envelope items.
func BuildPlans(items []scriptpkg.GenerationItemV2) []scriptpkg.ResolvedGenerationPlan {
	if len(items) == 0 {
		return nil
	}
	plans := make([]scriptpkg.ResolvedGenerationPlan, len(items))
	for i := range items {
		plans[i] = BuildPlan(items[i])
	}
	return plans
}

// ── Helpers ──────────────────────────────────────────────────────

// modeForSource maps a SourceType to the engine mode string.
func modeForSource(st scriptpkg.SourceType) string {
	switch st {
	case scriptpkg.SourceText:
		return "text"
	case scriptpkg.SourceClips, scriptpkg.SourceCurate:
		return "clip_to_script"
	case scriptpkg.SourceCatalog:
		return "clip_to_script"
	case scriptpkg.SourceSearch:
		return "clip_to_script"
	default:
		return "text"
	}
}

// buildPostprocessorList derives the ordered list of postprocessors
// from OutputSpec flags. Order: entities → metadata → voiceover →
// images → document → persistence.
func buildPostprocessorList(out scriptpkg.OutputSpec) []string {
	var pp []string
	if out.ExtractEntities {
		pp = append(pp, "entities")
	}
	if out.GenerateMetadata {
		pp = append(pp, "metadata")
	}
	if out.GenerateVoiceover {
		pp = append(pp, "voiceover")
	}
	if out.GenerateSceneImages {
		pp = append(pp, "images")
	}
	if out.GenerateDocument {
		pp = append(pp, "document")
	}
	if out.SaveToDB {
		pp = append(pp, "persistence")
	}
	return pp
}

// buildEditorialPrompt assembles the actual model-facing prompt
// from item.Source + item.ScriptParams. PR 2 reverse of the
// previous buildPrompt which returned BuildItemIdentity(item)
// (a SHA-256 hex digest sent to the model as the prompt —
// wrong, anti-pattern).
//
// The prompt body contains topic, source text, guidelines, sizing,
// style, language, tone — fields the model reads — but NEVER a
// fingerprint hash. Fingerprints live on plan.SourceFingerprint.
func buildEditorialPrompt(item scriptpkg.GenerationItemV2) string {
	var parts []string

	if item.Source.Topic != "" {
		parts = append(parts, "Topic: "+item.Source.Topic)
	}
	if item.Source.SourceText != "" {
		parts = append(parts, "Source text:\n"+item.Source.SourceText)
	}
	if item.Source.Guidelines != "" {
		parts = append(parts, "Guidelines:\n"+item.Source.Guidelines)
	}
	if item.ScriptParams.TargetWords > 0 {
		parts = append(parts, "Target words: "+itoa(item.ScriptParams.TargetWords))
	}
	if item.ScriptParams.MinWords > 0 {
		parts = append(parts, "Min words: "+itoa(item.ScriptParams.MinWords))
	}
	if item.Style != "" {
		parts = append(parts, "Style: "+item.Style)
	}
	if item.Language != "" {
		parts = append(parts, "Language: "+item.Language)
	}
	if item.Tone != "" {
		parts = append(parts, "Tone: "+item.Tone)
	}
	if item.ScriptParams.PromptVersion != "" {
		parts = append(parts, "Prompt version: "+item.ScriptParams.PromptVersion)
	}

	return strings.Join(parts, "\n\n")
}
