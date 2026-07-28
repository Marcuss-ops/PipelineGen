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
		SourceText:          item.Source.SourceText,
		Guidelines:          item.Source.Guidelines,
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
	if item.Source.Type != scriptpkg.SourceClips &&
		item.Source.Type != scriptpkg.SourceSearch &&
		item.Source.Type != scriptpkg.SourceCatalog &&
		item.Source.Type != scriptpkg.SourceCurate {
		return processors
	}
	return ensureInlineClipArtifacts(processors)
}

// buildPostprocessorList builds the normal output-driven processor list.
func buildPostprocessorList(out scriptpkg.OutputSpec) []adapters.ProcessorName {
	var processors []adapters.ProcessorName
	extractEntities := out.ExtractEntities.AsBool()
	if extractEntities {
		processors = append(processors, adapters.ProcessorEntities, adapters.ProcessorClipSearch)
	}
	if extractEntities {
		// Internet images reuse the same per-segment extraction surface
		// as clip_search, so keep them in the same branch and let the
		// processor enforce its own provider toggle.
		processors = append(processors, adapters.ProcessorInternetImages)
	}
	if out.GenerateMetadata.AsBool() {
		processors = append(processors, adapters.ProcessorMetadata)
	}
	if strings.TrimSpace(out.TranslateTo) != "" {
		processors = append(processors, adapters.ProcessorTranslation)
	}

	// Scene-normalisation must precede artifact producers so voiceover and the
	// Google Doc consume the final translated, clip-bound SpecScene.
	processors = append(processors, adapters.ProcessorClipBindings)
	if out.StockEnabled == scriptpkg.ToggleEnabled || out.StockEnabled == scriptpkg.ToggleDisabled || len(out.StockBindings) > 0 {
		processors = append(processors, adapters.ProcessorStockBindings)
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
	parts = append(parts, "Do not include raw URLs, hyperlinks, or source citations in the prose output.")
	return strings.Join(parts, "\n\n")
}
