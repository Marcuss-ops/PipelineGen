// Package usecase builds the canonical execution plan for script.generate.
package usecase

import (
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildPlan converts one normalized generation item into the plan consumed by
// the engine and the ordered post-processing pipeline.
func BuildPlan(item scriptpkg.GenerationItemV2) scriptpkg.ResolvedGenerationPlan {
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
		MediaMode:           item.MediaMode,
		SourceText:          item.Source.SourceText,
		Guidelines:          editorialGuidelines(item),
		TargetWords:         item.ScriptParams.TargetWords,
		SingleScene:         item.ScriptParams.SingleScene,
		Duration:            item.ScriptParams.Duration,
		MinWords:            item.ScriptParams.MinWords,
		NumClips:            item.Source.NumClips,
		SegmentWords:        item.ScriptParams.SegmentWords,
		SegmentTopics:       append([]string(nil), item.ScriptParams.SegmentTopics...),
		Segments:            append([]scriptpkg.ScriptSegment(nil), item.ScriptParams.Segments...),
		SentencesPerImage:   item.ScriptParams.SentencesPerImage,
		ImagesPerScene:      item.ScriptParams.ImagesPerScene,
		Style:               item.Style,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion,
		UseMemory:           item.ScriptParams.UseMemory,
		ForceRefresh:        item.ScriptParams.ForceRefresh,
		DriveFolderID:       item.Output.DriveFolderID,
		DocsEnabled:         item.Docs.Enabled,
		DocsLanguages:       append([]string(nil), item.Docs.Languages...),
		DocsFolderID:        item.Docs.FolderID,
		VoiceoverGroup:      item.Output.VoiceoverGroup,
		VoiceoverFolderID:   item.Output.VoiceoverFolderID,
		MaxChars:            item.Output.MaxChars,
		OutputFmt:           item.Output.OutputFmt,
		SaveToDB:            item.Output.SaveToDB,
		StockEnabled:        item.Output.StockEnabled,
		StockBindings:       append([]scriptpkg.StockBindingInput(nil), item.Output.StockBindings...),
		Languages:           append([]string(nil), item.Output.Languages...),
		TranslateTo:         item.Output.TranslateTo,
		FallbackPolicy:      item.Source.FallbackPolicy,
		MediaPlan:           item.MediaPlan.Clone(),
		VideoMetadata:       scriptpkg.CloneVideoMetadata(item.VideoMetadata),
	}

	if plan.VideoMetadata != nil {
		if strings.TrimSpace(plan.VideoMetadata.Language) == "" {
			plan.VideoMetadata.Language = plan.Language
		}

		// Il client non deve controllare lo stato interno.
		plan.VideoMetadata.TranslationStatus = ""
	}

	plan.Postprocessors = adapters.ProcessorNamesToStrings(buildPostprocessorListForItem(item))
	plan.RenderedPrompt = buildEditorialPrompt(item)
	plan.SourceKind = string(item.Source.Type)
	plan.PromptProfile = "default-v1"
	return plan
}

// BuildPlans builds plans for an already-normalized envelope.
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

var modeBySource = map[scriptpkg.SourceType]string{
	scriptpkg.SourceText:    "text",
	scriptpkg.SourceClips:   "clip_to_script",
	scriptpkg.SourceCurate:  "clip_to_script",
	scriptpkg.SourceCatalog: "clip_to_script",
	scriptpkg.SourceSearch:  "clip_to_script",
}

const defaultEngineMode = "text"

func modeForSource(st scriptpkg.SourceType) string {
	if mode, ok := modeBySource[st]; ok {
		return mode
	}
	return defaultEngineMode
}

// mediaPlanRequested reports whether the caller configured an active media
// plan. An active plan has a non-empty, valid mode that is not disabled.
func mediaPlanRequested(item scriptpkg.GenerationItemV2) bool {
	return media.IsActiveMediaPlanMode(item.MediaPlan.Mode)
}

