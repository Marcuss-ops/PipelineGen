package adapters

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── mergePostProcessResult: aggregate helper ──────────────────────────

// mergePostProcessResult copies non-zero fields from a processor
// result into the aggregate PipelineResult, and writes back the
// synthesised Scene slice into the registry-local ProcessInput so
// subsequent postprocessors see the populated input.SpecScene.Scenes
// (document/persistence stop reading empty scenes downstream of
// the prose-fallback clip-bindings heuristic).
//
// Issue #1 (June 2026): the canonical pipeline-level SpecScene
// surface lives on PipelineResult.FinalSpecScene. mergePostProcessResult
// captures the post-walk SpecScene after every processor (in
// last-writer-wins order — there's only ever one synthesizer at a
// time so a copy is sufficient) so buildGenerationResult reads the
// post-walk envelope via the empty-aware fallback in
// generate_one_usecase.go.
//
// currentInput is the by-value copy of the ProcessInput that Run()
// passes to processors; nil-safe so callers that pre-Issue-1 wiring
// (eg. in older tests) keep working.
//
// Each field is merged by a dedicated helper so the aggregate stays a
// flat, ordered list of last-writer-wins steps rather than one deeply
// nested function.
func mergePostProcessResult(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	mergeScalarIdentity(dst, src)
	mergeVisualPlans(dst, src)
	mergeVisualAssignments(dst, src, currentInput)
	mergeStageProgress(dst, src)
	// Concurrency safety: ProcessInput.SpecScene.Scenes may share its
	// backing array with the engine result (or with another concurrent
	// pipeline). Clone once before any in-place mutation so
	// postprocessors can write-back bindings without racing.
	cloneInputScenes(currentInput)
	mergeUpdatedSpecScene(dst, src, currentInput)
	mergeSpecSceneChanged(dst, src, currentInput)
	mergeEntities(dst, src, currentInput)
	mergeVidRushSegmentsInto(dst, src, currentInput)
	mergeMetadataInto(dst, src, currentInput)
	mergeVoiceovers(dst, src, currentInput)
	mergeSceneImages(dst, src, currentInput)
	mergeScriptID(dst, src)
	mergeSynthesizedScenes(dst, src, currentInput)
	mergeArtlistClipSuggestions(dst, src)
	// Issue #1 (June 2026) FINAL SURFACE. Capture the post-walk
	// SpecScene envelope so buildGenerationResult can read it instead
	// of the pre-walk engineResult.Output.SpecScene. Set unconditionally
	// (NOT inside the SynthesizedScenes branch) because the post-walk
	// envelope is meaningful even when no synthesizer ran: in that case
	// currentInput.SpecScene already mirrors engineResult.Output.SpecScene
	// and the downstream consumer's empty-aware fallback decides whether
	// to use it.
	captureFinalSpecScene(dst, currentInput)
	mergeWarnings(dst, src)
	mergeTranslated(dst, src, currentInput)
	mergeEffectiveLanguage(dst, src, currentInput)
}

func mergeScalarIdentity(dst *PipelineResult, src *PostProcessResult) {
	if strings.TrimSpace(src.DocID) != "" {
		dst.DocID = src.DocID
	}
	if strings.TrimSpace(src.DocLink) != "" {
		dst.DocLink = src.DocLink
	}
	if strings.TrimSpace(src.DocumentRenderer) != "" {
		dst.DocumentRenderer = src.DocumentRenderer
		dst.DocumentSpecSceneSHA256 = src.DocumentSpecSceneSHA256
		dst.DocumentSceneCount = src.DocumentSceneCount
		dst.DocumentLanguage = src.DocumentLanguage
	}
}

func mergeVisualPlans(dst *PipelineResult, src *PostProcessResult) {
	if len(src.VisualPlans) > 0 {
		dst.VisualPlans = append(dst.VisualPlans, src.VisualPlans...)
	}
}

