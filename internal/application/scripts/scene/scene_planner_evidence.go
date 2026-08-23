// internal/application/scripts/scene/scene_planner_evidence.go —
// clip-evidence scene narration (PlanFromClipEvidence) + evidence
// text cleaning. Extracted from scene_planner.go; no behavior
// change.
package scene

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PlanFromClipEvidence deterministically constructs one SpecScene
// per accepted clip using the clip's transcript, description and
// metadata as primary evidence. Wave 1.1 promotes this from the
// binder-internal `buildScenesFromClipEvidence` to a planner-owned
// method so the binder can route through the planner without
// re-implementing the evidence narration.
//
// godlike/06 SSOT: this method is the canonical clip-evidence
// scene builder — NO other file may construct SpecScenes from
// ClipEvidence directly. The pre-Phase-2 binder-internal helper
// `buildScenesFromClipEvidence` is preserved verbatim in body
// (no behavior change) so the W1.1 commit is byte-stable.
//
// Ordering: matches plan.ClipEvidence.AcceptedClipIDs AND respects
// plan.NumClips as the upper bound on constructed scenes.
//
// Kind assignment: intro / clip / outro by position when the
// scene count is >=3; otherwise every scene is SceneClip.
//
// Bindings: every scene receives a *ClipBinding carrying the
// evidence metadata (name, drive link, start/end ms, duration.
// Empty ClipDetails fall back to ClipNames/DriveLinks for the
// metadata-only path (preserves pre-Phase-2 legacy compatibility).
func (p *ScenePlanner) PlanFromClipEvidence(
	plan *scriptpkg.ResolvedGenerationPlan,
) []scriptpkg.SpecScene {
	if plan == nil || plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return nil
	}

	ev := plan.ClipEvidence
	clipIDs := ev.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	scenes := make([]scriptpkg.SpecScene, len(clipIDs))
	for i, clipID := range clipIDs {
		detail, ok := ev.ClipDetails[clipID]
		if !ok {
			// Legacy fallback: synthesize a ClipDetail from the
			// assembled evidence maps so the metadata fields
			// surface even when ClipDetails is not populated.
			detail = scriptpkg.ClipDetail{
				Name:      ev.ClipNames[clipID],
				DriveLink: ev.DriveLinks[clipID],
			}
		}

		text := cleanClipNarrativeText(detail.Transcript)
		if text == "" {
			text = cleanClipNarrativeText(detail.Description)
		}
		if text == "" {
			text = detail.Name
		}
		if text == "" {
			text = fmt.Sprintf("Scene %d", i+1)
		}

		kind := scriptpkg.SceneClip
		if len(clipIDs) >= 3 {
			if i == 0 {
				kind = scriptpkg.SceneIntro
			} else if i == len(clipIDs)-1 {
				kind = scriptpkg.SceneOutro
			}
		}

		// ClipBinding.DurationMs is the canonical segment-
		// duration surface; populated here via
		// scriptpkg.ClipDurationMs (PURE canonical helper) plus
		// the canonical caller pattern's
		// scriptpkg.ClipDurationMsFromAssetID fallback for the
		// zero-delta branch (returns 0 by godlike/07
		// NO-FAKE-AVAILABILITY; "duration unknown").
		binding := &scriptpkg.ClipBinding{
			ClipID:          clipID,
			ClipTitle:       detail.Name,
			DriveLink:       detail.DriveLink,
			SubtitleLink:    detail.SubtitleLink,
			SubtitleFileID:  detail.SubtitleFileID,
			StartMs:         detail.StartMs,
			EndMs:           detail.EndMs,
			DurationMs:      scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs),
			TotalDurationMs: detail.TotalDurationMs,
		}
		if binding.DurationMs <= 0 {
			binding.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
		}
		if binding.DriveLink == "" {
			binding.DriveLink = ev.DriveLinks[clipID]
		}
		if binding.ClipTitle == "" {
			binding.ClipTitle = ev.ClipNames[clipID]
		}

		scenes[i] = scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%s", clipID),
			Index: i,
			Text:  text,
			Title: detail.Name,
			Kind:  kind,
			Bindings: scriptpkg.SceneBindings{
				Clip: binding,
			},
		}
	}
	return scenes
}

// cleanClipNarrativeText keeps the evidence fallback narration-safe. Search
// metadata can contain a source URL followed by tags; neither belongs in a
// spoken scene or in a semantic narrative field.
func cleanClipNarrativeText(text string) string {
	text = strings.TrimSpace(text)
	for _, marker := range []string{"https://", "http://", "www."} {
		if i := strings.Index(text, marker); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
	}
	text = strings.TrimSpace(text)
	const maxNarrativeRunes = 320
	if len([]rune(text)) <= maxNarrativeRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxNarrativeRunes])) + "…"
}
