// Package scripts — generation_plan_builder.go constructs a
// ResolvedGenerationPlan from a normalized GenerationItemV2.
// The builder is the single place where a V2 item becomes a plan
// that the engine consumes.
//
// At this point the item has already been through:
//   1. Structural validation (GenerationEnvelopeV2.Validate)
//   2. Preset application (ApplyPreset)
//   3. Config defaults (applyConfigDefaults)
//   4. Safety defaults (applySafetyDefaults)
//   5. Semantic validation (ValidateItem)
//
// The builder does not validate — it trusts its inputs.
package scripts

import (
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
// Returns a fully populated plan. Every field that has a zero value
// has already been filled by the normalizer; the builder only maps
// fields from the item shape to the plan shape.
func BuildPlan(item scriptpkg.GenerationItemV2) scriptpkg.ResolvedGenerationPlan {
	plan := scriptpkg.ResolvedGenerationPlan{
		ID:    item.ID,
		Title: item.Title,
		Topic: item.Title, // topic = title by default; resolved source may override
		Language:       item.Language,
		Tone:           item.Tone,
		Model:          item.Model,
		Mode:           modeForSource(item.Source.Type),
		SourceText:     item.Source.SourceText,
		Guidelines:     item.Source.Guidelines,
		TargetWords:    item.ScriptParams.TargetWords,
		Duration:       item.ScriptParams.Duration,
		MinWords:       item.ScriptParams.MinWords,
		SentencesPerImage: item.ScriptParams.SentencesPerImage,
		ImagesPerScene:    item.ScriptParams.ImagesPerScene,
		Style:          item.Style,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion,
		UseMemory:      item.ScriptParams.UseMemory,
		ForceRefresh:   item.ScriptParams.ForceRefresh,
		DriveFolderID:  item.Output.DriveFolderID,
		MaxChars:       item.Output.MaxChars,
		OutputFmt:      item.Output.OutputFmt,
		SaveToDB:       item.Output.SaveToDB,
		Languages:      append([]string(nil), item.Output.Languages...),
	}

	// Build postprocessor list from output flags.
	plan.Postprocessors = buildPostprocessorList(item.Output)

	// Build prompt from source text and guidelines.
	plan.Prompt = buildPrompt(item)

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
	case scriptpkg.SourceClips:
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

// buildPrompt assembles the prompt text from the item's source and
// script parameters. Returns the deterministic item identity hash,
// which serves as the memory-gate cache key. The engine uses this
// together with the plan's SourceText to look up cached results.
//
// This matches the existing pattern where fingerprint = prompt for
// the memory gate (see pipeline_handlers.go::handleClipPathExplicit).
func buildPrompt(item scriptpkg.GenerationItemV2) string {
	return BuildItemIdentity(item)
}
