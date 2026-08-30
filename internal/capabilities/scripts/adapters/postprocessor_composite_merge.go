package adapters

import (
	"strings"

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

func preserveSceneBindings(previous, replacement []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if len(previous) == 0 || len(replacement) == 0 {
		return replacement
	}
	out := append([]scriptpkg.SpecScene(nil), replacement...)
	bySegment := make(map[string]int, len(previous))
	byID := make(map[string]int, len(previous))
	for i, scene := range previous {
		if key := strings.TrimSpace(scene.SegmentID); key != "" {
			bySegment[key] = i
		}
		if key := strings.TrimSpace(scene.ID); key != "" {
			byID[key] = i
		}
	}
	used := make(map[int]struct{}, len(previous))
	for i := range out {
		previousIndex := -1
		if key := strings.TrimSpace(out[i].SegmentID); key != "" {
			if index, ok := bySegment[key]; ok {
				previousIndex = index
			}
		} else if key := strings.TrimSpace(out[i].ID); key != "" {
			if index, ok := byID[key]; ok {
				previousIndex = index
			}
		} else if i < len(previous) {
			previousIndex = i
		}
		if previousIndex < 0 || previousIndex >= len(previous) {
			continue
		}
		if _, exists := used[previousIndex]; exists {
			continue
		}
		used[previousIndex] = struct{}{}
		if strings.TrimSpace(out[i].SegmentID) == "" {
			out[i].SegmentID = previous[previousIndex].SegmentID
		}
		if !out[i].Kind.Valid() || out[i].Kind == scriptpkg.SceneNarration {
			if previous[previousIndex].Kind.Valid() {
				out[i].Kind = previous[previousIndex].Kind
			}
		}
		out[i].Bindings = preserveBindings(previous[previousIndex].Bindings, out[i].Bindings)
		preserveResolvedEntityImages(&out[i], previous[previousIndex])
	}
	return out
}

// preserveResolvedEntityImages carries identity-image bindings across a
// later scene rewrite (for example materialization or document preparation).
// Those processors may return a fresh annotation slice without image fields;
// replacing it wholesale would silently turn a successfully materialized
// person image back into an unbound entity.
func preserveResolvedEntityImages(replacement *scriptpkg.SpecScene, previous scriptpkg.SpecScene) {
	if replacement == nil || previous.Annotations == nil || len(previous.Annotations.PrimaryEntities) == 0 {
		return
	}
	if replacement.Annotations == nil {
		return
	}
	previousImages := make(map[string]scriptpkg.EntityImageBinding)
	for _, entity := range previous.Annotations.PrimaryEntities {
		if entity.Image == nil || strings.TrimSpace(entity.Image.Status) != "resolved" {
			continue
		}
		key := normalizeEntityMatch(entity.CanonicalName)
		if key == "" {
			key = normalizeEntityMatch(entity.Text)
		}
		if key != "" {
			previousImages[key] = *entity.Image
		}
	}
	if len(previousImages) == 0 {
		return
	}
	for i := range replacement.Annotations.PrimaryEntities {
		entity := &replacement.Annotations.PrimaryEntities[i]
		if entity.Image != nil && strings.TrimSpace(entity.Image.Status) == "resolved" {
			continue
		}
		key := normalizeEntityMatch(entity.CanonicalName)
		if key == "" {
			key = normalizeEntityMatch(entity.Text)
		}
		if image, ok := previousImages[key]; ok {
			copy := image
			entity.Image = &copy
		}
	}
}

func preserveBindings(previous, replacement scriptpkg.SceneBindings) scriptpkg.SceneBindings {
	previous = cloneSceneBindings(previous)
	replacement = cloneSceneBindings(replacement)
	if replacement.Stock == nil {
		replacement.Stock = previous.Stock
	}
	if len(replacement.Media) == 0 {
		replacement.Media = append([]scriptpkg.ResolvedMediaBinding(nil), previous.Media...)
	}

	switch {
	case len(replacement.Clips) > 0:
		usedPrevious := make(map[int]struct{}, len(replacement.Clips))
		for i := range replacement.Clips {
			previousIndex := -1
			if replacement.Clips[i].ClipID != "" {
				for j := range previous.Clips {
					if previous.Clips[j].ClipID == replacement.Clips[i].ClipID {
						previousIndex = j
						break
					}
				}
			}
			if previousIndex < 0 && i < len(previous.Clips) {
				previousIndex = i
			}
			if previousIndex >= 0 {
				usedPrevious[previousIndex] = struct{}{}
				replacement.Clips[i] = mergeClipBinding(previous.Clips[previousIndex], replacement.Clips[i])
			}
		}
		// Translation and other scene replacements are often partial. Keep
		// previously materialized clips that were not mentioned by the
		// replacement so a multi-clip scene cannot collapse to one entry.
		for i := range previous.Clips {
			if _, exists := usedPrevious[i]; !exists {
				replacement.Clips = append(replacement.Clips, previous.Clips[i])
			}
		}
	case replacement.Clip != nil:
		// A legacy replacement containing only Clip must not collapse an
		// already-materialized multi-clip scene to one entry.
		if len(previous.Clips) > 0 {
			replacement.Clips = append([]scriptpkg.ClipBinding(nil), previous.Clips...)
			for i := range replacement.Clips {
				if replacement.Clips[i].ClipID == replacement.Clip.ClipID {
					replacement.Clips[i] = mergeClipBinding(previous.Clips[i], *replacement.Clip)
					break
				}
			}
		} else {
			replacement.Clips = []scriptpkg.ClipBinding{*replacement.Clip}
		}
	default:
		replacement.Clips = append([]scriptpkg.ClipBinding(nil), previous.Clips...)
	}
	if len(replacement.Clips) > 0 {
		replacement.Clip = &replacement.Clips[0]
	}

	if previous.Voiceover != nil {
		if replacement.Voiceover == nil {
			replacement.Voiceover = previous.Voiceover
		} else {
			if replacement.Voiceover.Status == "" {
				replacement.Voiceover.Status = previous.Voiceover.Status
			}
			if replacement.Voiceover.Link == "" {
				replacement.Voiceover.Link = previous.Voiceover.Link
			}
			if replacement.Voiceover.LocalPath == "" {
				replacement.Voiceover.LocalPath = previous.Voiceover.LocalPath
			}
			if replacement.Voiceover.DurationMs == 0 {
				replacement.Voiceover.DurationMs = previous.Voiceover.DurationMs
			}
			if len(previous.Voiceover.Links) > 0 {
				links := make(map[string]string, len(previous.Voiceover.Links)+len(replacement.Voiceover.Links))
				for language, link := range previous.Voiceover.Links {
					links[language] = link
				}
				for language, link := range replacement.Voiceover.Links {
					links[language] = link
				}
				replacement.Voiceover.Links = links
			}
			// Per-language timing bundles are additive state: previously
			// published timing links must survive a partial replacement
			// (translation / synthesis / reconciliation). The replacement
			// entry wins per language, but never erases a language that the
			// replacement did not touch.
			if len(previous.Voiceover.Timing) > 0 {
				timing := make(map[string]scriptpkg.VoiceoverTimingBinding, len(previous.Voiceover.Timing)+len(replacement.Voiceover.Timing))
				for language, entry := range previous.Voiceover.Timing {
					timing[language] = entry
				}
				for language, entry := range replacement.Voiceover.Timing {
					timing[language] = entry
				}
				replacement.Voiceover.Timing = timing
			}
		}
	}
	return replacement
}

func mergeClipBinding(previous, replacement scriptpkg.ClipBinding) scriptpkg.ClipBinding {
	if replacement.ClipID == "" {
		replacement.ClipID = previous.ClipID
	}
	if replacement.ClipTitle == "" {
		replacement.ClipTitle = previous.ClipTitle
	}
	if replacement.DriveLink == "" {
		replacement.DriveLink = previous.DriveLink
	}
	if replacement.SubtitleLink == "" {
		replacement.SubtitleLink = previous.SubtitleLink
	}
	if replacement.SubtitleFileID == "" {
		replacement.SubtitleFileID = previous.SubtitleFileID
	}
	if replacement.StartMs == 0 && replacement.EndMs == 0 {
		replacement.StartMs = previous.StartMs
		replacement.EndMs = previous.EndMs
	}
	if replacement.DurationMs == 0 {
		replacement.DurationMs = previous.DurationMs
	}
	return replacement
}

func mergeVidRushSegments(dst, src []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(dst)+len(src))
	for _, seg := range dst {
		if seg.SegmentID == "" {
			out = append(out, seg)
			continue
		}
		if i, exists := index[seg.SegmentID]; exists {
			out[i] = mergeVidRushSegmentResult(out[i], seg)
			continue
		}
		index[seg.SegmentID] = len(out)
		out = append(out, seg)
	}
	for _, seg := range src {
		if seg.SegmentID == "" {
			out = append(out, seg)
			continue
		}
		if i, ok := index[seg.SegmentID]; ok {
			out[i] = mergeVidRushSegmentResult(out[i], seg)
			continue
		}
		index[seg.SegmentID] = len(out)
		out = append(out, seg)
	}
	return out
}

