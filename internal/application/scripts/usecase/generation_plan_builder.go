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
package usecase

import (
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
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
	title := item.Title
	if title == "" {
		title = topic
	}
	if title == "" {
		title = "Untitled Script"
	}

	plan := scriptpkg.ResolvedGenerationPlan{
		ID:                  item.ID,
		Title:               title,
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
		NumClips:            item.Source.NumClips,
		SegmentWords:        item.ScriptParams.SegmentWords,
		SegmentTopics:       append([]string(nil), item.ScriptParams.SegmentTopics...),
		SentencesPerImage:   item.ScriptParams.SentencesPerImage,
		ImagesPerScene:      item.ScriptParams.ImagesPerScene,
		Style:               item.Style,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion,
		UseMemory:           item.ScriptParams.UseMemory,
		ForceRefresh:        item.ScriptParams.ForceRefresh,
		DriveFolderID:       item.Output.DriveFolderID,
		VoiceoverGroup:      item.Output.VoiceoverGroup,
		VoiceoverFolderID:   item.Output.VoiceoverFolderID,
		MaxChars:            item.Output.MaxChars,
		OutputFmt:           item.Output.OutputFmt,
		SaveToDB:            item.Output.SaveToDB,
		Languages:           append([]string(nil), item.Output.Languages...),
		// PR-TRANSLATE-SCRIPT-SPEC PR-5 (2026-07-09): thread
		// OutputSpec.TranslateTo into the resolved plan so the
		// TranslationProcessor reads a single source (the plan).
		TranslateTo: item.Output.TranslateTo,
		// FallbackPolicy controls whether clip-native generation may
		// fall back to prose when the model does not emit scenes.
		FallbackPolicy: item.Source.FallbackPolicy,
	}

	// Build postprocessor list from output flags.
	plan.Postprocessors = adapters.ProcessorNamesToStrings(buildPostprocessorList(item.Output))

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
// from OutputSpec flags.
//
// CRITICAL ordering (P0, June 2026): scene-normalisation stages
// (clip_bindings + stock_association) MUST run BEFORE artifact
// producers (voiceover, images, document) so the prose-fallback
// heuristic synthesised scenes are visible to voiceover/image
// renderers. Before this fix, voiceover and images ran with zero
// scenes when the LLM emitted prose-only output; clip_bindings
// synthesised scenes later but only document + persistence saw them.
//
// Order: entities → metadata → clip_bindings → stock_association →
// voiceover → images → document → persistence.
//
// clip_bindings is unconditional (no operator flag) because:
//   - It is BestEffort, so composition never fails on its absence.
//   - It is a no-op when plan.ClipEvidence is nil/empty (pure-text
//     generation paths), so including it in every plan is cheap.
//   - Conditional inclusion would expose a subtle drift: a plan with
//     clip evidence might skip the binder while one without it would
//     run it; both diverge silently. Unconditional inclusion makes
//     centralization tautological.
func buildPostprocessorList(out scriptpkg.OutputSpec) []adapters.ProcessorName {
	var pp []adapters.ProcessorName
	// SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-3 (TOGGLE-TRISTATE):
	// .AsBool() collapses Toggle tri-state → bool while preserving
	// caller-explicit ToggleDisabled (returns false) and treating
	// ToggleDefault + ToggleEnabled as enabled (saftey-default
	// fallback semantics). The unconditional clip_bindings +
	// stock_association are still append-before-flag-checks per
	// godlike/06 SSOT (PR-CLIP-SEARCH-WIRING rationale).
	if out.ExtractEntities.AsBool() {
		pp = append(pp, adapters.ProcessorEntities)
		// PR-CLIP-SEARCH-WIRING (July 2026): when entities are
		// extracted, also search for Artlist clips matching the
		// artlist_phrases. Must run AFTER entities (ordering
		// dependency: reads input.Entities.ArtlistPhrases).
		// BestEffort — nil backend is a warning, not a failure.
		pp = append(pp, adapters.ProcessorClipSearch)
	}
	if out.GenerateMetadata.AsBool() {
		pp = append(pp, adapters.ProcessorMetadata)
	}
	// PR-TRANSLATE-SCRIPT-SPEC PR-5 (2026-07-09): when caller sets
	// TranslateTo != "", append the TranslationProcessor between
	// metadata and clip_bindings (per the EXECUTION order documented
	// in processor_names.go goddoc) so the translated SpecScene is
	// visible to the downstream clip binder. Empty string is the
	// canonical "no translation requested" sentinel — the processor
	// is NOT appended (BestEffort + backwards-compat with the
	// pre-PR-5 silent-no-op behaviour). godlike/07 NO-FAKE-AVAILABILITY:
	// considering `out.TranslateTo != ""` is the SOLE trigger — the
	// legacy `len(out.Languages) > 0` fallback is intentionally NOT
	// consulted because callers that explicitly set Languages[]
	// without TranslateTo would silently incur an LLM cost per the
	// pre-PR-5 audit (false-positive "translation expected" derived
	// from stale caller intent).
	if strings.TrimSpace(out.TranslateTo) != "" {
		pp = append(pp, adapters.ProcessorTranslation)
	}
	// Scene-normalisation stages: MUST run before artifact producers
	// (voiceover, images, document) so prose-fallback synthesised
	// scenes are visible to downstream renderers.
	pp = append(pp, adapters.ProcessorClipBindings)
	// stock_association is unconditional (BestEffort, no-op when
	// Qdrant is unavailable). Runs after clip_bindings so it can
	// fall back to scene.Bindings.Clip.DriveLink.
	pp = append(pp, adapters.ProcessorStockAssociation)
	if out.GenerateVoiceover.AsBool() {
		pp = append(pp, adapters.ProcessorVoiceover)
	}
	if out.GenerateSceneImages.AsBool() {
		pp = append(pp, adapters.ProcessorImages)
	}
	if out.GenerateDocument.AsBool() {
		pp = append(pp, adapters.ProcessorDocument)
	}
	if out.SaveToDB {
		pp = append(pp, adapters.ProcessorPersistence)
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
		parts = append(parts, "Target words: "+strconv.Itoa(item.ScriptParams.TargetWords))
	}
	if item.ScriptParams.MinWords > 0 {
		parts = append(parts, "Min words: "+strconv.Itoa(item.ScriptParams.MinWords))
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
