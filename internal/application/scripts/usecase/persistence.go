// Package usecase — persistence.go: result-building helpers.
//
// Extracted from generate_one_usecase_persist.go (July 2026).
// Owns: buildGenerationResult.
package usecase

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Persist-phase helpers ─────────────────────────────────────────────

// buildGenerationResult constructs a GenerationResult from the
// engine and postprocessor outputs. PR 13: populates ONLY the
// canonical nested fields (Output, Source, Cache, Artifacts).
// The deprecated flat fields were removed in PR 13.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	cacheHit := engineResult.CacheStatus == "exact_hit"

	// PR 5: ScriptID is sourced from postResult.ScriptID (set by
	// PersistenceProcessor), NOT from engineResult.ScriptID (which
	// no longer exists post-PR 5). When the persistence processor
	// is not in the plan's Postprocessors list, ScriptID is zero.
	scriptIDFromPostprocess := int64(0)
	if postResult != nil {
		scriptIDFromPostprocess = postResult.ScriptID
	}

	// Issue #1 (June 2026): prefer postResult.FinalSpecScene over
	// engineResult.Output.SpecScene when populated.
	specScene := engineResult.Output.SpecScene
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		specScene = postResult.FinalSpecScene
	}
	specScene = sanitizeSpecSceneOutputForResponse(specScene)

	// PR-TRANSLATION-PIPELINE-2026-07-09: prefer translated text over
	// the engine's original output when the TranslationProcessor
	// succeeded. Without this, the API response always shows the
	// original (e.g. English) text even when translation to Italian
	// was requested and completed.
	outputText := engineResult.Output.Text
	if postResult != nil && strings.TrimSpace(postResult.TranslatedText) != "" {
		outputText = postResult.TranslatedText
	}

	result := &scriptpkg.GenerationResult{
		ItemID:   item.ID,
		ScriptID: scriptIDFromPostprocess,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		// Sprint 1.3 (godlike/08): Status is the canonical per-item
		// outcome enum. It is NOT set here — the orchestrator's
		// classify phase (ClassifyGenerationStatus in status_classifier.go)
		// is the SOLE writer in the success path. Leaving Status empty
		// here keeps the build-phase a pure data-construction step and
		// lets the classifier apply the canonical
		// build → enforce → quality → warnings → classify → emit order.
		Output: scriptpkg.ScriptOutput{
			Text:      outputText,
			WordCount: engineResult.WordCount,
			SpecScene: specScene,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
		},
		Timings: timings,
	}

	// Populate Source trace.
	var sourceTrace scriptpkg.SourceTrace

	// PR 7 (June 2026): the model-emitted SpecScene goes through
	// the post-processor walk BEFORE this function is called.
	if engineResult.ClipEvidence != nil {
		clipIDs := engineResult.ClipEvidence.AcceptedClipIDs
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
		sourceTrace.AcceptedClipIDs = append([]string(nil), clipIDs...)
	}

	// Merge postprocessor results into canonical Artifacts.
	if postResult != nil {
		result.Artifacts.Entities = postResult.Entities
		if postResult.Entities != nil {
			if raw, err := SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
				result.Artifacts.EntitiesJSON = raw
			}
		}

		if len(postResult.VideoMetadata) > 0 {
			meta := make([]scriptpkg.VideoMetadata, len(postResult.VideoMetadata))
			for i, m := range postResult.VideoMetadata {
				meta[i] = scriptpkg.VideoMetadata{
					Language:          m.Language,
					Title:             m.Title,
					Description:       m.Description,
					Tags:              m.Tags,
					TranslationStatus: m.TranslationStatus,
				}
			}
			result.Artifacts.Metadata = meta
		}

		if len(postResult.Scenes) > 0 {
			for _, s := range postResult.Scenes {
				if s.Index < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[s.Index]
					if sc.Bindings.Image == nil {
						sc.Bindings.Image = &scriptpkg.ImageBinding{}
					}
					// PR-PROCESSOR-FAILCLOSED-IMG-BINDING (commit 7b,
					// July 2026): wave-coherence sync with commit 7
					// (postprocessor_composite_merge.go). Apply the same
					// fail-closed bind rule here.
					//
					// Pre-fix (and pre-fix pre-fix in commit 7): this
					// block UNCONDITIONALLY set `Status="generated"`
					// whenever the SceneImages buffer was non-empty,
					// producing false successes when the underlying
					// image URL / DriveFileID were empty (e.g. when the
					// per-scene image call returned no asset). The
					// API-response surface exposed by
					// buildGenerationResult propagated the same
					// false-success state to operators / dashboards.
					//
					// Post-fix (commit 7b): the same fail-closed proxy
					// rule — `strings.TrimSpace(SceneImageDriveLink(s)) != ""`
					// is the canonical "implicitly succeeded" signal on
					// the SceneImage struct (no per-image Status field
					// today). When DriveLink is empty (FAILED / SKIPPED
					// / SUCCEEDED-without-link), terminate with
					// Status="failed" + URL="" per godlike/07
					// NO-FAKE-AVAILABILITY (an empty URL is the honest
					// answer for a non-promoted binding). When
					// DriveLink is populated, promote to
					// Status="generated" + URL=<link>.
					//
					// Typed enum usage: scriptpkg.ImageStatusGenerated /
					// scriptpkg.ImageStatusFailed are the canonical
					// ownership surfaces for image-binding lifecycle
					// states (binding_status.go). godlike/06 SSOT —
					// string literals would re-introduce the magic-string
					// drift that the typed enum family eliminated.
					//
					// Future work: a commit wiring []SceneImageOutcome
					// through buildGenerationResult can replace this
					// proxy with the typed Status comparison
					// (outcome.Status == SceneImageSucceeded && ...) at
					// the same site — mirroring the same architectural
					// opportunity documented at
					// postprocessor_composite_merge.go.
					driveLink := adapters.SceneImageDriveLink(s)
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

		if len(postResult.Voiceovers) > 0 {
			for _, v := range postResult.Voiceovers {
				if v.SceneIndex < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[v.SceneIndex]
					if sc.Bindings.Voiceover == nil {
						sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
					}
					sc.Bindings.Voiceover.Status = v.Status
					sc.Bindings.Voiceover.Link = v.Link
				}
			}
		}

		if postResult.DocLink != "" {
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocLink: postResult.DocLink,
				DocID:   postResult.DocID,
				Status:  "completed",
			}
		}
	}

	if postResult != nil && len(postResult.Warnings) > 0 {
		result.Warnings = append(result.Warnings, postResult.Warnings...)
	}

	result.Source = sourceTrace
	return result
}

