package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Prompt helpers ────────────────────────────────────────────────────
//
// These functions build the prompt that is sent to the Ollama model.
// They are consumed exclusively by Engine.Generate (engine_generate.go).

// extractPlanClipIDs extracts clip IDs from the resolved plan's
// ClipEvidence. Returns nil for text-only plans (no clip evidence).
//
// Issue #2 (June 2026): field renamed from ClipIDs to
// AcceptedClipIDs. The LLM prompt grounding set is unchanged —
// any transcript-usable resolved clip counts.
func extractPlanClipIDs(plan *scriptpkg.ResolvedGenerationPlan) []string {
	if plan == nil || plan.ClipEvidence == nil {
		return nil
	}
	return plan.ClipEvidence.AcceptedClipIDs
}

// buildClipGroundingInstructions adds clip-specific prompt guidance
// when the plan carries clip evidence. The goal is to keep the model
// anchored to the supplied clips instead of drifting into generic
// biography.
func buildClipGroundingInstructions(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil || !plan.HasClips() {
		return ""
	}

	// Issue #2 (June 2026): field renamed ClipIDs → AcceptedClipIDs.
	clipIDs := strings.Join(plan.ClipEvidence.AcceptedClipIDs, ", ")
	requestedClips := len(plan.ClipEvidence.AcceptedClipIDs)
	if plan.NumClips > 0 && plan.NumClips < requestedClips {
		requestedClips = plan.NumClips
	}

	var extra []string
	if plan.NumClips > 0 {
		extra = append(extra, fmt.Sprintf("Use exactly %d clip-driven scenes.", requestedClips))
	}
	if plan.SegmentWords > 0 {
		extra = append(extra, fmt.Sprintf("Aim for about %d words per segment.", plan.SegmentWords))
	}
	if len(plan.SegmentTopics) > 0 {
		topics := make([]string, 0, len(plan.SegmentTopics))
		for i, topic := range plan.SegmentTopics {
			topic = strings.TrimSpace(topic)
			if topic == "" {
				continue
			}
			topics = append(topics, fmt.Sprintf("%d. %s", i+1, topic))
		}
		if len(topics) > 0 {
			extra = append(extra, "Segment topics:\n"+strings.Join(topics, "\n"))
		}
	}

	lines := []string{
		"CLIP-GROUNDED WRITING RULES:",
		"1. Treat the supplied clip evidence as the primary source.",
		"2. Every scene must describe what is happening in the clips: action, movement, setting, objects, reactions, and immediate consequences.",
		"3. Stay anchored to the clip sequence and the listed clip IDs: " + clipIDs + ". Do not drift into generic biography unless it directly explains the clip.",
		"4. If a clip contains multiple beats, narrate those beats in order instead of abstracting them away.",
		"5. Do not invent events, dialogue, or transitions that are not supported by the clip evidence.",
		"6. Keep drive links out of the spoken script; they are reference metadata only.",
	}
	lines = append(lines, extra...)
	return strings.Join(lines, "\n")
}

// plainTextInstruction is the prompt suffix appended unconditionally
// for the canonical script-generation pipeline.
//
// LLM-PLAIN-TEXT-CONTRACT wave (PR-1, July 2026): the model MUST
// emit ONLY narrative prose. Every structured field (schema_version,
// text envelope, specscene, scene IDs, scene indexes, kind labels,
// bindings) is owned by downstream Go code (SceneSynthesizer +
// scene binder + postprocessor registry — see
// internal/application/scripts/scene/synthesizer.go and
// internal/application/scripts/adapters/processor_*.go).
//
// The model is FORBIDDEN from producing:
//   - JSON objects or arrays
//   - schema_version / specscene keys
//   - scene IDs ("scene-N") or scene indexes
//   - kind labels (narration|clip|image|mixed)
//   - bindings objects (clip_id, drive_link, image_id, etc.)
//   - markdown fences, code blocks, or any structured envelope
//
// godlike/06 SSOT (one canonical owner per fact): the narrative-prose
// contract lives ONLY here (engine_prompt.go). The downstream
// structured envelope (ModelScriptOutputV1) is composed exclusively
// by SceneSynthesizer.FromProse + SceneAssetBinder + postprocessor
// pipeline — the model output is read as raw text via
// jsonextract.ParsePlainTextFresh.
const plainTextInstruction = `

[OUTPUT_FORMAT]
Write ONLY the complete narrative script text. Follow these rules:

1. DO NOT output JSON, JSON objects, or JSON arrays.
2. DO NOT output markdown, code fences, or block formatting.
3. DO NOT output scene IDs, scene indexes, kind labels, or bindings.
4. DO NOT output schema_version, specscene, or any structured envelope.
5. DO NOT output metadata fields, clip_ids, drive links, or image URLs.
6. Write ONLY cohesive narrative prose. The script text itself. Nothing else.`