// insertProcessorAfterClipBindings places the given processor immediately
// after ProcessorClipBindings so the post-clip resolution pass sees the final
// clip-bound scenes before voiceover/persistence run.
func insertProcessorAfterClipBindings(processors []adapters.ProcessorName, proc adapters.ProcessorName) []adapters.ProcessorName {
	out := make([]adapters.ProcessorName, 0, len(processors)+1)
	for _, p := range processors {
		out = append(out, p)
		if p == adapters.ProcessorClipBindings {
			out = append(out, proc)
		}
	}
	return out
}

// buildPostprocessorListForItem is the source-aware post-processing resolver.
// Explicit clips are a complete generate job: translation (when requested),
// clip binding, voiceover, Google Doc creation and persistence all execute in
// this job. The unified POST /api/v1/script/generate endpoint with
// source.type: clips is the SOLE entry point for clip-based generation.
func buildPostprocessorListForItem(item scriptpkg.GenerationItemV2) []adapters.ProcessorName {
	out := item.Output
	if !out.ExtractEntities.AsBool() && item.MediaPlan.Extraction.Enabled {
		out.ExtractEntities = scriptpkg.ToggleEnabled
	}

	// Caller-provided metadata uses the existing metadata processor,
	// but the processor will return it directly without calling Ollama.
	if item.VideoMetadata != nil && item.VideoMetadata.HasContent() {
		out.GenerateMetadata = scriptpkg.ToggleEnabled
	}

	processors := buildPostprocessorList(out)
	if item.Docs.Enabled {
		processors = append(processors, adapters.ProcessorDocument)
	}
	if mediaPlanRequested(item) {
		// Active media plan: route through the new visual_planning processor
		// right after clip_bindings so it sees the final clip-bound scenes.
		processors = insertProcessorAfterClipBindings(processors, adapters.ProcessorVisualSlots)
		processors = insertProcessorAfterClipBindings(processors, adapters.ProcessorVisualPlanning)
	}
	if item.Source.Type == scriptpkg.SourceClips ||
		item.Source.Type == scriptpkg.SourceSearch ||
		item.Source.Type == scriptpkg.SourceCatalog ||
		item.Source.Type == scriptpkg.SourceCurate {
		// Explicit clip_only is an isolated media contract: it produces
		// clip bindings only and must not enqueue an unrelated voiceover
		// side effect that can fail on a missing voiceover destination.
		if item.MediaMode == scriptpkg.MediaModeClipOnly {
			processors = ensureInlineClipArtifactsWithoutVoiceover(processors)
		} else {
			processors = ensureInlineClipArtifacts(processors)
		}
	}
	// Asset reconciliation is the final binding gate. It must run after
	// every producer (clip/stock, visual media, images, and voiceover) and
	// immediately before persistence/document so both durable outputs see
	// the same verified SpecScene.
	return ensureAssetLocationReconciliation(processors)
}

// ensureAssetLocationReconciliation places the canonical Drive-link gate
// after all binding producers and before persistence/document. Keeping the
// terminal processors separate prevents a document or manifest from
// consuming the pre-reconciliation ProcessInput.
func ensureAssetLocationReconciliation(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+1)
	needsPersistence := false
	needsDocument := false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorAssetLocationReconciliation:
			// Reinsert at the canonical gate position below.
		case adapters.ProcessorPersistence:
			needsPersistence = true
		case adapters.ProcessorDocument:
			needsDocument = true
		default:
			result = append(result, processor)
		}
	}
	result = append(result, adapters.ProcessorAssetLocationReconciliation)
	if needsPersistence {
		result = append(result, adapters.ProcessorPersistence)
	}
	if needsDocument {
		result = append(result, adapters.ProcessorDocument)
	}
	return result
}

