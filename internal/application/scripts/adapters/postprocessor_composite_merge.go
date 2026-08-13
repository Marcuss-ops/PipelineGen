package adapters

import (
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// P1 #10 (June 2026) wall-clock timing — keep the per-processor
// StageDurations map hot so the outer use case can stream it into
// GenerationTimings.PostprocessMs (canonical Issue #3 plumbing).
//
// currentInput is the by-value copy of the ProcessInput that Run()
// passes to processors; nil-safe so callers that pre-Issue-1 wiring
// (eg. in older tests) keep working.
func mergePostProcessResult(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	if strings.TrimSpace(src.DocID) != "" {
		dst.DocID = src.DocID
	}
	if strings.TrimSpace(src.DocLink) != "" {
		dst.DocLink = src.DocLink
	}
	if len(src.VisualPlans) > 0 {
		dst.VisualPlans = append(dst.VisualPlans, src.VisualPlans...)
	}
	if len(src.VisualAssignments) > 0 {
		dst.VisualAssignments = append(dst.VisualAssignments, src.VisualAssignments...)
		if currentInput != nil {
			currentInput.SpecScene.VisualAssignments = append([]mediadomain.VisualAssignment(nil), src.VisualAssignments...)
			// Keep the scene-level clip binding and the independent timeline
			// contract in sync. Timeline post-segment clips are also the
			// primary clip for their narrative scene; the timeline still
			// remains authoritative when multiple clips share one scene.
			projectPostSegmentClipBindings(currentInput.SpecScene.Scenes, src.VisualAssignments)
			dst.FinalSpecScene = currentInput.SpecScene
		}
	}
	// P1 #10 (June 2026): record per-processor wall-clock timing.
	if dst.StageDurations == nil {
		dst.StageDurations = make(map[string]int64)
	}
	if len(src.StageProgress) > 0 {
		if dst.StageProgress == nil {
			dst.StageProgress = make(map[string]job.StageProgress)
		}
		for stage, progress := range src.StageProgress {
			dst.StageProgress[stage] = progress
		}
	}
	// Concurrency safety: ProcessInput.SpecScene.Scenes may share
	// its backing array with the engine result (or with another
	// concurrent pipeline). Clone once before any in-place mutation
	// so postprocessors can write-back bindings without racing.
	if currentInput != nil {
		currentInput.SpecScene.Scenes = cloneSpecSceneSlice(currentInput.SpecScene.Scenes)
	}
	if len(src.UpdatedSpecScene.Scenes) > 0 && currentInput != nil {
		previous := append([]scriptpkg.SpecScene(nil), currentInput.SpecScene.Scenes...)
		updated := src.UpdatedSpecScene
		updated.Scenes = preserveSceneBindings(previous, updated.Scenes)
		currentInput.SpecScene = updated
		dst.FinalSpecScene = updated
	}
	if src.SpecSceneChanged {
		dst.SpecSceneChanged = true
		if currentInput != nil {
			currentInput.SpecSceneChanged = true
		}
	}
	if src.Entities != nil {
		dst.Entities = src.Entities
		// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026):
		// write-back to currentInput so the document processor
		// (which runs later in the registry) receives populated
		// entities instead of nil.
		if currentInput != nil {
			currentInput.Entities = src.Entities
		}
	}
	if len(src.VidRushSegments) > 0 {
		dst.VidRushSegments = mergeVidRushSegments(dst.VidRushSegments, src.VidRushSegments)
		if currentInput != nil {
			currentInput.VidRushSegments = mergeVidRushSegments(currentInput.VidRushSegments, src.VidRushSegments)
		}
	}
	if len(src.Metadata) > 0 {
		dst.VideoMetadata = append(dst.VideoMetadata, src.Metadata...)
		// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026):
		// write-back to currentInput so the document processor
		// (which runs later in the registry) receives populated
		// metadata instead of nil.
		if currentInput != nil {
			currentInput.Metadata = append(currentInput.Metadata, src.Metadata...)
		}
	}
	if len(src.Voiceovers) > 0 {
		dst.Voiceovers = append(dst.Voiceovers, src.Voiceovers...)
		if currentInput != nil {
			for _, v := range src.Voiceovers {
				if v.SceneIndex < 0 || v.SceneIndex >= len(currentInput.SpecScene.Scenes) {
					continue
				}
				sc := &currentInput.SpecScene.Scenes[v.SceneIndex]
				if sc.Bindings.Voiceover == nil {
					sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
				}
				binding := sc.Bindings.Voiceover
				language := strings.TrimSpace(v.Language)
				if language != "" && strings.TrimSpace(v.Link) != "" {
					if binding.Links == nil {
						binding.Links = make(map[string]string)
					}
					binding.Links[language] = v.Link
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
		}
	}
	if len(src.SceneImages) > 0 {
		dst.Scenes = append(dst.Scenes, src.SceneImages...)
		if currentInput != nil {
			for _, s := range src.SceneImages {
				if s.Index < 0 || s.Index >= len(currentInput.SpecScene.Scenes) {
					continue
				}
				sc := &currentInput.SpecScene.Scenes[s.Index]
				if sc.Bindings.Image == nil {
					sc.Bindings.Image = &scriptpkg.ImageBinding{}
				}
				// PR-PROCESSOR-FAILCLOSED-IMG-BINDING (commit 7,
				// July 2026): fail-closed bind rule. Only an
				// implicitly-succeeded outcome (i.e. the SceneImage
				// has a non-empty SceneImageDriveLink) promotes to
				// "generated" with URL populated. Every other case
				// (FAILED / SKIPPED / SUCCEEDED-without-link) terminates
				// with Status="failed" and URL=""
				// (godlike/07 NO-FAKE-AVAILABILITY: an empty URL
				// is the honest answer for a non-promoted binding;
				// pre-fix this block UNCONDITIONALLY set
				// Status="generated" whenever SceneImages was
				// populated, which produced false successes when
				// the underlying image was empty / Drive-deferred).
				//
				// Architecture note: src.SceneImages is the
				// stream-surface ([]SceneImage) emitted today by
				// (*ImageProcessor).Process, NOT the typed
				// []SceneImageOutcome introduced in Commit 6.
				// The fail-closed rule therefore uses the canonical
				// proxy "non-empty DriveLink" for "implicitly
				// succeeded". A future commit wiring
				// []SceneImageOutcome through the merge can replace
				// this proxy with the typed Status comparison
				// (outcome.Status == SceneImageSucceeded && ...)
				// at the same site (the user spec writes this
				// pseudocode; we honor its INTENT here).
				//
				// Out-of-scope: the same bug pre-exists at
				// internal/application/scripts/usecase/persistence.go
				// line ~121 (buildGenerationResult); the user spec
				// scoped this Commit 7 to postprocessor_composite_merge.go
				// only, so persistence.go is documented as a
				// wave follow-up rather than touched here.
				driveLink := SceneImageDriveLink(s)
				if strings.TrimSpace(driveLink) != "" {
					sc.Bindings.Image.URL = driveLink
					sc.Bindings.Image.Status = string(scriptpkg.ImageStatusGenerated)
				} else {
					sc.Bindings.Image.URL = ""
					sc.Bindings.Image.Status = string(scriptpkg.ImageStatusFailed)
				}
			}
		}
	}
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
		dst.AlreadyPersisted = src.AlreadyPersisted
	}
	// FASE 3 (June 2026): prose-fallback clip_bindings emits
	// SynthesizedScenes. Last-wins semantics: only one processor
	// synthesises scenes at a time, so a simple overwrite keeps the
	// invariant simple.
	if len(src.SynthesizedScenes) > 0 {
		var prevScenes []scriptpkg.SpecScene
		if currentInput != nil {
			prevScenes = append([]scriptpkg.SpecScene(nil), currentInput.SpecScene.Scenes...)
		}
		dst.SynthesizedScenes = src.SynthesizedScenes
		// Scene synthesis may happen after local semantic extraction. Carry
		// annotations forward by stable segment/scene identity so the final
		// materialized scenes retain the spans computed from their text.
		if len(prevScenes) > 0 {
			bySegment := make(map[string]*scriptpkg.SceneAnnotations, len(prevScenes))
			byScene := make(map[string]*scriptpkg.SceneAnnotations, len(prevScenes))
			for i := range prevScenes {
				if prevScenes[i].Annotations == nil {
					continue
				}
				if key := strings.TrimSpace(prevScenes[i].SegmentID); key != "" {
					bySegment[key] = prevScenes[i].Annotations
				}
				if key := strings.TrimSpace(prevScenes[i].ID); key != "" {
					byScene[key] = prevScenes[i].Annotations
				}
			}
			for i := range src.SynthesizedScenes {
				if src.SynthesizedScenes[i].Annotations != nil {
					src.SynthesizedScenes[i].Annotations = rebaseSceneAnnotations(src.SynthesizedScenes[i].Annotations, src.SynthesizedScenes[i].Text)
					continue
				}
				if annotations := bySegment[strings.TrimSpace(src.SynthesizedScenes[i].SegmentID)]; annotations != nil {
					src.SynthesizedScenes[i].Annotations = rebaseSceneAnnotations(annotations, src.SynthesizedScenes[i].Text)
				} else if annotations := byScene[strings.TrimSpace(src.SynthesizedScenes[i].ID)]; annotations != nil {
					src.SynthesizedScenes[i].Annotations = rebaseSceneAnnotations(annotations, src.SynthesizedScenes[i].Text)
				} else if i < len(prevScenes) {
					src.SynthesizedScenes[i].Annotations = rebaseSceneAnnotations(prevScenes[i].Annotations, src.SynthesizedScenes[i].Text)
				}
			}
			dst.SynthesizedScenes = src.SynthesizedScenes
		}
		// Issue #1 (June 2026) WRITE-BACK. The registry passes
		// the same `input` ProcessInput to every processor in
		// the loop, so updating its SpecScene.Scenes here means
		// every subsequent processor (document, persistence,
		// voiceover, images) sees the synthesised bundle instead
		// of the original empty specscene. Without this the
		// prose-fallback heuristic could declare success
		// (PipelineResult.SynthesizedScenes populated +
		// IsEmpty == false) while downstream processors still
		// received an envelope with empty SpecScene.Scenes —
		// document got an empty storyboard, persistence stored
		// an empty SpecScene row.
		if currentInput != nil {
			currentInput.SpecScene.Scenes = src.SynthesizedScenes
			for i := range currentInput.SpecScene.Scenes {
				if i >= len(prevScenes) {
					break
				}
				currentInput.SpecScene.Scenes[i].Bindings = prevScenes[i].Bindings
			}
		}
	}
	// PR-CLIP-SEARCH-WIRING (July 2026): propagate Artlist clip
	// search results from the ClipSearchProcessor into the aggregate
	// pipeline result.
	if len(src.ArtlistClipSuggestions) > 0 {
		dst.ArtlistClipSuggestions = append(dst.ArtlistClipSuggestions, src.ArtlistClipSuggestions...)
	}
	// Issue #1 (June 2026) FINAL SURFACE. Capture the post-walk
	// SpecScene envelope so buildGenerationResult can read it
	// instead of the pre-walk engineResult.Output.SpecScene.
	// Set unconditionally (NOT inside the SynthesizedScenes
	// branch) because the post-walk envelope is meaningful even
	// when no synthesizer ran: in that case currentInput.SpecScene
	// already mirrors engineResult.Output.SpecScene and the
	// downstream consumer's empty-aware fallback decides whether
	// to use it.
	if currentInput != nil {
		dst.FinalSpecScene = currentInput.SpecScene
	}
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): propagate the
	// canonical translated-text + translated-SpecScene fields
	// from the per-stage result into the aggregate pipeline
	// result. Last-writer-wins semantics, mirroring the
	// SynthesizedScenes pattern above: there is only ever one
	// translator running per Run, so a simple overwrite keeps
	// the invariant simple. godlike/07 NO-FAKE-AVAILABILITY: the
	// empty-string / nil-Scenes guards ensure pre-translation
	// pipeline runs (which may carry the same TranslateTo=""'')
	// don't accidentally overwrite a previously-set translated
	// surface.
	// PR-TRANSLATE-SCRIPT-SPEC PR-5/PR-6 (2026-07-09) WARNINGS
	// PROPAGATION FIX: the TranslationProcessor emits soft warnings
	// via the warnings []string slot on TranslateScriptSpec (the
	// typed-warning contract is "warnings are signals, errors are
	// stops"; the ErrTranslationEqualToSource sentinel surfaces
	// here). Pre-fix: src.Warnings never bubbled to dst.Warnings so
	// the aggregate pipeline result lost the per-stage observation
	// surface (operator dashboards could not count translation
	// soft-fails at the API response level). Post-fix: append
	// (mirrors the canonical Voiceovers / SceneImages /
	// VideoMetadata direct-append pattern; per-Run the warning
	// surface is bounded — there is only ever one translator per
	// pipeline, so duplicate-aggregation is not a real concern).
	if len(src.Warnings) > 0 {
		dst.Warnings = append(dst.Warnings, src.Warnings...)
	}
	if strings.TrimSpace(src.TranslatedText) != "" {
		dst.TranslatedText = src.TranslatedText
		// PR-TRANSLATION-PIPELINE-2026-07-09 WRITE-BACK:
		// propagate translated text into currentInput so
		// downstream processors (VoiceoverProcessor, DocumentProcessor)
		// read the translated content instead of the original.
		// Without this, VoiceoverProcessor generates TTS from
		// the original (English) text instead of the translated
		// (Italian) text.
		if currentInput != nil {
			currentInput.Text = src.TranslatedText
			currentInput.TranslatedText = src.TranslatedText
		}
	}
	if len(src.TranslatedSpecScene.Scenes) > 0 {
		dst.TranslatedSpecScene = src.TranslatedSpecScene
		// PR-TRANSLATION-PIPELINE-2026-07-09 WRITE-BACK:
		// propagate translated SpecScene into currentInput so
		// downstream processors see translated scene text, while
		// retaining any already-materialized clip/subtitle/voiceover
		// bindings absent from the translation result.
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
	if strings.TrimSpace(src.EffectiveLanguage) != "" {
		dst.EffectiveLanguage = strings.TrimSpace(src.EffectiveLanguage)
		if currentInput != nil {
			currentInput.EffectiveLanguage = strings.TrimSpace(src.EffectiveLanguage)
		}
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
		out[i].Bindings = preserveBindings(previous[previousIndex].Bindings, out[i].Bindings)
	}
	return out
}

func preserveBindings(previous, replacement scriptpkg.SceneBindings) scriptpkg.SceneBindings {
	previous = cloneSceneBindings(previous)
	replacement = cloneSceneBindings(replacement)

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
	out := append([]scriptpkg.VidRushSegmentResult(nil), dst...)
	for i, seg := range out {
		if seg.SegmentID == "" {
			continue
		}
		index[seg.SegmentID] = i
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
