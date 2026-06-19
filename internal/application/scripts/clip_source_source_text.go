package scripts

import (
	"fmt"
	"strings"
)

// BuildSourceText converts the evidence cards and narrative plan into a
// structured source text that engine.WriteScript can consume. This is the
// bridge between ClipSourceBuilder and the shared script generation engine.
//
// The prompt follows this order (from the NarrativeStrategy registry):
//  1. TASK IDENTITY — what kind of video this is
//  2. STRUCTURAL STRATEGY — output format and progression
//  3. NON-NEGOTIABLE RULES — grounding, clip IDs, etc.
//  4. LENGTH BUDGET — word targets
//  5. NARRATIVE PLAN — clip order and roles
//  6. CLIP EVIDENCE — title, summary, transcript
//  7. FINAL COMPLIANCE REMINDER — brief recap of key rules
//
// When opts.SourceText is not empty, it is used as the primary text to rewrite,
// with clip evidence provided as supporting context to ground the rewrite.
func (b *ClipSourceBuilder) BuildSourceText(pack *ClipSourcePack, plan *NarrativePlan, opts *ClipGenerationOptions) string {
	var sb strings.Builder

	strategy := ResolveStrategy(opts.Type)

	// ── 1. TASK IDENTITY ──────────────────────────────────────────────
	if opts.SourceText != "" {
		sb.WriteString("You are rewriting a script. Use the clip evidence below to ground and enrich the rewrite.\n\n")
		sb.WriteString("=== SOURCE TEXT TO REWRITE ===\n\n")
		sb.WriteString(opts.SourceText)
		sb.WriteString("\n\n")
		sb.WriteString("=== END OF SOURCE TEXT ===\n\n")
	}

	sb.WriteString(strategy.TaskIdentity)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("TITLE: %s\n", plan.Title))
	sb.WriteString(fmt.Sprintf("VIDEO TYPE: %s\n", strategy.Type))
	sb.WriteString(fmt.Sprintf("LANGUAGE: %s\n", opts.Language))
	sb.WriteString(fmt.Sprintf("TONE: %s\n", opts.Tone))
	sb.WriteString(fmt.Sprintf("NARRATIVE ARC: %s\n\n", plan.NarrativeArc))

	// ── 2. STRUCTURAL STRATEGY ────────────────────────────────────────
	sb.WriteString("=== STRUCTURAL STRATEGY ===\n\n")
	sb.WriteString(strategy.OutputFormat)
	sb.WriteString("\n\n")

	// ── 2b. OUTPUT CONTRACT (PR4 + PR5) ───────────────────────────────────
	// Explicit, machine-checkable contract for the writer. Mirrors the
	// structural checks in script_validator.ValidateScriptWithPack so
	// the LLM sees the same rules the validator enforces.
	//
	// PR5: tightened language — smaller models (qwen2.5:1.5b, gemma2:2b)
	// often skipped the markers entirely. Added the literal GOOD vs BAD
	// comparison and emphasised "FIRST line of every scene".
	sb.WriteString("=== OUTPUT CONTRACT — MANDATORY SCENE MARKERS ===\n\n")
	sb.WriteString("CRITICAL: Your response MUST be a sequence of scenes. The very FIRST line of every scene MUST be a marker line. Without markers the script is rejected.\n\n")
	if strategy.AllowNarrationScenes {
		sb.WriteString("Each scene is one of:\n")
		sb.WriteString("- A clip scene:    [Clip: <clip_id>]\n")
		sb.WriteString("- A narration scene: [Narration: opening|closing|intro|outro|transition]\n\n")
	} else {
		sb.WriteString("Every scene MUST be a clip scene starting with `[Clip: <clip_id>]` on the FIRST line. No narration-only scenes.\n\n")
	}
	sb.WriteString("Hard rules:\n")
	sb.WriteString("- Every `[Clip: ...]` marker MUST contain a non-empty clip_id drawn from the NARRATIVE PLAN below — copy the EXACT id (e.g. `1XcdSo0so-ur0-cITwWNeQyP9Q-n_GKvv`), no abbreviations.\n")
	sb.WriteString("- Each clip_id MUST appear in exactly one `[Clip: ...]` scene (no duplicates, no reuse).\n")
	sb.WriteString("- Every clip in the NARRATIVE PLAN MUST appear in exactly one scene (no skipped clips).\n")
	sb.WriteString("- The marker line is the FIRST line of the scene — body text follows on subsequent lines.\n")
	sb.WriteString("- The body under each marker MUST be non-empty and grounded in the clip's evidence.\n")
	sb.WriteString("- Mention at least one observable action from the clip's transcript or evidence per scene.\n")
	if strategy.AllowNarrationScenes {
		sb.WriteString("- A narration scene uses `[Narration: opening|closing|intro|outro|transition]` and is allowed only at the very start, very end, or between two clip scenes as a transition.\n")
	}
	sb.WriteString("\nGOOD vs BAD example (do exactly like GOOD):\n\n")
	sb.WriteString(" GOOD (markers on first line, one-clip-per-scene):\n")
	sb.WriteString("   [Clip: 1XcdSo0so-ur0-cITwWNeQyP9Q-n_GKvv]\n")
	sb.WriteString("   The actor leans over the broken bench and winces, in obvious pain but smiling wryly.\n\n")
	sb.WriteString("   [Clip: 1o2-jTsU4i09zz0Qdo8lKC17oX3DHaNwo]\n")
	sb.WriteString("   A close-up reveals the cast around the table nodding in sympathy.\n\n")
	sb.WriteString(" BAD (no markers — the script is rejected):\n")
	sb.WriteString("   The actor leans over the broken bench and winces. Then there is a close-up of the table...\n\n")

	// ── 3. NON-NEGOTIABLE RULES ───────────────────────────────────────
	sb.WriteString("=== NON-NEGOTIABLE RULES ===\n\n")
	sb.WriteString("- Use only facts supported by clip evidence below. Do NOT invent any actions, dialogue, or details.\n")
	sb.WriteString("- Each scene must reference exactly one real accepted clip.\n")
	sb.WriteString("- Never output an empty clip ID.\n")
	sb.WriteString("- Mention at least one specific, observable action from each assigned clip.\n")
	sb.WriteString("- Do not merely say that something is funny, impressive or interesting — show it through narration.\n")
	sb.WriteString("- Do not repeat the same praise, observation or conclusion across multiple scenes.\n")
	sb.WriteString("- Ground every statement in the evidence.\n")
	if !strategy.AllowNarrationScenes {
		sb.WriteString("- Every scene must be tied to a specific clip — no narration-only scenes.\n")
	}
	sb.WriteString("\n")

	// ── 4. LENGTH BUDGET ──────────────────────────────────────────────
	if opts.TargetWords > 0 {
		clipsCount := len(plan.OrderedClips)
		wordsPerClip := opts.TargetWords / max(clipsCount, 1)
		sb.WriteString(fmt.Sprintf("=== LENGTH BUDGET ===\n\n"))
		sb.WriteString(fmt.Sprintf("TOTAL TARGET: ~%d words\n", opts.TargetWords))
		sb.WriteString(fmt.Sprintf("ACCEPTED CLIPS: %d\n", clipsCount))
		sb.WriteString(fmt.Sprintf("TARGET PER CLIP: ~%d words\n\n", wordsPerClip))
	}
	if opts.MaxCharsPerScene > 0 {
		sb.WriteString(fmt.Sprintf("MAX CHARS PER SCENE: %d\n", opts.MaxCharsPerScene))
	}
	sb.WriteString("\n")

	// ── 5. NARRATIVE PLAN ─────────────────────────────────────────────
	sb.WriteString("=== NARRATIVE PLAN (clip order, roles, per-clip intent) ===\n\n")
	for i, oc := range plan.OrderedClips {
		sb.WriteString(fmt.Sprintf("%d. Clip %s — role: %s", i+1, oc.ClipID, oc.Role))
		if oc.TargetWords > 0 {
			sb.WriteString(fmt.Sprintf(" — target: ~%d words", oc.TargetWords))
		}
		sb.WriteString("\n")
		if oc.Purpose != "" {
			sb.WriteString(fmt.Sprintf("   purpose: %s\n", oc.Purpose))
		}
		if oc.ComedicAngle != "" {
			sb.WriteString(fmt.Sprintf("   comedic_angle: %s\n", oc.ComedicAngle))
		}
		if oc.Reason != "" {
			sb.WriteString(fmt.Sprintf("   reason: %s\n", oc.Reason))
		}
	}
	sb.WriteString("\n")

	// ── 6. CLIP EVIDENCE ──────────────────────────────────────────────
	sb.WriteString("=== CLIP EVIDENCE ===\n\n")
	clipMap := make(map[string]ClipEvidence)
	for _, c := range pack.Clips {
		clipMap[c.ClipID] = c
	}

	for _, oc := range plan.OrderedClips {
		c, ok := clipMap[oc.ClipID]
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- Clip %s ---\n", c.ClipID))
		sb.WriteString(fmt.Sprintf("Title: %s\n", c.Title))
		if c.Summary != "" {
			sb.WriteString(fmt.Sprintf("Summary: %s\n", c.Summary))
		}
		if len(c.Topics) > 0 {
			sb.WriteString(fmt.Sprintf("Topics: %s\n", strings.Join(c.Topics, ", ")))
		}

		switch opts.TranscriptPolicy {
		case "summary_only":
			// Summary already included above
		case "evidence_only":
			maxChunks := 3
			for j, chunk := range c.EvidenceChunks {
				if j >= maxChunks {
					break
				}
				sb.WriteString(fmt.Sprintf("  [%dms-%dms] %s\n", chunk.StartMS, chunk.EndMS, chunk.Text))
			}
		default: // "auto", "full"
			for _, chunk := range c.EvidenceChunks {
				sb.WriteString(fmt.Sprintf("  [%dms-%dms] %s\n", chunk.StartMS, chunk.EndMS, chunk.Text))
			}
		}
		sb.WriteString("\n")
	}

	// ── 7. FINAL COMPLIANCE REMINDER ──────────────────────────────────
	sb.WriteString("=== FINAL CHECK BEFORE WRITING ===\n\n")
	sb.WriteString(fmt.Sprintf("- Follow the %s structure.\n", strategy.Type))
	sb.WriteString("- Use only the accepted clip IDs listed in the NARRATIVE PLAN.\n")
	sb.WriteString("- One clip block per scene.\n")
	sb.WriteString("- Ground every scene in its clip evidence.\n")
	sb.WriteString("- Preserve the requested tone and style.\n")

	// Custom style instructions
	if opts.StyleInstructions != "" {
		sb.WriteString("\n=== STYLE INSTRUCTIONS ===\n\n")
		sb.WriteString(opts.StyleInstructions)
		sb.WriteString("\n")
	}

	return sb.String()
}
