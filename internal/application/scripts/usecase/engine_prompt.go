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
		"3. Stay anchored to the clip sequence and its narrative evidence blocks. Do not drift into generic biography unless it directly explains the clip.",
		"4. If a clip contains multiple beats, narrate those beats in order instead of abstracting them away.",
		"5. Do not invent events, dialogue, or transitions that are not supported by the clip evidence.",
		"6. Treat every transcript as private reference evidence, not as copy-ready script text.",
		"7. Rewrite and paraphrase what each clip is about in natural narrative language; do not reproduce transcript sentences verbatim.",
		"8. Never paste a transcript, subtitle line, or direct quote into the scene text unless the caller explicitly requests a quotation.",
		"9. Keep the spoken text natural and clean: do not include URLs, drive links, clip IDs, speaker labels, tag lists, keyword lists, or other technical markers in the narrated text.",
		"10. Put technical details only in metadata or bindings when the output contract supports them; never print them inside the voiceover text. Sources belong in metadata.sources, and narrative text and sources must remain separate fields when structured output is enabled.",
		"11. The text field is speakable and must contain only words intended for the viewer: never use [Fonte: ...], [Source: ...], markdown links, URLs, or bibliography notes.",
		"12. Write as an external narrator describing what is happening across the clips; never speak as the person shown or heard in a clip.",
		"13. Use third person narration. Do not use first-person roleplay such as I, me, my, or we, and do not rewrite the speaker's words as if you were that speaker.",
		"14. Use a youthful, conversational, video-friendly voice: concise, energetic, smooth, and lightly funny when the source supports it.",
		"15. Prefer concrete details, active verbs, and natural transitions. Avoid academic or formulaic analysis such as 'the narrative shifts', 'this segment illustrates', or 'the speaker provides insight'.",
		"16. For compilation videos, give each clip its own clear narrative beat and connect the beats with short, natural transitions without inventing dialogue or events.",
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
		b.WriteString("Scope: write exclusively about this topic; do not mention another declared segment or introduce the next segment.\n")
		fmt.Fprintf(&b, "Target words: %d", target)
		if strings.TrimSpace(s.SourceText) != "" {
			b.WriteString("\nSource text:\n")
			b.WriteString(s.SourceText)
		}
	}
	// Footer canonical — DoD-driven contract emitted once.
	b.WriteString("\n\nWrite one continuous narrative.\n")
	b.WriteString("Follow the segment order strictly. Do not skip, merge, or reorder topics.\n")
	b.WriteString("Emit exactly one prose paragraph for each segment, in the declared order, with one blank line between paragraphs. Never merge two segments into one paragraph and never move content across paragraph boundaries.\n")
	b.WriteString("Each segment must treat exclusively the subject named in its Topic. Do not anticipate the next subject, move paragraphs between segments, or insert a general conclusion before the final segment.\n")
	if len(plan.Segments) == 1 {
		b.WriteString("Because this request declares one single-scene segment, write between 180 and 260 words for that segment. This range is mandatory.\n")
	} else {
		b.WriteString("Respect each segment's declared target_words and any explicit min_words/max_words; do not pad short segments with generic filler. The first segment is an introduction when its topic says introduction, and must remain concise (one sentence whenever possible).\n")
	}
	b.WriteString("Write for a modern video voiceover: conversational, youthful, fluid, energetic, and easy to listen to. Use short natural transitions and concrete details instead of explaining the structure of the story.\n")
	b.WriteString("Paraphrase the supplied source naturally, preserving every name, date, score, result, and supported statement. Do not imitate the speaker or turn the narration into first-person dialogue.\n")
	b.WriteString("Do not invent names, dates, scores, results, or events.\n")
	b.WriteString("If a topic has no source_text, write the segment using only the topic and the global source. Do not repeat facts from previous segments.\n")
	b.WriteString("Target words are budget guidance, not exact count.\n")
	b.WriteString("Do not print segment titles (SEGMENT 1, Topic:, Source text:) in the output.\n")
	b.WriteString("Do not include markers like clip_id, accepted_clip_ids, JSON, Markdown code fences, schema_version, or specscene. Output only the script text.\n")
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
6. DO NOT put technical markers such as URLs, speaker labels, tags, keywords, or clip IDs inside the prose.
7. The clip transcripts are reference evidence only. Paraphrase what each clip says and does; do not copy transcript wording verbatim.
8. Write ONLY cohesive narrative prose. The script text itself. Nothing else.`
