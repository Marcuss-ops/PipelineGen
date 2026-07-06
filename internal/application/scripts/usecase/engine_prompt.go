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

// v1OutputInstruction is the prompt suffix appended unconditionally
// for the canonical script-generation pipeline.
//
// Two layers of enforcement cooperate to keep the model output on the
// ModelScriptOutputV1 contract:
//
//  1. Native Ollama JSON-mode (generate.go::GenerateScript sets
//     `options["format"] = "json"` when OutputMode == script_v1).
//     Ollama forces the model response to be syntactically valid
//     JSON — but a JSON object is not a V1 script; the schema
//     (schema_version, text, specscene.scenes[…].bindings) is still
//     the model's responsibility.
//
//  2. The v1OutputInstruction suffix below tells the model which
//     keys the V1 contract expects, in what shape, and forbids
//     markdown fences and any prose around the JSON object. The
//     decoder (model_output_decoder.go) tolerates code fences for
//     legacy cache rows, but the suffix biases the model toward
//     emitting clean canonical JSON so the decoder's path is the
//     happy path.
//
// Removing this suffix in favour of "json format only" is not safe:
// native json mode does not enforce schema-shaped JSON.
const v1OutputInstruction = `

[OUTPUT_FORMAT]
Respond ONLY with a single JSON object matching the canonical V1 shape:

  {
    "schema_version": 1,
    "text": "<complete script prose>",
    "specscene": {
      "version": 1,
      "scenes": [
        {"id": "scene-N", "index": N, "text": "<scene narration>", "kind": "narration|clip|image|mixed", "bindings": {}}
      ]
    }
  }

Do not include any text outside the JSON object. Do not wrap the JSON in markdown fences. Top-level keys are required: schema_version, text, specscene. SpecScene.scenes requires id (non-empty), index (sequential from 0), text (non-empty), kind (one of narration|clip|image|mixed), bindings (object).`
