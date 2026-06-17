package scriptcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama/types"

	"go.uber.org/zap"
)

// PlanNarrative calls the LLM to create a narrative plan from the evidence.
// The system prompt and narrative roles are resolved from the central
// NarrativeStrategy registry based on opts.Type.
func (b *ClipSourceBuilder) PlanNarrative(ctx context.Context, pack *ClipSourcePack, opts *ClipGenerationOptions) (*NarrativePlan, error) {
	b.log.Info("PlanNarrative: calling LLM for narrative ordering",
		zap.Int("clips", len(pack.Clips)),
		zap.String("language", opts.Language),
		zap.String("tone", opts.Tone),
		zap.String("type", opts.Type))

	strategy := ResolveStrategy(opts.Type)
	prompt := buildNarrativePlanPrompt(pack, opts, strategy)
	b.log.Debug("PlanNarrative: prompt built",
		zap.Int("prompt_chars", len(prompt)),
		zap.String("strategy_type", strategy.Type))

	// Planner uses a low temperature (0.2) and a fixed num_predict
	// so that the same evidence pack yields reproducible plans.
	// These values are also mixed into the fingerprint via
	// FingerprintVersionContext so changing them invalidates the cache.
	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	start := time.Now()
	plannerOpts := map[string]any{
		"temperature": 0.2,
		"num_predict": 2048,
	}
	response, err := b.ollamaCli.Chat(llmCtx, []types.Message{
		{Role: "system", Content: strategy.PlannerSystem},
		{Role: "user", Content: prompt},
	}, plannerOpts)
	elapsed := time.Since(start)
	if err != nil {
		b.log.Error("PlanNarrative: LLM call failed", zap.Duration("elapsed", elapsed), zap.Error(err))
		return nil, fmt.Errorf("narrative planning failed: %w", err)
	}

	b.log.Info("PlanNarrative: LLM response received",
		zap.Duration("elapsed", elapsed),
		zap.Int("response_chars", len(response)))

	plan, err := parseNarrativePlan(response)
	if err != nil {
		b.log.Warn("PlanNarrative: failed to parse JSON, using fallback",
			zap.Error(err),
			zap.String("raw_response", fmt.Sprintf("%.200s", response)))
		plan = fallbackNarrativePlan(pack, opts, strategy)
	} else {
		b.log.Info("PlanNarrative: plan parsed",
			zap.String("title", plan.Title),
			zap.String("arc", plan.NarrativeArc),
			zap.Int("ordered_clips", len(plan.OrderedClips)))
	}

	return plan, nil
}

