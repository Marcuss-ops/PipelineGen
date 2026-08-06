// Package generation owns the canonical plan-builder implementation during
// the WAVE-21 EXPAND/BACKFILL migration. The legacy usecase implementation is
// intentionally retained until parity is complete and CUTOVER is approved.
package generation

import (
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildPlan converts one normalized generation item into the plan consumed by
// the engine and ordered post-processing pipeline.
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
		ID: item.ID, Title: title, Topic: topic, Language: item.Language,
		Tone: item.Tone, Model: item.Model, Mode: scriptpkg.ModeForSource(item.Source.Type),
		MediaMode: item.MediaMode, SourceText: item.Source.SourceText,
		Guidelines: editorialGuidelines(item), TargetWords: item.ScriptParams.TargetWords,
		SingleScene: item.ScriptParams.SingleScene, Duration: item.ScriptParams.Duration,
		MinWords: item.ScriptParams.MinWords, NumClips: item.Source.NumClips,
		SegmentWords:      item.ScriptParams.SegmentWords,
		SegmentTopics:     append([]string(nil), item.ScriptParams.SegmentTopics...),
		Segments:          append([]scriptpkg.ScriptSegment(nil), item.ScriptParams.Segments...),
		SentencesPerImage: item.ScriptParams.SentencesPerImage,
		ImagesPerScene:    item.ScriptParams.ImagesPerScene, Style: item.Style,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion, UseMemory: item.ScriptParams.UseMemory,
		ForceRefresh: item.ScriptParams.ForceRefresh, DriveFolderID: item.Output.DriveFolderID,
		DocsEnabled: item.Docs.Enabled, DocsLanguages: append([]string(nil), item.Docs.Languages...),
		DocsFolderID: item.Docs.FolderID, VoiceoverGroup: item.Output.VoiceoverGroup,
		VoiceoverFolderID: item.Output.VoiceoverFolderID, MaxChars: item.Output.MaxChars,
		OutputFmt: item.Output.OutputFmt, SaveToDB: item.Output.SaveToDB,
		StockEnabled:  item.Output.StockEnabled,
		StockBindings: append([]scriptpkg.StockBindingInput(nil), item.Output.StockBindings...),
		Languages:     append([]string(nil), item.Output.Languages...), TranslateTo: item.Output.TranslateTo,
		FallbackPolicy: item.Source.FallbackPolicy, MediaPlan: item.MediaPlan.Clone(),
		VideoMetadata: scriptpkg.CloneVideoMetadata(item.VideoMetadata),
	}
	if plan.VideoMetadata != nil {
		if strings.TrimSpace(plan.VideoMetadata.Language) == "" {
			plan.VideoMetadata.Language = plan.Language
		}
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

func mediaPlanRequested(item scriptpkg.GenerationItemV2) bool {
	return media.IsActiveMediaPlanMode(item.MediaPlan.Mode)
}

func insertProcessorAfterClipBindings(processors []adapters.ProcessorName, proc adapters.ProcessorName) []adapters.ProcessorName {
	out := make([]adapters.ProcessorName, 0, len(processors)+1)
	for _, processor := range processors {
		out = append(out, processor)
		if processor == adapters.ProcessorClipBindings {
			out = append(out, proc)
		}
	}
	return out
}

func buildPostprocessorListForItem(item scriptpkg.GenerationItemV2) []adapters.ProcessorName {
	out := item.Output
	if !out.ExtractEntities.AsBool() && item.MediaPlan.Extraction.Enabled {
		out.ExtractEntities = scriptpkg.ToggleEnabled
	}
	if item.VideoMetadata != nil && item.VideoMetadata.HasContent() {
		out.GenerateMetadata = scriptpkg.ToggleEnabled
	}
	processors := buildPostprocessorList(out)
	if item.Docs.Enabled {
		processors = append(processors, adapters.ProcessorDocument)
	}
	if mediaPlanRequested(item) {
		processors = insertProcessorAfterClipBindings(processors, adapters.ProcessorVisualSlots)
		processors = insertProcessorAfterClipBindings(processors, adapters.ProcessorVisualPlanning)
	}
	if item.Source.Type == scriptpkg.SourceClips || item.Source.Type == scriptpkg.SourceSearch || item.Source.Type == scriptpkg.SourceCatalog || item.Source.Type == scriptpkg.SourceCurate {
		if item.MediaMode == scriptpkg.MediaModeClipOnly {
			processors = ensureInlineClipArtifactsWithoutVoiceover(processors)
		} else {
			processors = ensureInlineClipArtifacts(processors)
		}
	}
	return ensureAssetLocationReconciliation(processors)
}

func ensureAssetLocationReconciliation(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+1)
	needsPersistence, needsDocument := false, false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorAssetLocationReconciliation:
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

func buildPostprocessorList(output scriptpkg.OutputSpec) []adapters.ProcessorName {
	var processors []adapters.ProcessorName
	extractEntities := output.ExtractEntities.AsBool()
	if strings.TrimSpace(output.TranslateTo) != "" {
		processors = append(processors, adapters.ProcessorTranslation)
	}
	if extractEntities {
		processors = append(processors, adapters.ProcessorEntities, adapters.ProcessorClipSearch)
	}
	if output.GenerateMetadata.AsBool() {
		processors = append(processors, adapters.ProcessorMetadata)
	}
	processors = append(processors, adapters.ProcessorClipBindings)
	if output.StockEnabled == scriptpkg.ToggleEnabled || output.StockEnabled == scriptpkg.ToggleDisabled || len(output.StockBindings) > 0 {
		processors = append(processors, adapters.ProcessorStockBindings)
	}
	if extractEntities {
		processors = append(processors, adapters.ProcessorEntities, adapters.ProcessorInternetImages, adapters.ProcessorVidRushMaterialization)
	}
	if output.GenerateSceneImages.AsBool() {
		processors = append(processors, adapters.ProcessorImages)
	}
	if strings.TrimSpace(output.VoiceoverGroup) != "" || strings.TrimSpace(output.VoiceoverFolderID) != "" {
		processors = append(processors, adapters.ProcessorVoiceover)
	}
	if output.SaveToDB {
		processors = append(processors, adapters.ProcessorPersistence)
	}
	return processors
}

func ensureInlineClipArtifacts(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+2)
	persist := false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorVoiceover:
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

func ensureInlineClipArtifactsWithoutVoiceover(processors []adapters.ProcessorName) []adapters.ProcessorName {
	result := make([]adapters.ProcessorName, 0, len(processors)+1)
	persist := false
	for _, processor := range processors {
		switch processor {
		case adapters.ProcessorVoiceover:
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

func editorialGuidelines(item scriptpkg.GenerationItemV2) string {
	seen := make(map[string]struct{}, 2)
	parts := make([]string, 0, 2)
	for _, raw := range []string{item.Source.Guidelines, item.ScriptParams.Guidelines} {
		guidelines := strings.TrimSpace(raw)
		if guidelines == "" {
			continue
		}
		if _, exists := seen[guidelines]; exists {
			continue
		}
		seen[guidelines] = struct{}{}
		parts = append(parts, guidelines)
	}
	return strings.Join(parts, "\n")
}