func mergeVisualAssignments(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.VisualAssignments) == 0 {
		return
	}
	dst.VisualAssignments = append(dst.VisualAssignments, src.VisualAssignments...)
	if currentInput == nil {
		return
	}
	currentInput.SpecScene.VisualAssignments = append([]mediadomain.VisualAssignment(nil), src.VisualAssignments...)
	// Keep the scene-level clip binding and the independent timeline
	// contract in sync. Timeline post-segment clips are also the
	// primary clip for their narrative scene; the timeline still
	// remains authoritative when multiple clips share one scene.
	projectPostSegmentClipBindings(currentInput.SpecScene.Scenes, src.VisualAssignments)
	dst.FinalSpecScene = currentInput.SpecScene
}

func mergeStageProgress(dst *PipelineResult, src *PostProcessResult) {
	if len(src.StageProgress) == 0 {
		return
	}
	if dst.StageProgress == nil {
		dst.StageProgress = make(map[string]job.StageProgress)
	}
	for stage, progress := range src.StageProgress {
		dst.StageProgress[stage] = progress
	}
}

func cloneInputScenes(currentInput *ProcessInput) {
	if currentInput != nil {
		currentInput.SpecScene.Scenes = cloneSpecSceneSlice(currentInput.SpecScene.Scenes)
	}
}

func mergeUpdatedSpecScene(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.UpdatedSpecScene.Scenes) == 0 || currentInput == nil {
		return
	}
	previous := append([]scriptpkg.SpecScene(nil), currentInput.SpecScene.Scenes...)
	updated := src.UpdatedSpecScene
	updated.Scenes = preserveSceneBindings(previous, updated.Scenes)
	currentInput.SpecScene = updated
	dst.FinalSpecScene = updated
}

func mergeSpecSceneChanged(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if !src.SpecSceneChanged {
		return
	}
	dst.SpecSceneChanged = true
	if currentInput != nil {
		currentInput.SpecSceneChanged = true
	}
}

func mergeEntities(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if src.Entities == nil {
		return
	}
	dst.Entities = src.Entities
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026):
	// write-back to currentInput so the document processor
	// (which runs later in the registry) receives populated
	// entities instead of nil.
	if currentInput != nil {
		currentInput.Entities = src.Entities
	}
}

func mergeVidRushSegmentsInto(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.VidRushSegments) == 0 {
		return
	}
	dst.VidRushSegments = mergeVidRushSegments(dst.VidRushSegments, src.VidRushSegments)
	if currentInput != nil {
		currentInput.VidRushSegments = mergeVidRushSegments(currentInput.VidRushSegments, src.VidRushSegments)
	}
}

func mergeMetadataInto(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.Metadata) == 0 {
		return
	}
	dst.VideoMetadata = append(dst.VideoMetadata, src.Metadata...)
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026):
	// write-back to currentInput so the document processor
	// (which runs later in the registry) receives populated
	// metadata instead of nil.
	if currentInput != nil {
		currentInput.Metadata = append(currentInput.Metadata, src.Metadata...)
	}
}

func mergeVoiceovers(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.Voiceovers) == 0 {
		return
	}
	dst.Voiceovers = append(dst.Voiceovers, src.Voiceovers...)
	for _, v := range src.Voiceovers {
		if v.TimingArtifact == nil || strings.TrimSpace(v.Language) == "" || v.SceneIndex < 0 {
			continue
		}
		if dst.TimingArtifacts == nil {
			dst.TimingArtifacts = make(map[string]*capabilityaudio.SpeechTimingArtifact)
		}
		dst.TimingArtifacts[sceneTimingArtifactKey(v.Language, v.SceneIndex)] = v.TimingArtifact
	}
	if currentInput == nil {
		return
	}
	for _, v := range src.Voiceovers {
		if v.SceneIndex < 0 || v.SceneIndex >= len(currentInput.SpecScene.Scenes) {
			continue
		}
		sc := &currentInput.SpecScene.Scenes[v.SceneIndex]
		if sc.Bindings.Voiceover == nil {
			sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
		}
		applyVoiceoverBinding(sc.Bindings.Voiceover, v)
	}
}

func sceneTimingArtifactKey(language string, sceneIndex int) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(language)), sceneIndex)
}