// ── Entity-result serializer (PR-LEGACY-CLEANUP-2026-07-10 Item 2) ──

// SerializeEntityResultRoundTrip preserves the typed entity result
// as JSON for legacy read-only compatibility. It never mutates the
// source of truth.
//
// godlike/06 SSOT: the helper used to live at
// internal/application/scripts/dto/compat_types.go as the canonical
// (export-named) SerializeEntityResultRoundTrip. After PR-LEGACY-
// CLEANUP-2026-07-10 Item 2 retired the entire dto/compat_types.go
// file + the PostProcessArtifact ` = any` alias, the helper moved
// here to preserve the export-named wire-shape contract.
//
// The single canonical caller in production code is
// buildGenerationResult in this file (call site at line ~92).
// The export name is preserved so that any future migration can
// reach the helper without changing the import path again; per the
// pre-Item-2 forensic rg, the prior dto-path had zero external
// callers, so the export is forward-looking contract stability,
// not backward-compat. If a future agent introduces a non-canonical
// caller, this goddoc must be updated to list the new caller.
func SerializeEntityResultRoundTrip(res *scriptpkg.EntityResult) (string, error) {
	if res == nil {
		return "", nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("serialize entity result: %w", err)
	}
	return string(raw), nil
}

// sanitizeSpecSceneOutputForResponse returns a deep copy of the
// canonical SpecSceneOutput with production-local voiceover paths
// stripped from the API-facing surface. The internal pipeline keeps
// LocalPath for downstream persistence and diagnostics; only the
// emitted result hides it.
func sanitizeSpecSceneOutputForResponse(in scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	if len(in.Scenes) == 0 {
		return in
	}

	out := in
	out.Scenes = append([]scriptpkg.SpecScene(nil), in.Scenes...)
	for i := range out.Scenes {
		img := out.Scenes[i].Bindings.Image
		if img != nil {
			imgCopy := *img
			imgCopy.LocalPath = ""
			out.Scenes[i].Bindings.Image = &imgCopy
		}
		vo := out.Scenes[i].Bindings.Voiceover
		if vo == nil {
			continue
		}
		voCopy := *vo
		voCopy.LocalPath = ""
		out.Scenes[i].Bindings.Voiceover = &voCopy
	}
	return out
}
