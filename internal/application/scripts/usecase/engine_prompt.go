package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// cleanSegmentSourceText converts legacy editorial briefs into model-facing
// evidence. The old payload stored both the clip description and the request
// ("Write a ...") in source_text; sending that verbatim makes small models
// copy the instruction instead of rewriting the clip context.
func cleanSegmentSourceText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "clip description:") {
		text = strings.TrimSpace(text[len("clip description:"):])
	}
	// Remove the editorial instruction, including legacy duplicated tails.
	for {
		lower = strings.ToLower(text)
		idx := strings.Index(lower, ". write ")
		if idx < 0 {
			break
		}
		text = strings.TrimSpace(text[:idx+1])
	}
	return strings.TrimSpace(text)
}

// ── Prompt helpers ────────────────────────────────────────────────────
//
// These functions build the prompt that is sent to the Ollama model.
// They are consumed exclusively by Engine.Generate (engine_generate.go).

// buildClipGroundingInstructions adds clip-specific prompt guidance
// when the plan carries clip evidence. The goal is to keep the model
// anchored to the supplied clips instead of drifting into generic
// biography.
func buildClipGroundingInstructions(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil || !plan.HasClips() {
		return ""
	}

	requestedClips := len(plan.ClipEvidence.AcceptedClipIDs)

	var extra []string
	if len(plan.Segments) > 0 {
		extra = append(extra, fmt.Sprintf("Use exactly %d declared editorial segments; clips are supporting evidence grouped under those segments.", len(plan.Segments)))
	} else if plan.NumClips > 0 {
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
		"2. Every declared scene or segment must describe what is happening in the clips or supporting evidence: action, movement, setting, objects, reactions, and immediate consequences when available.",
		"3. Stay anchored to the clip sequence, declared segment order, and each segment's evidence blocks. Do not drift into generic biography unless it directly explains the requested topic.",
		"4. If assigned clip evidence contains multiple beats, narrate those beats in order instead of abstracting them away; a segment without clips must follow its topic and source_text.",
		"5. Do not invent events, dialogue, or transitions that are not supported by the clip evidence.",
		"6. Treat every transcript as private reference evidence, not as copy-ready script text.",
		"7. Rewrite and paraphrase what each clip is about in natural narrative language; do not reproduce transcript sentences verbatim.",
		"8. Never paste a transcript, subtitle line, or direct quote into the scene text unless the caller explicitly requests a quotation.",
		"9. Keep the spoken text natural and clean: do not include URLs, drive links, clip IDs, speaker labels, tag lists, keyword lists, or other technical markers in the narrated text.",
		"10. Put technical details only in metadata or bindings when the output contract supports them; never print them inside the voiceover text. Sources belong in metadata.sources, and narrative text and sources must remain separate fields when structured output is enabled.",
		"11. The text field is speakable and must contain only words intended for the viewer: never use [Fonte: ...], [Source: ...], markdown links, URLs, or bibliography notes.",
		"12. Write as an external narrator describing what is happening across the supplied segments and clips; never speak as the person shown or heard in a clip.",
		"13. Use third person narration. Do not use first-person roleplay such as I, me, my, or we, and do not rewrite the speaker's words as if you were that speaker.",
		"14. Use a youthful, conversational, video-friendly voice: concise, energetic, smooth, and lightly funny when the source supports it.",
		"15. Prefer concrete details, active verbs, and natural transitions. Avoid academic or formulaic analysis such as 'the narrative shifts', 'this segment illustrates', or 'the speaker provides insight'.",
		"16. For clip compilations, preserve each clip's concrete beats and connect them with short, natural transitions without inventing dialogue or events; explicit segments may combine multiple clips into one narrative beat.",
		"17. Some legacy evidence may contain editorial instructions such as 'Clip description' or 'Write a funny introduction'. Treat those words as contamination, never as output content. Always write a NEW narrator introduction and never copy that instruction or its sentence structure.",
	}
	if len(plan.Segments) > 0 {
		lines = append(lines,
			fmt.Sprintf("18. There are exactly %d declared editorial segments; a segment may contain zero, one, or many clips.", len(plan.Segments)),
			fmt.Sprintf("19. Output exactly %d non-empty prose paragraphs, one paragraph per declared segment, separated by one blank line.", len(plan.Segments)),
			"20. Paragraph i must correspond exclusively to declared segment i and may use only that segment's assigned clip evidence. Never merge segments, skip a segment, or reorder paragraphs.",
			"21. Every paragraph must preserve concrete details from its segment evidence: names, visible actions, subjects, settings, reactions, and supported transcript facts. Do not replace concrete information with generic commentary.",
			"22. Multiple clips assigned to one segment are supporting evidence for one paragraph; they must not become separate scenes or paragraphs.",
			"23. Do not output paragraph numbers, headings, labels, evidence markers, or any literal 'Clip description'/'Write a funny introduction' instruction; output only newly written paragraph text.",
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("17. There are exactly %d ordered narrative evidence blocks, one for each supplied clip.", requestedClips),
			fmt.Sprintf("18. Output exactly %d non-empty prose paragraphs, one paragraph per clip, separated by one blank line.", requestedClips),
			"19. Paragraph i must correspond exclusively to NARRATIVE EVIDENCE i and the i-th clip in the supplied order. Never merge two clips into one paragraph, skip a clip, or reorder the paragraphs.",
			"20. Every paragraph must preserve concrete details from its own evidence: names, visible actions, subjects, settings, reactions, and supported transcript facts. Do not replace concrete information with generic commentary.",
			"21. Do not output paragraph numbers, headings, labels, or evidence markers; output only the paragraph text.",
			"22. For clip-driven requests, this one-paragraph-per-clip contract is authoritative even when other segment guidance is present.",
		)
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
		if kind := strings.TrimSpace(s.Kind); kind != "" {
			fmt.Fprintf(&b, "Kind: %s\n", kind)
		}
		b.WriteString("Scope: write exclusively about this topic; do not mention another declared segment or introduce the next segment.\n")
		fmt.Fprintf(&b, "Target words: %d", target)
		if strings.EqualFold(strings.TrimSpace(s.Kind), "intro") {
			b.WriteString("\nINTRO FORMAT: write one or two short, punchy narrator sentences. Keep it playful and under 30 words; do not explain the clip or summarize its structure.")
		}
		// Per-segment source_text is an editorial brief, not transcript data.
		// Older payloads incorrectly stored "Clip description ... Write ..."
		// here; the canonical clip evidence (including DB subtitles) below is
		// the only model-facing source for those scenes.
		segmentSource := cleanSegmentSourceText(s.SourceText)
		if segmentSource != "" {
			b.WriteString("\nSource text:\n")
			b.WriteString(segmentSource)
			b.WriteString("\nREWRITE RULE: rewrite this source text as a new playful narrator introduction. Do not copy its wording, do not mention these instructions, and do not add unsupported facts.")
		}
		if len(s.ClipIDs) > 0 {
			b.WriteString("\nAssigned clip_ids (use only these clips for this segment): ")
			b.WriteString(strings.Join(s.ClipIDs, ", "))
		}
		if plan.ClipEvidence != nil && i < len(plan.ClipEvidence.SegmentEvidence) {
			segmentEvidence := plan.ClipEvidence.SegmentEvidence[i]
			if len(segmentEvidence.Clips) > 0 {
				b.WriteString("\nCLIP EVIDENCE:\n")
				for _, clipID := range segmentEvidence.ClipIDs {
					detail, ok := segmentEvidence.Clips[clipID]
					if !ok {
						continue
					}
					fmt.Fprintf(&b, "- %s", clipID)
					if detail.Name != "" {
						fmt.Fprintf(&b, " (%s)", detail.Name)
					}
					if detail.Description != "" {
						fmt.Fprintf(&b, "\n  Description: %s", detail.Description)
					}
					if detail.Transcript != "" {
						fmt.Fprintf(&b, "\n  Transcript: %s", detail.Transcript)
					}
					b.WriteByte('\n')
				}
			}
		}
	}
	// Footer canonical — DoD-driven contract emitted once.
	b.WriteString("\n\nWrite one continuous narrative.\n")
	b.WriteString("Follow the segment order strictly. Do not skip, merge, or reorder topics.\n")
	b.WriteString("Emit exactly one prose paragraph for each segment, in the declared order, with one blank line between paragraphs. Never merge two segments into one paragraph and never move content across paragraph boundaries.\n")
	b.WriteString("Each segment must treat exclusively the subject named in its Topic. Do not anticipate the next subject, move paragraphs between segments, or insert a general conclusion before the final segment.\n")
	if len(plan.Segments) == 1 {
		// Do not impose the historical 180–260-word single-scene range when
		// the caller supplied a smaller explicit segment budget. That range
		// contradicted ScriptSegment.TargetWords (for example target_words=70),
		// causing the model to generate a valid-looking paragraph that the
		// downstream 15% validator necessarily rejected.
		target := segmentBudgetFor(plan, 0, defaultSegmentWordsTolerancePercent).Target
		if target > 0 && target < 180 {
			fmt.Fprintf(&b, "Because this request declares one single-scene segment, write about %d words for that segment and stay within its declared word budget. This range is mandatory.\n", target)
		} else {
			b.WriteString("Because this request declares one single-scene segment, write between 180 and 260 words for that segment. This range is mandatory.\n")
		}
	} else {
		b.WriteString("Respect each segment's declared target_words and any explicit min_words/max_words; do not pad short segments with generic filler. The first segment is an introduction when its topic says introduction, and must remain concise (one sentence whenever possible).\n")
	}
	b.WriteString("Write for a modern video voiceover: conversational, youthful, fluid, energetic, and easy to listen to. Use short natural transitions and concrete details instead of explaining the structure of the story.\n")
	b.WriteString("Paraphrase the supplied source naturally, preserving every name, date, score, result, and supported statement. Do not imitate the speaker or turn the narration into first-person dialogue.\n")
	b.WriteString("Do not invent names, dates, scores, results, or events.\n")
	b.WriteString("If a topic has no source_text, write the segment using only the topic and the global source. Do not repeat facts from previous segments.\n")
	b.WriteString("Target words and explicit min_words/max_words are a hard editorial contract. If the segment is 500 words, stay within its declared range; never compensate with filler or extra paragraphs.\n")
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
3. DO NOT output machine scene IDs, scene indexes, kind labels, or bindings. Editorial section headings such as "Scene 1" are allowed only when the resolved narrative plan explicitly requires them.
4. DO NOT output schema_version, specscene, or any structured envelope.
5. DO NOT output metadata fields, clip_ids, drive links, or image URLs.
6. DO NOT put technical markers such as URLs, speaker labels, tags, keywords, or clip IDs inside the prose.
7. The clip transcripts are reference evidence only. Paraphrase what each clip says and does; do not copy transcript wording verbatim.
8. Write ONLY cohesive narrative prose. The script text itself. Nothing else.`