func applyVoiceoverBinding(binding *scriptpkg.VoiceoverBinding, v SceneVoiceover) {
	language := strings.TrimSpace(v.Language)
	if language != "" && strings.TrimSpace(v.Link) != "" {
		if binding.Links == nil {
			binding.Links = make(map[string]string)
		}
		binding.Links[language] = v.Link
	}
	// Per-language timing bundle write-back. Populated for every
	// timing outcome (completed / unavailable / failed) so the
	// scene binding reflects the timing policy result, and never
	// erases a previously written language entry.
	if language != "" && v.Timing != nil {
		if binding.Timing == nil {
			binding.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding)
		}
		binding.Timing[language] = *v.Timing
	}
	// Keep the first successful language as the compatibility
	// default Link/LocalPath/Duration. Later language outcomes
	// remain available in Links without overwriting that default.
	if binding.Link == "" && strings.TrimSpace(v.Link) != "" {
		binding.Link = v.Link
	}
	if binding.LocalPath == "" && strings.TrimSpace(v.LocalPath) != "" {
		binding.LocalPath = v.LocalPath
	}
	if binding.DurationMs == 0 && v.DurationMs > 0 {
		binding.DurationMs = v.DurationMs
	}
	if binding.Status == "" || binding.Status == string(scriptpkg.VoiceoverStatusSkipped) {
		binding.Status = v.Status
	} else if v.Status == string(scriptpkg.VoiceoverStatusFailed) {
		// Any failed language must remain visible at the
		// scene aggregate even when another language completed.
		binding.Status = v.Status
	}
}

func mergeSceneImages(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.SceneImages) == 0 {
		return
	}
	dst.Scenes = append(dst.Scenes, src.SceneImages...)
	if currentInput == nil {
		return
	}
	for _, s := range src.SceneImages {
		if s.Index < 0 || s.Index >= len(currentInput.SpecScene.Scenes) {
			continue
		}
		sc := &currentInput.SpecScene.Scenes[s.Index]
		if sc.Bindings.Image == nil {
			sc.Bindings.Image = &scriptpkg.ImageBinding{}
		}
		applySceneImageBinding(sc.Bindings.Image, s)
	}
}

// applySceneImageBinding implements the PR-PROCESSOR-FAILCLOSED-IMG-BINDING
// (commit 7, July 2026) fail-closed bind rule. Only an implicitly-succeeded
// outcome (i.e. the SceneImage has a non-empty SceneImageDriveLink) promotes
// to "generated" with URL populated. Every other case (FAILED / SKIPPED /
// SUCCEEDED-without-link) terminates with Status="failed" and URL="" per
// godlike/07 NO-FAKE-AVAILABILITY: an empty URL is the honest answer for a
// non-promoted binding.
func applySceneImageBinding(binding *scriptpkg.ImageBinding, s SceneImage) {
	driveLink := SceneImageDriveLink(s)
	if strings.TrimSpace(driveLink) != "" {
		binding.URL = driveLink
		binding.Status = string(scriptpkg.ImageStatusGenerated)
	} else {
		binding.URL = ""
		binding.Status = string(scriptpkg.ImageStatusFailed)
	}
}

func mergeScriptID(dst *PipelineResult, src *PostProcessResult) {
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
		dst.AlreadyPersisted = src.AlreadyPersisted
	}
}