func buildNarrativePlanPrompt(pack *ClipSourcePack, opts *ClipGenerationOptions, strategy NarrativeStrategy) string {
	var b strings.Builder

	b.WriteString(strategy.TaskIdentity)
	b.WriteString("\n\n")
	b.WriteString("VIDEO TYPE: ")
	b.WriteString(strategy.Type)
	b.WriteString("\n")
	if opts.Title != "" {
		b.WriteString(fmt.Sprintf("TITLE: %s\n", opts.Title))
	}
	b.WriteString(fmt.Sprintf("LANGUAGE: %s\n", opts.Language))
	b.WriteString(fmt.Sprintf("TONE: %s\n", opts.Tone))
	if opts.TargetWords > 0 {
		b.WriteString(fmt.Sprintf("TARGET WORDS: ~%d\n", opts.TargetWords))
	}
	b.WriteString(fmt.Sprintf("AVAILABLE ROLES: %s\n\n", strategy.RolesHelp))

	b.WriteString("Available clips:\n\n")
	for i, clip := range pack.Clips {
		b.WriteString(fmt.Sprintf("--- CLIP %d ---\n", i+1))
		b.WriteString(fmt.Sprintf("ID: %s\n", clip.ClipID))
		b.WriteString(fmt.Sprintf("Title: %s\n", clip.Title))
		if clip.Summary != "" {
			b.WriteString(fmt.Sprintf("Summary: %s\n", clip.Summary))
		}
		if len(clip.Topics) > 0 {
			b.WriteString(fmt.Sprintf("Topics: %s\n", strings.Join(clip.Topics, ", ")))
		}
		if len(clip.Speakers) > 0 {
			b.WriteString(fmt.Sprintf("Speakers: %s\n", strings.Join(clip.Speakers, ", ")))
		}
		if clip.Hook != "" {
			b.WriteString(fmt.Sprintf("Hook: %s\n", clip.Hook))
		}
		b.WriteString("\n")
	}

	b.WriteString("Your task:\n")
	b.WriteString("1. Order the clips in the best sequence for this ")
	b.WriteString(strategy.Type)
	b.WriteString(" format.\n")
	b.WriteString("2. Assign each clip a narrative role from the AVAILABLE ROLES list above.\n")
	b.WriteString("3. For each clip, write a one-sentence PURPOSE: what the writer should accomplish in that scene (e.g. 'Set up the compilation theme', 'Deliver the central punchline').\n")
	b.WriteString("4. For each clip, write a one-sentence COMEDIC_ANGLE (or narrative_angle for non-comedy types): the specific device the writer should use (e.g. 'visual gag is the punchline', 'deadpan reaction carries the joke').\n")
	b.WriteString("5. Assign a TARGET_WORDS budget to each clip. Total across all clips should sum to ~")
	b.WriteString(fmt.Sprintf("%d", opts.TargetWords))
	b.WriteString(". Distribute unevenly: opening and closing clips often need more space; transitional clips need less.\n")
	b.WriteString("6. Briefly explain the REASON each clip is placed where it is.\n")
	b.WriteString("7. Identify any clips that are incompatible or redundant.\n")
	b.WriteString("8. Describe how the arc builds from start to finish.\n\n")

	b.WriteString(`Respond with valid JSON only:
{
  "title": "Script title",
  "narrative_arc": "description of the narrative arc",
  "ordered_clips": [
    {
      "clip_id": "...",
      "role": "...",
      "reason": "...",
      "purpose": "one sentence — what this scene must accomplish",
      "comedic_angle": "one sentence — the specific device to use",
      "target_words": 150
    }
  ],
  "warnings": ["optional warning"]
}`)

	return b.String()
}

func parseNarrativePlan(raw string) (*NarrativePlan, error) {
	cleaned := raw
	if idx := strings.Index(cleaned, "{"); idx != -1 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx != -1 {
		cleaned = cleaned[:idx+1]
	}
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var plan NarrativePlan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse narrative plan JSON: %w", err)
	}
	return &plan, nil
}

func fallbackNarrativePlan(pack *ClipSourcePack, opts *ClipGenerationOptions, strategy NarrativeStrategy) *NarrativePlan {
	ordered := make([]OrderedClip, len(pack.Clips))

	openingRole := "context"
	closingRole := "context"
	if len(strategy.PlannerRoles) > 0 {
		openingRole = strategy.PlannerRoles[0]
		closingRole = strategy.PlannerRoles[len(strategy.PlannerRoles)-1]
	}

	// Distribute target words across clips so the writer has a per-scene
	// budget even when the planner LLM is unavailable. Opening and closing
	// get +20% because they typically need a hook/closing beat.
	clipCount := len(pack.Clips)
	wordsPerClip := 0
	if opts.TargetWords > 0 && clipCount > 0 {
		wordsPerClip = opts.TargetWords / clipCount
	}

	for i, c := range pack.Clips {
		role := strategy.PlannerRoles[0] // default = first role
		if len(strategy.PlannerRoles) > i {
			role = strategy.PlannerRoles[i]
		}
		switch i {
		case 0:
			role = openingRole
		case len(pack.Clips) - 1:
			role = closingRole
		}
		// Route the per-scene budget through sceneBudget so the +20%
		// opening/closing rule lives in exactly one place. Keeps the
		// contract between fallbackNarrativePlan and NormalizeScriptByScenes
		// in lockstep.
		budget := sceneBudget(wordsPerClip, i, clipCount)
		ordered[i] = OrderedClip{
			ClipID:       c.ClipID,
			Role:         role,
			Reason:       "automatic ordering",
			Purpose:      "ground the scene in the clip's observable action",
			ComedicAngle: "use the clip's own strongest beat as the punchline",
			TargetWords:  budget,
		}
	}

	title := opts.Title
	if title == "" && len(pack.Clips) > 0 {
		title = pack.Clips[0].Title
	}

	return &NarrativePlan{
		Title:        title,
		NarrativeArc: strategy.Type + " auto-order",
		OrderedClips: ordered,
	}
}
