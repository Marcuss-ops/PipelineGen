// Package scripts — specscene_validator.go is the canonical
// ValidateAndEnrichSpecScene entry point. It runs BEFORE the
// postprocessor phase in GenerateOneUseCase.Execute and:
//  1. Rejects model-invented clip IDs (clip_id not in
//     ClipEvidence.AcceptedClipIDs — Issue #2, June 2026:
//     field renamed from ClipIDs).
//  2. Rejects kind=clip without a populated Clip binding.
//  3. Rejects kind=clip with empty clip_id.
//  4. Rejects invalid temporal range (start_ms < 0, end_ms < 0,
//     end_ms <= start_ms).
//  5. Rejects unknown ImageBinding.Status / VoiceoverBinding.Status
//     enum values.
//  6. Auto-enriches valid clip bindings: DriveLink from
//     ClipEvidence.DriveLinks[clip_id] + ClipTitle from a
//     canonical "Clip <truncated_id>" placeholder (clip metadata
//     lookup is a follow-up).
//  7. Accepts (with Warnings) describe-only narratives using the
//     same clip id twice and evidence partial-coverage cases
//     (model emits fewer scenes than the resolved evidence).
//
// PR 6 (June 2026): introduced to enforce compatibility between
// structure emitted by the model and the canonical clip IDs
// resolved by the source resolver. The pre-PR-6 path silently
// accepted model-invented clips, letting the downstream postprocess
// leg fail in non-obvious ways.
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ValidateAndEnrichSpecScene validates the model-emitted specscene
// against the resolved ClipEvidence and returns an enriched copy.
// The original engineResult.Output.SpecScene is NOT mutated.
//
// Returns:
//   - enriched *scriptpkg.SpecSceneOutput on success (auto-enriched
//     bindings, no BadSceneIndices)
//   - *scriptpkg.SpecSceneValidationError on rule violation
//   - nil, nil must never happen (the input *must* be non-nil per
//     caller contract)
//
// The function is pure: no I/O, no log emission.  Callers can run
// it from any goroutine.  Warnings are returned as a separate slice
// (not as a typed error) so a recovered scene never blocks the
// pipeline.
func ValidateAndEnrichSpecScene(
	ctx context.Context,
	output *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
) (*scriptpkg.SpecSceneOutput, []string, error) {
	_ = ctx // reserved for future cancellation

	if output == nil {
		return nil, nil, fmt.Errorf("ValidateAndEnrichSpecScene: output is nil")
	}
	if len(output.SpecScene.Scenes) == 0 {
		// Empty specscene is canonical for pure-prose generation.
		// Pass through unchanged.
		return &output.SpecScene, nil, nil
	}

	// Build a quick-lookup of allowed clip IDs (Issue #2, June
	// 2026: field renamed ClipIDs → AcceptedClipIDs).
	allowedClips := make(map[string]struct{}, len(evidence.AcceptedClipIDs))
	for _, id := range evidence.AcceptedClipIDs {
		allowedClips[strings.TrimSpace(id)] = struct{}{}
	}

	enriched := scriptpkg.SpecSceneOutput{
		Version: output.SpecScene.Version,
		Scenes:  make([]scriptpkg.SpecScene, len(output.SpecScene.Scenes)),
	}
	warnings := []string{}
	var badIndices []int
	var badKinds []scriptpkg.SceneKind

	for i, scene := range output.SpecScene.Scenes {
		// Copy the scene verbatim; we'll mutate bindings only.
		enriched.Scenes[i] = scene
		badBindings := false

		// Rule: kind=clip requires ClipID present + in evidence.
		if scene.Kind == scriptpkg.SceneClip {
			clipID := ""
			if scene.Bindings.Clip != nil {
				clipID = strings.TrimSpace(scene.Bindings.Clip.ClipID)
			}
			if clipID == "" {
				badBindings = true
				badIndices = append(badIndices, i)
				badKinds = append(badKinds, scene.Kind)
				warnings = append(warnings,
					fmt.Sprintf("scene[%d]: kind=clip but clip_id is empty", i))
				continue
			}
			if _, ok := allowedClips[clipID]; !ok && len(allowedClips) > 0 {
				badBindings = true
				badIndices = append(badIndices, i)
				badKinds = append(badKinds, scene.Kind)
				warnings = append(warnings,
					fmt.Sprintf("scene[%d]: clip_id %q not in resolved ClipEvidence", i, clipID))
				continue
			}
			// Auto-enrich: DriveLink + ClipTitle (placeholder).
			if scene.Bindings.Clip == nil {
				scene.Bindings.Clip = &scriptpkg.ClipBinding{}
			}
			if scene.Bindings.Clip.DriveLink == "" && evidence.DriveLinks != nil {
				if link, ok := evidence.DriveLinks[clipID]; ok {
					scene.Bindings.Clip.DriveLink = link
				}
			}
			if scene.Bindings.Clip.ClipTitle == "" {
				// PR 6 (June 2026): canonical placeholder until
				// clips metadata lookup ships. Truncated to 16
				// chars for operator-readability.
				t := clipID
				if len(t) > 16 {
					t = t[:16]
				}
				scene.Bindings.Clip.ClipTitle = "Clip " + t
			}
			enriched.Scenes[i] = scene
		}

		// Rule: narration kind does NOT require any binding. Mixed
		// kinds accept a clip + image binding under SceneMixed and
		// are validated per-binding below.
		_ = scene.Kind

		// Rule: invalid temporal range.
		if scene.Bindings.Clip != nil {
			startMs := scene.Bindings.Clip.StartMs
			endMs := scene.Bindings.Clip.EndMs
			if startMs != 0 || endMs != 0 {
				if startMs < 0 || endMs < 0 {
					badBindings = true
					warnings = append(warnings,
						fmt.Sprintf("scene[%d]: negative temporal range (start_ms=%d end_ms=%d)", i, startMs, endMs))
				} else if endMs <= startMs {
					badBindings = true
					warnings = append(warnings,
						fmt.Sprintf("scene[%d]: invalid temporal range (end_ms=%d <= start_ms=%d)", i, endMs, startMs))
				}
			}
			if badBindings && len(badIndices) == 0 || (len(badIndices) > 0 && badIndices[len(badIndices)-1] != i) {
				badIndices = append(badIndices, i)
				badKinds = append(badKinds, scene.Kind)
			}
		}

		// Rule: unknown image binding status.
		if scene.Bindings.Image != nil {
			st := scriptpkg.ImageBindingStatus(strings.TrimSpace(scene.Bindings.Image.Status))
			if st != "" && !st.Valid() {
				badBindings = true
				warnings = append(warnings,
					fmt.Sprintf("scene[%d]: unknown image binding status %q", i, scene.Bindings.Image.Status))
				if len(badIndices) == 0 || badIndices[len(badIndices)-1] != i {
					badIndices = append(badIndices, i)
					badKinds = append(badKinds, scene.Kind)
				}
			}
		}

		// Rule: unknown voiceover binding status.
		if scene.Bindings.Voiceover != nil {
			st := scriptpkg.VoiceoverBindingStatus(strings.TrimSpace(scene.Bindings.Voiceover.Status))
			if st != "" && !st.Valid() {
				badBindings = true
				warnings = append(warnings,
					fmt.Sprintf("scene[%d]: unknown voiceover binding status %q", i, scene.Bindings.Voiceover.Status))
				if len(badIndices) == 0 || badIndices[len(badIndices)-1] != i {
					badIndices = append(badIndices, i)
					badKinds = append(badKinds, scene.Kind)
				}
			}
		}
	}

	if len(badIndices) > 0 {
		return nil, warnings, &scriptpkg.SpecSceneValidationError{
			ItemID:          "", // populated by caller (item.ID)
			BadSceneIndices: dedupIntSlice(badIndices),
			SceneKindBad:    dedupKindSlice(badKinds),
			Reason: fmt.Sprintf("%d scene(s) failed validation (warnings: %v)",
				len(badIndices), warnings),
		}
	}

	// Soft warning: evidence has unused clip_ids (model emitted fewer
	// scenes than the resolved evidence). This is harmless but useful
	// for operator dashboards. Issue #2 (June 2026): evidence.ClipIDs
	// renamed to AcceptedClipIDs.
	if evidence != nil && len(evidence.AcceptedClipIDs) > len(output.SpecScene.Scenes) {
		warnings = append(warnings,
			fmt.Sprintf("model emitted %d scenes for %d resolved clips (unused = %d)",
				len(output.SpecScene.Scenes), len(evidence.AcceptedClipIDs),
				len(evidence.AcceptedClipIDs)-len(output.SpecScene.Scenes)))
	}
	// Soft warning: same clip_id used in 2+ scenes (operator-allowed).
	clipUseCount := make(map[string]int, len(output.SpecScene.Scenes))
	for _, scene := range output.SpecScene.Scenes {
		if scene.Bindings.Clip != nil && scene.Bindings.Clip.ClipID != "" {
			clipUseCount[scene.Bindings.Clip.ClipID]++
		}
	}
	for id, count := range clipUseCount {
		if count > 1 {
			warnings = append(warnings,
				fmt.Sprintf("clip_id %q used in %d scenes (duplicates allowed)", id, count))
		}
	}

	return &enriched, warnings, nil
}

// dedupIntSlice returns the input slice with duplicates removed and
// the order preserved. Used by ValidateAndEnrichSpecScene to
// compress per-scene rejections into a unique index list.
func dedupIntSlice(in []int) []int {
	if len(in) == 0 {
		return in
	}
	seen := make(map[int]struct{}, len(in))
	out := make([]int, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// dedupKindSlice returns the input slice with duplicates removed.
func dedupKindSlice(in []scriptpkg.SceneKind) []scriptpkg.SceneKind {
	if len(in) == 0 {
		return in
	}
	seen := make(map[scriptpkg.SceneKind]struct{}, len(in))
	out := make([]scriptpkg.SceneKind, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
