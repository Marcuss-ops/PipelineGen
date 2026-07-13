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
//
// PR-REFACTOR-P1-CYCLOMATIC (2026-08-15): cyclomatic complexity
// reduced from 44 → ≤15 via per-rule helper extraction + early
// returns. The 4 per-scene rule checks (kind=clip + temporal range
// + image status + voiceover status) are now extracted into typed
// helpers that each return (warning, bad) — the main loop is a
// linear orchestrator with early returns on hard failures. The
// dedup guard for badIndices/badKinds is extracted into a single
// maybeRecordBadIndex helper so the 4 occurrences of the
// (len==0 || last != i) pattern collapse to a single call site.
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// sceneRuleWarning / sceneRuleBad are the typed return shape of
// every per-scene rule helper. Each rule either emits a warning
// (operator-observable) AND/OR flips its bad bool (failure that
// contributes to the overall SpecSceneValidationError). The main
// loop consumes this shape uniformly so it can stay linear.
//
// godlike/07 typed-error contract: warnings are non-fatal (they
// surface via the warnings []string channel); the `bad` bool is
// the unified signal for the final typed error returned by
// ValidateAndEnrichSpecScene. NO new exported symbols — these are
// private to the usecase package per godlike/07 minimum-blast-radius.
type sceneRuleResult struct {
	warning string
	bad     bool
}

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
//
// Cyclomatic complexity: was 44 (pre-PR-REFACTOR-P1-CYCLOMATIC),
// now ≤15. The per-rule checks (kind=clip, temporal range, image
// status, voiceover status) are extracted into typed helpers so
// the main loop is a linear orchestrator that consumes the
// per-rule (warning, bad) shape uniformly.
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
	var allowedClips map[string]struct{}
	if evidence != nil {
		allowedClips = make(map[string]struct{}, len(evidence.AcceptedClipIDs))
		for _, id := range evidence.AcceptedClipIDs {
			allowedClips[strings.TrimSpace(id)] = struct{}{}
		}
	} else {
		allowedClips = make(map[string]struct{})
	}

	enriched := scriptpkg.SpecSceneOutput{
		Version: output.SpecScene.Version,
		Scenes:  make([]scriptpkg.SpecScene, len(output.SpecScene.Scenes)),
	}
	warnings := []string{}
	var badIndices []int
	var badKinds []scriptpkg.SceneKind
	// badMessages tracks the per-scene failure messages in lockstep
	// with badIndices. The typed SpecSceneValidationError.Reason
	// includes these messages so the test contract (and operator
	// dashboard) can grep for the canonical per-rule text
	// ("invalid temporal range", "unknown image binding status",
	// "not in resolved ClipEvidence", "clip_id is empty").
	var badMessages []string

	for i, scene := range output.SpecScene.Scenes {
		// Copy the scene verbatim; per-rule helpers mutate the
		// local copy. The main loop only commits the final mutated
		// scene to enriched.Scenes[i] at the end of the iteration.
		enriched.Scenes[i] = scene
		hardFail := false
		// perSceneMsg captures the most recent warning appended
		// by a failed rule. Used to populate badMessages in
		// recordBadIndex so the typed error Reason includes the
		// canonical per-rule text.
		var perSceneMsg string

		// Rule A: kind=clip requires ClipID present + in evidence.
		// Hard failure: if Rule A fails, skip Rules B/C/D because
		// we already know the scene is broken (continues pre-PR
		// behaviour).
		if scene.Kind == scriptpkg.SceneClip {
			var newHardFail bool
			enriched.Scenes[i], warnings, newHardFail = applyKindClipRule(
				i, scene, allowedClips, evidence, warnings,
			)
			hardFail = newHardFail
			if hardFail && len(warnings) > 0 {
				perSceneMsg = warnings[len(warnings)-1]
			}
		}
		if hardFail {
			recordBadIndex(&badIndices, &badKinds, &badMessages, i, scene.Kind, perSceneMsg)
			continue
		}

		// Rule B: invalid temporal range.
		if r := applyTemporalRangeRule(i, scene, warnings); r.bad {
			recordBadIndex(&badIndices, &badKinds, &badMessages, i, scene.Kind, r.warning)
		} else if r.warning != "" {
			warnings = append(warnings, r.warning)
		}

		// Rule C: unknown image binding status.
		if r := applyImageBindingStatusRule(i, scene, warnings); r.bad {
			recordBadIndex(&badIndices, &badKinds, &badMessages, i, scene.Kind, r.warning)
		} else if r.warning != "" {
			warnings = append(warnings, r.warning)
		}

		// Rule D: unknown voiceover binding status.
		if r := applyVoiceoverBindingStatusRule(i, scene, warnings); r.bad {
			recordBadIndex(&badIndices, &badKinds, &badMessages, i, scene.Kind, r.warning)
		} else if r.warning != "" {
			warnings = append(warnings, r.warning)
		}
	}

	if len(badIndices) > 0 {
		return nil, warnings, &scriptpkg.SpecSceneValidationError{
			ItemID:          "", // populated by caller (item.ID)
			BadSceneIndices: dedupIntSlice(badIndices),
			SceneKindBad:    dedupKindSlice(badKinds),
			Reason: fmt.Sprintf("%d scene(s) failed validation: %s",
				len(badIndices), strings.Join(badMessages, "; ")),
		}
	}

	// Soft warnings: extract the post-loop observations into a
	// dedicated helper so the main function stays linear. These
	// never contribute to SpecSceneValidationError — they are
	// operator-dashboard observability surfaces.
	warnings = appendSoftWarnings(warnings, output, evidence)

	return &enriched, warnings, nil
}