// mergeSynthesizedScenes applies FASE 3 (June 2026) prose-fallback
// clip_bindings last-wins semantics: only one processor synthesises scenes
// at a time, so a simple overwrite keeps the invariant simple.
func mergeSynthesizedScenes(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if len(src.SynthesizedScenes) == 0 {
		return
	}
	var prevScenes []scriptpkg.SpecScene
	if currentInput != nil {
		prevScenes = append([]scriptpkg.SpecScene(nil), currentInput.SpecScene.Scenes...)
	}
	dst.SynthesizedScenes = src.SynthesizedScenes
	// Scene synthesis may happen after local semantic extraction. Carry
	// annotations forward by stable segment/scene identity so the final
	// materialized scenes retain the spans computed from their text.
	if len(prevScenes) > 0 {
		carrySceneAnnotations(src.SynthesizedScenes, prevScenes)
		dst.SynthesizedScenes = src.SynthesizedScenes
	}
	// Issue #1 (June 2026) WRITE-BACK. The registry passes the same
	// `input` ProcessInput to every processor in the loop, so updating its
	// SpecScene.Scenes here means every subsequent processor (document,
	// persistence, voiceover, images) sees the synthesised bundle instead
	// of the original empty specscene.
	if currentInput != nil {
		// A synthesized scene may carry bindings produced by the
		// processor that emitted it (for example a locked visual clip),
		// while the previous scene surface may already carry stock,
		// subtitle, or other bindings. Merge by stable scene identity so
		// the write-back cannot erase either side of the contract.
		currentInput.SpecScene.Scenes = preserveSceneBindings(prevScenes, src.SynthesizedScenes)
	}
}

func carrySceneAnnotations(synthesized, previous []scriptpkg.SpecScene) {
	bySegment := make(map[string]*scriptpkg.SceneAnnotations, len(previous))
	byScene := make(map[string]*scriptpkg.SceneAnnotations, len(previous))
	for i := range previous {
		if previous[i].Annotations == nil {
			continue
		}
		if key := strings.TrimSpace(previous[i].SegmentID); key != "" {
			bySegment[key] = previous[i].Annotations
		}
		if key := strings.TrimSpace(previous[i].ID); key != "" {
			byScene[key] = previous[i].Annotations
		}
	}
	for i := range synthesized {
		if synthesized[i].Annotations != nil {
			synthesized[i].Annotations = rebaseSceneAnnotations(synthesized[i].Annotations, synthesized[i].Text)
			continue
		}
		if annotations := bySegment[strings.TrimSpace(synthesized[i].SegmentID)]; annotations != nil {
			synthesized[i].Annotations = rebaseSceneAnnotations(annotations, synthesized[i].Text)
		} else if annotations := byScene[strings.TrimSpace(synthesized[i].ID)]; annotations != nil {
			synthesized[i].Annotations = rebaseSceneAnnotations(annotations, synthesized[i].Text)
		} else if i < len(previous) {
			synthesized[i].Annotations = rebaseSceneAnnotations(previous[i].Annotations, synthesized[i].Text)
		}
	}
}

func mergeArtlistClipSuggestions(dst *PipelineResult, src *PostProcessResult) {
	// PR-CLIP-SEARCH-WIRING (July 2026): propagate Artlist clip
	// search results from the ClipSearchProcessor into the aggregate
	// pipeline result.
	if len(src.ArtlistClipSuggestions) > 0 {
		dst.ArtlistClipSuggestions = append(dst.ArtlistClipSuggestions, src.ArtlistClipSuggestions...)
	}
}

func captureFinalSpecScene(dst *PipelineResult, currentInput *ProcessInput) {
	if currentInput != nil {
		dst.FinalSpecScene = currentInput.SpecScene
	}
}

// mergeWarnings propagates the TranslationProcessor soft warnings into the
// aggregate result (PR-TRANSLATE-SCRIPT-SPEC PR-5/PR-6, 2026-07-09).
// Append mirrors the canonical Voiceovers / SceneImages / VideoMetadata
// direct-append pattern; per-Run the warning surface is bounded — there is
// only ever one translator per pipeline.
func mergeWarnings(dst *PipelineResult, src *PostProcessResult) {
	if len(src.Warnings) > 0 {
		dst.Warnings = append(dst.Warnings, src.Warnings...)
	}
}

