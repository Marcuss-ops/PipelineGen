// Package payloadcheck owns the semantic checks that the structural
// GenerationEnvelopeV2.Validate cannot express.
//
// The JSON contract gate proves that every key exists and has the right type.
// It cannot prove that two DIFFERENT blocks do not contradict each other — for
// example an image_search_mode that disables entity-derived searches while
// entity_images.enabled asks for them. Those contradictions used to be
// discovered only after a run had paid for script, translation, TTS and
// renders, so they are collected here and applied before submission.
//
// The package is deliberately transport-free: cmd/payloadlint scans the
// on-disk job corpus, cmd/velox preflight checks one submission, and both get
// exactly the same verdict from the same functions.
package payloadcheck

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SemanticErrors returns every known semantic contradiction in the envelope.
// The slice is empty for a coherent payload. Errors are ordered by item and
// then by check so the CLI output is stable. Each message starts with its
// stable code so operators can grep a verdict.
func SemanticErrors(env *scriptpkg.GenerationEnvelopeV2) []string {
	if env == nil {
		return nil
	}
	var out []string
	for i := range env.Items {
		out = append(out, itemErrors(&env.Items[i], i)...)
	}
	return out
}

// SemanticWarnings returns advisory findings that do not make a payload
// invalid but change what the caller probably expects. payloadlint prints
// them and, with -strict, counts them as failures.
func SemanticWarnings(env *scriptpkg.GenerationEnvelopeV2) []string {
	if env == nil {
		return nil
	}
	var out []string
	for i := range env.Items {
		out = append(out, itemWarnings(&env.Items[i], i, env.ForceRefresh)...)
	}
	return out
}

func itemWarnings(item *scriptpkg.GenerationItemV2, index int, envelopeForceRefresh bool) []string {
	if item == nil {
		return nil
	}
	ref := strings.TrimSpace(item.ID)
	if ref == "" {
		ref = fmt.Sprintf("items[%d]", index)
	}
	var out []string
	if item.ScriptParams.ForceRefresh && !envelopeForceRefresh {
		out = append(out, ref+": FORCE_REFRESH_DUPLICATE: script_params.force_refresh does not bypass the idempotency store; set the envelope-level force_refresh instead")
	}
	if item.ScriptParams.TargetWords > 0 {
		words := 0
		for _, segment := range item.ScriptParams.Segments {
			words += len(strings.Fields(segment.SourceText))
		}
		if words > 0 && item.ScriptParams.TargetWords > 2*words {
			out = append(out, fmt.Sprintf("%s: TARGET_WORDS_WIDE: target_words=%d is more than twice the %d supplied segment words", ref, item.ScriptParams.TargetWords, words))
		}
	}
	return out
}

func itemErrors(item *scriptpkg.GenerationItemV2, index int) []string {
	if item == nil {
		return nil
	}
	ref := strings.TrimSpace(item.ID)
	if ref == "" {
		ref = fmt.Sprintf("items[%d]", index)
	}
	var out []string

	// image_search_mode=disabled is the ONLY mode that switches the entity
	// image lane off: disabled means no provider query at all, so asking for
	// portrait images at the same time contradicts itself.
	//
	// explicit_only is deliberately NOT paired with entity_images.enabled here.
	// It restricts inferred CONTEXTUAL queries; the identity lane is a direct
	// operator request and stays on (entityImageLaneEnabled in
	// vidrush/media_resolver_image_segments.go, and the ITCLOSE-07 closeout).
	// Flagging that pair re-asserts the bug ITCLOSE-07 fixed and would push
	// callers to widen their contextual search just to silence the lint.
	extraction := item.MediaPlan.Extraction
	if strings.EqualFold(strings.TrimSpace(extraction.ImageSearchMode), media.ImageSearchModeDisabled) &&
		extraction.EntityImages.Enabled {
		out = append(out, ref+": IMAGE_SEARCH_DISABLED_WITH_ENTITY_IMAGES: media_plan.extraction.image_search_mode=disabled forbids every image search, but media_plan.extraction.entity_images.enabled=true requests portraits")
	}

	// audio.mode has one owner: item.audio. output.audio is the legacy
	// fallback and must not be populated at the same time; the builder prefers
	// item.audio, so a different value in output.audio is silently ignored.
	itemAudio := strings.TrimSpace(item.Audio.Mode)
	outputAudio := strings.TrimSpace(item.Output.Audio.Mode)
	if itemAudio != "" && outputAudio != "" {
		if strings.EqualFold(itemAudio, outputAudio) {
			out = append(out, ref+": AUDIO_MODE_CONFLICT: audio.mode is set twice (item.audio.mode and output.audio.mode); keep only item.audio")
		} else {
			out = append(out, ref+": AUDIO_MODE_CONFLICT: audio.mode disagrees (item.audio.mode="+itemAudio+", output.audio.mode="+outputAudio+"); item.audio is authoritative")
		}
	}

	// The runtime clamps an oversized phrase budget. A payload that asks for
	// more than the hard limit is silently rewritten, so reject it instead.
	if item.MaxPhraseOverlays > overlays.MaxPhraseOverlaysHardLimit {
		out = append(out, fmt.Sprintf("%s: PHRASE_BUDGET_CLAMPED: max_phrase_overlays=%d exceeds the runtime hard limit %d", ref, item.MaxPhraseOverlays, overlays.MaxPhraseOverlaysHardLimit))
	}

	// Two explicit owners for the per-scene image count. A planner cap that
	// disagrees with script_params.images_per_scene means one of the two
	// numbers is silently ignored.
	imagesPerScene := item.ScriptParams.ImagesPerScene
	plannerImages := item.MediaPlan.Planner.MaxImagesPerScene
	if imagesPerScene > 0 && plannerImages > 0 && imagesPerScene != plannerImages {
		out = append(out, fmt.Sprintf("%s: IMAGES_PER_SCENE_CONFLICT: script_params.images_per_scene=%d disagrees with media_plan.planner.max_images_per_scene=%d; keep one owner", ref, imagesPerScene, plannerImages))
	}

	// Per-segment target words are explicit budgets. When every segment
	// carries one, their sum is the body target; a different run-level
	// target_words is a contradiction (the reduced canary payloads used to
	// say 1800 while their single segment said 60).
	if item.ScriptParams.TargetWords > 0 && len(item.ScriptParams.Segments) > 0 {
		sum := 0
		allExplicit := true
		for _, segment := range item.ScriptParams.Segments {
			if segment.TargetWords <= 0 {
				allExplicit = false
				break
			}
			sum += segment.TargetWords
		}
		if allExplicit && sum != item.ScriptParams.TargetWords {
			out = append(out, fmt.Sprintf("%s: TARGET_WORDS_MISMATCH: script_params.target_words=%d disagrees with the sum of segment target_words=%d", ref, item.ScriptParams.TargetWords, sum))
		}
	}

	// voiceover_group has two spellings. One non-empty value is the routing
	// key; two different values would make the group depend on which block a
	// consumer reads.
	itemGroup := strings.TrimSpace(item.ScriptParams.VoiceoverGroup)
	outputGroup := strings.TrimSpace(item.Output.VoiceoverGroup)
	if itemGroup != "" && outputGroup != "" && itemGroup != outputGroup {
		out = append(out, ref+": VOICEOVER_GROUP_CONFLICT: voiceover_group disagrees (script_params.voiceover_group="+itemGroup+", output.voiceover_group="+outputGroup+")")
	}

	return out
}