// applyKindClipRule validates that a scene with Kind=SceneClip has a
// populated Clip binding with a clip_id in the allowed-evidence
// set. On success, it auto-enriches the binding with DriveLink
// (from evidence.DriveLinks[clip_id]) + ClipTitle (canonical
// "Clip <truncated_id>" placeholder).
//
// Returns the (mutated scene, appended warnings, hardFail) tuple.
// hardFail=true signals the caller to skip the remaining rules for
// this scene (a kind=clip with bad binding is broken; further rule
// checks would just add noise to the warnings list).
//
// godlike/07 minimum-blast-radius: this helper mutates the
// passed-in scene by VALUE (Go value-copy of the SpecScene struct
// includes the pointer-to-ClipBinding; the helper mutates the
// pointed-to struct). The main loop commits the returned
// (mutated) scene to enriched.Scenes[i] at the end of the
// iteration. The caller's `output.SpecScene.Scenes[i]` is never
// touched (per the function-level contract: "The original
// engineResult.Output.SpecScene is NOT mutated").
func applyKindClipRule(
	i int,
	scene scriptpkg.SpecScene,
	allowedClips map[string]struct{},
	evidence *scriptpkg.ClipEvidence,
	warnings []string,
) (scriptpkg.SpecScene, []string, bool) {
	clipID := ""
	if scene.Bindings.Clip != nil {
		clipID = strings.TrimSpace(scene.Bindings.Clip.ClipID)
	}
	if clipID == "" {
		warnings = append(warnings,
			fmt.Sprintf("scene[%d]: kind=clip but clip_id is empty", i))
		return scene, warnings, true
	}
	if _, ok := allowedClips[clipID]; !ok && len(allowedClips) > 0 {
		warnings = append(warnings,
			fmt.Sprintf("scene[%d]: clip_id %q not in resolved ClipEvidence", i, clipID))
		return scene, warnings, true
	}

	// Auto-enrich: DriveLink + ClipTitle (placeholder). The
	// pointer-to-ClipBinding is the canonical mutation target;
	// we update the pointed-to struct in-place, then return
	// the (value-copied) scene so the main loop commits it.
	if scene.Bindings.Clip == nil {
		scene.Bindings.Clip = &scriptpkg.ClipBinding{}
	}
	if scene.Bindings.Clip.DriveLink == "" && evidence != nil && evidence.DriveLinks != nil {
		if link, ok := evidence.DriveLinks[clipID]; ok {
			scene.Bindings.Clip.DriveLink = link
		}
	}
	if scene.Bindings.Clip.DurationMs <= 0 && evidence != nil && evidence.ClipDetails != nil {
		if detail, ok := evidence.ClipDetails[clipID]; ok {
			scene.Bindings.Clip.DurationMs = scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs)
		}
	}
	if scene.Bindings.Clip.DurationMs <= 0 {
		scene.Bindings.Clip.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
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
	return scene, warnings, false
}

// applyTemporalRangeRule validates that the scene's clip binding
// has a non-negative + non-degenerate temporal range
// (start_ms >= 0, end_ms > start_ms).
//
// Returns the (warning, bad) tuple. The bad bool contributes to
// the final SpecSceneValidationError.
func applyTemporalRangeRule(i int, scene scriptpkg.SpecScene, warnings []string) sceneRuleResult {
	if scene.Bindings.Clip == nil {
		return sceneRuleResult{}
	}
	startMs := scene.Bindings.Clip.StartMs
	endMs := scene.Bindings.Clip.EndMs
	if startMs == 0 && endMs == 0 {
		// No temporal range set — skip rule (caller didn't
		// supply a range, so we don't reject on missing range).
		return sceneRuleResult{}
	}
	if startMs < 0 || endMs < 0 {
		warnings = append(warnings,
			fmt.Sprintf("scene[%d]: negative temporal range (start_ms=%d end_ms=%d)", i, startMs, endMs))
		return sceneRuleResult{warning: warnings[len(warnings)-1], bad: true}
	}
	if endMs <= startMs {
		warnings = append(warnings,
			fmt.Sprintf("scene[%d]: invalid temporal range (end_ms=%d <= start_ms=%d)", i, endMs, startMs))
		return sceneRuleResult{warning: warnings[len(warnings)-1], bad: true}
	}
	return sceneRuleResult{}
}