// mergeTranslated propagates the canonical translated-text + translated-
// SpecScene fields (PR-TRANSLATE-SCRIPT-SPEC PR-6, 2026-07-09) from the
// per-stage result into the aggregate pipeline result, plus the original
// (pre-translation) surface. Last-writer-wins semantics: only one
// translator runs per Run.
func mergeTranslated(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if strings.TrimSpace(src.TranslatedText) != "" {
		dst.TranslatedText = src.TranslatedText
		// PR-TRANSLATION-PIPELINE-2026-07-09 WRITE-BACK: propagate
		// translated text into currentInput so downstream processors
		// (VoiceoverProcessor, DocumentProcessor) read the translated
		// content instead of the original.
		if currentInput != nil {
			currentInput.Text = src.TranslatedText
			currentInput.TranslatedText = src.TranslatedText
		}
	}
	if len(src.TranslatedSpecScene.Scenes) > 0 {
		dst.TranslatedSpecScene = src.TranslatedSpecScene
		// PR-TRANSLATION-PIPELINE-2026-07-09 WRITE-BACK: propagate
		// translated SpecScene into currentInput so downstream
		// processors see translated scene text, while retaining any
		// already-materialized clip/subtitle/voiceover bindings absent
		// from the translation result.
		if currentInput != nil {
			previous := append([]scriptpkg.SpecScene(nil), currentInput.SpecScene.Scenes...)
			translated := src.TranslatedSpecScene
			translated.Scenes = preserveSceneBindings(previous, translated.Scenes)
			currentInput.SpecScene = translated
			currentInput.TranslatedSpecScene = translated
		}
	}
	if strings.TrimSpace(src.OriginalText) != "" && currentInput != nil {
		if currentInput.OriginalText == "" {
			currentInput.OriginalText = src.OriginalText
			currentInput.OriginalSpecScene = src.OriginalSpecScene
		}
	}
}

func mergeEffectiveLanguage(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if strings.TrimSpace(src.EffectiveLanguage) == "" {
		return
	}
	dst.EffectiveLanguage = strings.TrimSpace(src.EffectiveLanguage)
	if currentInput != nil {
		currentInput.EffectiveLanguage = strings.TrimSpace(src.EffectiveLanguage)
	}
}

// reapplyTranslatedSceneText restores the translated narrative after a
// downstream processor synthesizes or normalizes scene slots from the
// original segment plan. Bindings are deliberately left untouched: this
// function owns only translated text/title fields.
func reapplyTranslatedSceneText(input *ProcessInput) {
	if input == nil || len(input.SpecScene.Scenes) == 0 {
		return
	}
	bySegment := make(map[string]scriptpkg.SpecScene, len(input.TranslatedSpecScene.Scenes)+len(input.OriginalSpecScene.Scenes))
	byID := make(map[string]scriptpkg.SpecScene, len(input.TranslatedSpecScene.Scenes)+len(input.OriginalSpecScene.Scenes))
	for _, scene := range input.TranslatedSpecScene.Scenes {
		if scene.SegmentID != "" {
			bySegment[scene.SegmentID] = scene
		}
		if scene.ID != "" {
			byID[scene.ID] = scene
		}
	}
	for _, scene := range input.OriginalSpecScene.Scenes {
		if scene.SegmentID != "" {
			if _, exists := bySegment[scene.SegmentID]; !exists {
				bySegment[scene.SegmentID] = scene
			}
		}
		if scene.ID != "" {
			if _, exists := byID[scene.ID]; !exists {
				byID[scene.ID] = scene
			}
		}
	}
	for i := range input.SpecScene.Scenes {
		var translated scriptpkg.SpecScene
		if input.SpecScene.Scenes[i].SegmentID != "" {
			translated = bySegment[input.SpecScene.Scenes[i].SegmentID]
		}
		if translated.Text == "" && input.SpecScene.Scenes[i].ID != "" {
			translated = byID[input.SpecScene.Scenes[i].ID]
		}
		if translated.Text == "" && i < len(input.TranslatedSpecScene.Scenes) {
			translated = input.TranslatedSpecScene.Scenes[i]
		}
		if translated.Text == "" && i < len(input.OriginalSpecScene.Scenes) {
			translated = input.OriginalSpecScene.Scenes[i]
		}
		if translated.Text != "" {
			input.SpecScene.Scenes[i].Text = translated.Text
		}
		if translated.Title != "" {
			input.SpecScene.Scenes[i].Title = translated.Title
		}
	}
}