// buildPostprocessorList builds the normal output-driven processor list.
func buildPostprocessorList(out scriptpkg.OutputSpec) []adapters.ProcessorName {
	var processors []adapters.ProcessorName
	extractEntities := out.ExtractEntities.AsBool()
	// Translation must run before local semantic extraction so annotations,
	// spans, and keywords are aligned with the text sent to voiceover.
	if strings.TrimSpace(out.TranslateTo) != "" {
		processors = append(processors, adapters.ProcessorTranslation)
	}
	if extractEntities {
		processors = append(processors, adapters.ProcessorEntities, adapters.ProcessorClipSearch)
	}
	if extractEntities {
		// Discovery runs before scene binding. Final annotations and image
		// resolution are appended below, after the final scene text exists.
	}
	if out.GenerateMetadata.AsBool() {
		processors = append(processors, adapters.ProcessorMetadata)
	}
	// Scene-normalisation must precede artifact producers so voiceover and the
	// Google Doc consume the final translated, clip-bound SpecScene.
	processors = append(processors, adapters.ProcessorClipBindings)
	if out.StockEnabled == scriptpkg.ToggleEnabled || out.StockEnabled == scriptpkg.ToggleDisabled || len(out.StockBindings) > 0 {
		processors = append(processors, adapters.ProcessorStockBindings)
	}
	if extractEntities {
		processors = append(processors,
			// Re-run the canonical local entities processor after all scene
			// text normalization. Duplicate names are intentional: the first
			// pass feeds clip discovery; this pass owns final annotations.
			adapters.ProcessorEntities,
			adapters.ProcessorInternetImages,
			adapters.ProcessorVidRushMaterialization,
		)
	}
	if out.GenerateSceneImages.AsBool() {
		processors = append(processors, adapters.ProcessorImages)
	}

	if strings.TrimSpace(out.VoiceoverGroup) != "" || strings.TrimSpace(out.VoiceoverFolderID) != "" {
		processors = append(processors, adapters.ProcessorVoiceover)
	}
	if out.SaveToDB {
		processors = append(processors, adapters.ProcessorPersistence)
	}
	return processors
}

// ensureInlineClipArtifacts guarantees the canonical clips suffix while
// preserving every earlier processor and keeping persistence last.
func ensureInlineClipArtifacts(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+2)
	persist := false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorVoiceover:
			// Reinsert once below in canonical order.
		case adapters.ProcessorPersistence:
			persist = true
		default:
			result = append(result, processor)
		}
	}
	result = append(result, adapters.ProcessorVoiceover)
	if persist {
		result = append(result, adapters.ProcessorPersistence)
	}
	return result
}

// ensureInlineClipArtifactsWithoutVoiceover preserves the clip-source
// persistence ordering for an explicit clip_only request without adding an
// unrelated voiceover processor.
func ensureInlineClipArtifactsWithoutVoiceover(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+1)
	persist := false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorVoiceover:
			// Explicit clip_only has no voiceover side effect.
		case adapters.ProcessorPersistence:
			persist = true
		default:
			result = append(result, processor)
		}
	}
	if persist {
		result = append(result, adapters.ProcessorPersistence)
	}
	return result
}

func buildEditorialPrompt(item scriptpkg.GenerationItemV2) string {
	var parts []string
	if item.Source.Topic != "" {
		parts = append(parts, "Topic: "+item.Source.Topic)
	}
	if item.Source.SourceText != "" {
		parts = append(parts, "Source text:\n"+item.Source.SourceText)
	}
	if guidelines := editorialGuidelines(item); guidelines != "" {
		parts = append(parts, "Guidelines:\n"+guidelines)
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
	parts = append(parts, "Do not include raw URLs, hyperlinks, or source citations in the prose output.")
	return strings.Join(parts, "\n\n")
}

// editorialGuidelines bridges the two request surfaces that can carry
// editorial instructions. New script-generation requests use
// script_params.guidelines; source.guidelines remains valid for source
// resolvers and older callers. Keep both when they are supplied so segment
// isolation rules are not silently dropped while building the plan.
func editorialGuidelines(item scriptpkg.GenerationItemV2) string {
	seen := make(map[string]struct{}, 2)
	parts := make([]string, 0, 2)
	for _, raw := range []string{item.Source.Guidelines, item.ScriptParams.Guidelines} {
		guidelines := strings.TrimSpace(raw)
		if guidelines == "" {
			continue
		}
		if _, ok := seen[guidelines]; ok {
			continue
		}
		seen[guidelines] = struct{}{}
		parts = append(parts, guidelines)
	}
	return strings.Join(parts, "\n")
}
