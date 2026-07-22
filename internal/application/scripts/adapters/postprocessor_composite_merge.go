package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
	if len(src.VisualPlans) > 0 {
		dst.VisualPlans = append(dst.VisualPlans, src.VisualPlans...)
	}
	// P1 #10 (June 2026): record per-processor wall-clock timing.
	if dst.StageDurations == nil {
		dst.StageDurations = make(map[string]int64)
	}
	// Concurrency safety: ProcessInput.SpecScene.Scenes may share
	// its backing array with the engine result (or with another
	// concurrent pipeline). Clone once before any in-place mutation
	// so postprocessors can write-back bindings without racing.
	if currentInput != nil {
		currentInput.SpecScene.Scenes = cloneSpecSceneSlice(currentInput.SpecScene.Scenes)
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
				sc.Bindings.Voiceover.Status = v.Status
				sc.Bindings.Voiceover.Link = v.Link
				sc.Bindings.Voiceover.LocalPath = v.LocalPath
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
		}
	}
	if len(src.TranslatedSpecScene.Scenes) > 0 {
		dst.TranslatedSpecScene = src.TranslatedSpecScene
		// PR-TRANSLATION-PIPELINE-2026-07-09 WRITE-BACK:
		// propagate translated SpecScene into currentInput so
		// downstream processors see translated scene text.
		if currentInput != nil {
			currentInput.SpecScene = src.TranslatedSpecScene
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
