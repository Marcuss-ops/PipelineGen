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
			RecordScriptGenerationBranch("b", plan.Language)
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

// buildSegmentInstructions renders the PR-CS-1 ScriptSegment blocks
// plus the canonical DoD-driven footer (DoD #1-#5). The function
// runs unconditionally — segments are a script-level structural
// directive, independent of clip evidence.
//
// Branch A applies when len(plan.Segments) > 0. Branch B (legacy,
// untouched) is the existing buildClipGroundingInstructions path
// that handles plan.SegmentTopics when Segments is empty. The two
// paths are mutually exclusive at runtime — the validator layer
// (DoD #8 / FASE 6, separate commit) rejects input that mixes both.
func buildSegmentInstructions(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range plan.Segments {
		if i > 0 {
			b.WriteString("\n\n")
		}
		// Resolve target words via the canonical fallback chain:
		// per-segment → plan.SegmentWords → plan.TargetWords → 80 default.
		target := s.TargetWords
		if target <= 0 {
			target = plan.SegmentWords
		}
		if target <= 0 {
			target = plan.TargetWords
		}
		if target <= 0 {
			target = 80
		}
		fmt.Fprintf(&b, "SEGMENT %d\n", i+1)
		fmt.Fprintf(&b, "Topic: %s\n", s.Topic)
		fmt.Fprintf(&b, "Target words: %d", target)
		if strings.TrimSpace(s.SourceText) != "" {
			b.WriteString("\nSource text:\n")
			b.WriteString(s.SourceText)
		}
	}
	// Footer canonical — DoD-driven contract emitted once.
	b.WriteString("\n\nWrite one continuous narrative.\n")
	b.WriteString("Follow the segment order strictly (Introduzione → Contesto → Evento → Conseguenze → Conclusione). Do not skip, merge, or reorder topics.\n")
	b.WriteString("Rewrite the supplied source text naturally, preserving every name, date, score, result, and quoted statement.\n")
	b.WriteString("Do not invent names, dates, scores, results, or events.\n")
	b.WriteString("If a topic has no source_text, write the segment using only the topic and the global source. Do not repeat facts from previous segments.\n")
	b.WriteString("Target words are budget guidance, not exact count.\n")
	b.WriteString("Do not print segment titles (SEGMENT 1, Topic:, Source text:) in the output.\n")
	b.WriteString("Do not include markers like clip_id, accepted_clip_ids, JSON, Markdown code fences, schema_version, or specscene. Output only the script text.\n")
	RecordScriptGenerationBranch("a", plan.Language)
	return b.String()
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