// mergeVidRushSegmentResult preserves provider discoveries when a later
// processor returns only its own asset delta (for example, internet_images
// returning an image candidate after clip_search returned Artlist clips).
// Segment identity is shared, but provider assets are additive state and
// must not be replaced by the last processor to touch the segment.
func mergeVidRushSegmentResult(dst, src scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := cloneVidRushSegmentResult(dst)
	if src.SceneID != "" {
		out.SceneID = src.SceneID
	}
	if src.Text != "" {
		out.Text = src.Text
	}
	if src.TextHash != "" {
		out.TextHash = src.TextHash
	}
	if len(src.Insights.Entities) > 0 {
		out.Insights.Entities = append([]scriptpkg.ExtractedEntity(nil), src.Insights.Entities...)
	}
	if len(src.Insights.ImportantPhrases) > 0 {
		out.Insights.ImportantPhrases = append([]string(nil), src.Insights.ImportantPhrases...)
	}
	if len(src.Insights.ImportantWords) > 0 {
		out.Insights.ImportantWords = append([]string(nil), src.Insights.ImportantWords...)
	}
	if len(src.Insights.ArtlistQueries) > 0 {
		out.Insights.ArtlistQueries = append([]string(nil), src.Insights.ArtlistQueries...)
	}
	if len(src.Insights.ImageQueries) > 0 {
		out.Insights.ImageQueries = append([]string(nil), src.Insights.ImageQueries...)
	}
	out.Assets.Candidates = appendProviderCandidatesUnique(out.Assets.Candidates, src.Assets.Candidates)
	out.Assets.SecondaryImages = appendProviderCandidatesUnique(out.Assets.SecondaryImages, src.Assets.SecondaryImages)
	out.Assets.GeneratedImages = appendProviderCandidatesUnique(out.Assets.GeneratedImages, src.Assets.GeneratedImages)
	if src.Assets.PrimaryVideo != nil {
		primary := *src.Assets.PrimaryVideo
		out.Assets.PrimaryVideo = &primary
	}
	if src.Assets.SelectionReason != "" {
		out.Assets.SelectionReason = src.Assets.SelectionReason
	}
	if src.Assets.CandidateSetHash != "" {
		out.Assets.CandidateSetHash = src.Assets.CandidateSetHash
	}
	if src.Cache.Extraction != "" {
		out.Cache.Extraction = src.Cache.Extraction
	}
	if src.Cache.Artlist != "" {
		out.Cache.Artlist = src.Cache.Artlist
	}
	if src.Cache.InternetImages != "" {
		out.Cache.InternetImages = src.Cache.InternetImages
	}
	if src.Cache.ImageGeneration != "" {
		out.Cache.ImageGeneration = src.Cache.ImageGeneration
	}
	if src.Cache.Binding != "" {
		out.Cache.Binding = src.Cache.Binding
	}
	return out
}

// cloneSpecSceneSlice returns a shallow copy of the scene slice.
// Bindings is a value struct, so copying the slice element is enough
// to isolate in-place binding mutations from the original backing array.
func cloneSpecSceneSlice(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if scenes == nil {
		return nil
	}
	out := make([]scriptpkg.SpecScene, len(scenes))
	copy(out, scenes)
	return out
}