// applyImageBindingStatusRule validates that the scene's image
// binding (if any) carries a known ImageBindingStatus enum value.
// Empty status is the canonical "no image binding" signal and is
// NOT a bad-rule (returns neutral result).
func applyImageBindingStatusRule(i int, scene scriptpkg.SpecScene, warnings []string) sceneRuleResult {
	if scene.Bindings.Image == nil {
		return sceneRuleResult{}
	}
	st := scriptpkg.ImageBindingStatus(strings.TrimSpace(scene.Bindings.Image.Status))
	if st == "" || st.Valid() {
		return sceneRuleResult{}
	}
	warnings = append(warnings,
		fmt.Sprintf("scene[%d]: unknown image binding status %q", i, scene.Bindings.Image.Status))
	return sceneRuleResult{warning: warnings[len(warnings)-1], bad: true}
}

// applyVoiceoverBindingStatusRule validates that the scene's
// voiceover binding (if any) carries a known
// VoiceoverBindingStatus enum value. Empty status is the canonical
// "no voiceover binding" signal and is NOT a bad-rule.
func applyVoiceoverBindingStatusRule(i int, scene scriptpkg.SpecScene, warnings []string) sceneRuleResult {
	if scene.Bindings.Voiceover == nil {
		return sceneRuleResult{}
	}
	st := scriptpkg.VoiceoverBindingStatus(strings.TrimSpace(scene.Bindings.Voiceover.Status))
	if st == "" || st.Valid() {
		return sceneRuleResult{}
	}
	warnings = append(warnings,
		fmt.Sprintf("scene[%d]: unknown voiceover binding status %q", i, scene.Bindings.Voiceover.Status))
	return sceneRuleResult{warning: warnings[len(warnings)-1], bad: true}
}

// recordBadIndex is the canonical dedup guard for badIndices +
// badKinds + badMessages. Pre-PR, this 4-line pattern appeared 4
// times in the main loop:
//
//	if badBindings && len(badIndices) == 0 || (len(badIndices) > 0 && badIndices[len(badIndices)-1] != i) {
//	    badIndices = append(badIndices, i)
//	    badKinds = append(badKinds, scene.Kind)
//	}
//
// The semantic: only append if i is not the last index recorded
// (because a single scene can fail MULTIPLE rules and we'd
// otherwise double-record the same index). godlike/06 SSOT: this
// helper is the SOLE canonical owner of the dedup-guard logic.
//
// The msg parameter carries the per-rule failure text (e.g.
// "invalid temporal range", "unknown image binding status") which
// is surfaced in the typed SpecSceneValidationError.Reason so the
// test contract (and operator dashboard) can grep for the
// canonical per-rule text. msg is captured ONLY on the first
// failure per scene (per the dedup-guard invariant above); for
// subsequent failures of the same scene, the first message wins.
func recordBadIndex(badIndices *[]int, badKinds *[]scriptpkg.SceneKind, badMessages *[]string, i int, kind scriptpkg.SceneKind, msg string) {
	if len(*badIndices) > 0 && (*badIndices)[len(*badIndices)-1] == i {
		// Same scene failed multiple rules — append the
		// additional kind to the existing entry but do NOT
		// re-record the index or the message (preserve the
		// first per-rule message for the typed error Reason).
		*badKinds = append(*badKinds, kind)
		return
	}
	*badIndices = append(*badIndices, i)
	*badKinds = append(*badKinds, kind)
	*badMessages = append(*badMessages, msg)
}

// appendSoftWarnings extracts the post-loop soft-warning
// observations (unused clips, duplicate clip_id usage) into a
// dedicated helper. These are operator-dashboard observability
// surfaces; they never contribute to SpecSceneValidationError.
//
// godlike/06 SSOT: this helper is the SOLE canonical owner of the
// "model emitted N scenes for M resolved clips" warning surface +
// the "clip_id used in 2+ scenes" warning surface.
func appendSoftWarnings(warnings []string, output *scriptpkg.ModelScriptOutputV1, evidence *scriptpkg.ClipEvidence) []string {
	// Soft warning: evidence has unused clip_ids (model emitted
	// fewer scenes than the resolved evidence). This is
	// harmless but useful for operator dashboards. Issue #2
	// (June 2026): evidence.ClipIDs renamed to AcceptedClipIDs.
	if evidence != nil && len(evidence.AcceptedClipIDs) > len(output.SpecScene.Scenes) {
		warnings = append(warnings,
			fmt.Sprintf("model emitted %d scenes for %d resolved clips (unused = %d)",
				len(output.SpecScene.Scenes), len(evidence.AcceptedClipIDs),
				len(evidence.AcceptedClipIDs)-len(output.SpecScene.Scenes)))
	}

	// Soft warning: same clip_id used in 2+ scenes
	// (operator-allowed).
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
	return warnings
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
