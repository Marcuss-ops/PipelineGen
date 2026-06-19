package scripts

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/prompts"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// WordCountBounds returns the acceptable word count range using percentage-based
// tolerance. Shorter targets get wider tolerance (15%) because 300 words on a
// 500-word section is 60%, while 300 words on a 3000-word section is only 10%.
func WordCountBounds(targetWords int) (int, int) {
	if targetWords <= 0 {
		targetWords = 1800
	}
	var tolerance float64
	switch {
	case targetWords <= 800:
		tolerance = 0.15 // 15% for short sections
	case targetWords <= 2000:
		tolerance = 0.10 // 10% for medium sections
	default:
		tolerance = 0.08 // 8% for long sections
	}
	minWords := int(float64(targetWords) * (1 - tolerance))
	if minWords < 500 {
		minWords = 500
	}
	maxWords := int(float64(targetWords) * (1 + tolerance))
	if maxWords < minWords {
		maxWords = minWords
	}
	return minWords, maxWords
}

// ExpandPrompt is the public wrapper around the package-internal
// expandPrompt helper. Tests in the same package call the public name
// so the wrapper is part of the contract; production code reaches the
// helper indirectly via NormalizeLength.
func ExpandPrompt(topic, chapter string, targetWords, currentWords int, guidelines string) string {
	return expandPrompt(topic, chapter, targetWords, currentWords, guidelines)
}

// CompressPrompt is the public wrapper around the package-internal
// compressPrompt helper. See ExpandPrompt for the rationale.
func CompressPrompt(topic, chapter string, targetWords, currentWords int, guidelines string) string {
	return compressPrompt(topic, chapter, targetWords, currentWords, guidelines)
}

// CalculateTargetWords converts duration to a target word count using the
// standard 140 wpm rate. DurationMinutes takes priority over durationSeconds
// when both are provided.
//
// Seconds are converted to minutes with math.Ceil so that 90s and 119s
// both produce a 2-minute target (≈280 words) instead of silently
// truncating to 1 minute. The previous integer division
// (durationSeconds / 60) caused scripts of e.g. 90 seconds to be
// treated as 60 seconds — a 33% undersize.
func CalculateTargetWords(durationSeconds, durationMinutes int) int {
	if durationMinutes <= 0 && durationSeconds > 0 {
		durationMinutes = int(math.Ceil(float64(durationSeconds) / 60.0))
	}
	if durationMinutes <= 0 {
		durationMinutes = 1
	}
	return durationMinutes * ollamatypes.WordsPerMinute
}

// expandPrompt creates a controlled expansion prompt that tells the LLM
// exactly WHAT to add, preventing filler and repetition.
func expandPrompt(topic, chapter string, targetWords, currentWords int, guidelines string) string {
	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderExpand(topic, chapter, targetWords, currentWords, guidelines)
		if err == nil {
			return rendered
		}
	}
	// Fallback
	deficit := targetWords - currentWords
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"This section is too short (%d words). You MUST expand it to about %d words "+
			"(deficit: ~%d words).\n\n", currentWords, targetWords, deficit))
	b.WriteString("EXPAND BY ADDING:\n")
	b.WriteString("1. More narrative context and scene-setting\n")
	b.WriteString("2. Concrete examples, data, or anecdotes\n")
	b.WriteString("3. Consequences or implications of key points\n")
	b.WriteString("4. Natural transitions between ideas\n")
	b.WriteString("5. Emotional or sensory details\n")
	b.WriteString("6. Clearer explanations of complex concepts\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("- Do NOT repeat the same sentences or ideas\n")
	b.WriteString("- Do NOT change the meaning or factual content\n")
	b.WriteString("- Do NOT add facts not present in the source material\n")
	b.WriteString("- Preserve the original voice and tone\n\n")
	if guidelines != "" {
		b.WriteString("[GUIDELINES]\n")
		b.WriteString(guidelines)
		b.WriteString("\n[/GUIDELINES]\n\n")
	}
	b.WriteString("TOPIC:\n")
	b.WriteString(topic)
	b.WriteString("\n\nCURRENT TEXT:\n")
	b.WriteString(chapter)
	b.WriteString(fmt.Sprintf("\n\nTarget length: about %d words.", targetWords))
	return b.String()
}

// compressPrompt creates a targeted compression prompt that preserves
// concrete value while removing filler.
func compressPrompt(topic, chapter string, targetWords, currentWords int, guidelines string) string {
	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderCompress(topic, chapter, targetWords, currentWords, guidelines)
		if err == nil {
			return rendered
		}
	}
	// Fallback
	excess := currentWords - targetWords
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"This section is too long (%d words). Compress it to about %d words "+
			"(remove ~%d words).\n\n", currentWords, targetWords, excess))
	b.WriteString("COMPRESS BY:\n")
	b.WriteString("1. Removing repetition and filler phrases\n")
	b.WriteString("2. Merging redundant sentences\n")
	b.WriteString("3. Cutting abstract padding and vague statements\n")
	b.WriteString("4. Simplifying verbose constructions\n\n")
	b.WriteString("PRESERVE:\n")
	b.WriteString("- Concrete examples, numbers, statistics\n")
	b.WriteString("- Checklists and actionable items\n")
	b.WriteString("- Proper nouns and specific references\n")
	b.WriteString("- The original voice and tone\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("- Do NOT become poetic or motivational\n")
	b.WriteString("- Do NOT add new content\n")
	b.WriteString("- Do NOT change the meaning\n\n")
	if guidelines != "" {
		b.WriteString("[GUIDELINES]\n")
		b.WriteString(guidelines)
		b.WriteString("\n[/GUIDELINES]\n\n")
	}
	b.WriteString("TOPIC:\n")
	b.WriteString(topic)
	b.WriteString("\n\nTEXT TO COMPRESS:\n")
	b.WriteString(chapter)
	b.WriteString(fmt.Sprintf("\n\nTarget length: about %d words.", targetWords))
	return b.String()
}

// NormalizeLength adjusts content to fit within the acceptable word count bounds.
// Uses percentage-based tolerance and controlled expand/compress prompts
// with a maximum of 2 adjustment attempts.
// numPredict optionally limits LLM output tokens (0 = use server default).
func NormalizeLength(ctx context.Context, gen *ollama.Generator, language, tone, model, topic, chapter, guidelines string, targetWords, numPredict int) (string, int, string, error) {
	content := strings.TrimSpace(chapter)
	if content == "" {
		return content, 0, "empty", nil
	}
	if gen == nil {
		return content, textutil.CountWords(content), "approve", nil
	}

	minWords, maxWords := WordCountBounds(targetWords)
	action := "approve"
	for attempt := 0; attempt < 2; attempt++ {
		wordCount := textutil.CountWords(content)
		if wordCount >= minWords && wordCount <= maxWords {
			return content, wordCount, action, nil
		}

		if wordCount < minWords {
			action = "expand"
			opts := make(map[string]any)
			if numPredict > 0 {
				opts["num_predict"] = numPredict
			}
			expanded, err := gen.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
				Language:   language,
				MinWords:   targetWords,
				Tone:       tone,
				Model:      model,
				Prompt:     expandPrompt(topic, content, targetWords, wordCount, guidelines),
				SourceText: content,
				Title:      topic,
				Options:    opts,
			})
			if err != nil || strings.TrimSpace(expanded.Script) == "" {
				if err != nil {
					return content, wordCount, action, err
				}
				return content, wordCount, action, nil
			}
			content = ollamatypes.CleanScript(strings.TrimSpace(expanded.Script))
			continue
		}

		action = "compress"
		opts := make(map[string]any)
		if numPredict > 0 {
			opts["num_predict"] = numPredict
		}
		compressed, err := gen.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
			Language:   language,
			MinWords:   targetWords,
			Tone:       tone,
			Model:      model,
			Prompt:     compressPrompt(topic, content, targetWords, wordCount, guidelines),
			SourceText: content,
			Title:      topic,
			Options:    opts,
		})
		if err != nil || strings.TrimSpace(compressed.Script) == "" {
			if err != nil {
				return content, wordCount, action, err
			}
			return content, wordCount, action, nil
		}
		content = ollamatypes.CleanScript(strings.TrimSpace(compressed.Script))
	}

	return content, textutil.CountWords(content), action, nil
}

// ── Scene-aware normalization (PR4) ──────────────────────────────────────

// NormalizeScriptByScenes normalizes a script scene-by-scene, preserving
// every [Clip: id] and [Narration: ...] marker. Each scene gets its own
// target word count (targetWords / sceneCount, with a +20% bump on the
// first and last scene as the opening/closing hooks) and is expanded or
// compressed independently. Scenes that are already inside the tolerance
// band are left untouched, which means a long script with a few thin
// scenes no longer gets rewritten end-to-end (the previous behaviour,
// which could erase or duplicate markers).
//
// Returns the reassembled script, the total word count, the dominant
// action ("expand" / "compress" / "approve" / "empty"), and any error.
//
// If the script has no parseable markers, this function falls back to
// the legacy NormalizeLength so the legacy text-generation path keeps
// working unchanged.
func NormalizeScriptByScenes(ctx context.Context, gen *ollama.Generator, language, tone, model, topic, script, guidelines string, targetWords, numPredict int) (string, int, string, error) {
	content := strings.TrimSpace(script)
	if content == "" {
		return content, 0, "empty", nil
	}

	scenes := ParseScenes(content)
	// Fall back to legacy behaviour for unstructured scripts
	if len(scenes) == 0 || (len(scenes) == 1 && scenes[0].Kind == "preamble") {
		return NormalizeLength(ctx, gen, language, tone, model, topic, content, guidelines, targetWords, numPredict)
	}

	// Distribute the target across scenes. Opening and closing get +20%.
	clipScenes := 0
	for _, s := range scenes {
		if s.Kind == "clip" {
			clipScenes++
		}
	}
	if clipScenes == 0 {
		clipScenes = len(scenes) // fall back to scene count
	}
	perScene := targetWords / clipScenes
	if perScene <= 0 {
		perScene = targetWords
	}

	dominant := "approve"
	totalWords := 0
	results := make([]string, len(scenes))

	// Precompute 0-based clip index for each scene (O(n) instead of an
	// O(n) re-scan inside the main loop). Non-clip scenes get -1.
	clipIdxMap := make([]int, len(scenes))
	clipRunning := -1
	for i, s := range scenes {
		if s.Kind == "clip" {
			clipRunning++
			clipIdxMap[i] = clipRunning
		} else {
			clipIdxMap[i] = -1
		}
	}

	for i, s := range scenes {
		sceneText := s.Text
		sceneWords := textutil.CountWords(sceneText)
		totalWords += sceneWords

		// Per-scene target: opening/closing +20%, middle 1.0x
		budget := perScene
		if s.Kind == "clip" {
			budget = sceneBudget(perScene, clipIdxMap[i], clipScenes)
		}

		minW, maxW := WordCountBounds(budget)
		normalized := sceneText
		action := "approve"

		// Only send the LLM call if the scene is actually out of band
		// and we have a generator to use. Narration scenes follow the
		// same rule.
		if gen != nil && sceneWords < minW {
			action = "expand"
			expanded, err := gen.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
				Language:   language,
				MinWords:   budget,
				Tone:       tone,
				Model:      model,
				Prompt:     expandPrompt(topic, sceneText, budget, sceneWords, guidelines),
				SourceText: sceneText,
				Title:      topic,
				Options:    ollamaOpts(numPredict),
			})
			if err == nil && strings.TrimSpace(expanded.Script) != "" {
				normalized = ollamatypes.CleanScript(strings.TrimSpace(expanded.Script))
				totalWords += textutil.CountWords(normalized) - sceneWords
			}
		} else if gen != nil && sceneWords > maxW {
			action = "compress"
			compressed, err := gen.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
				Language:   language,
				MinWords:   budget,
				Tone:       tone,
				Model:      model,
				Prompt:     compressPrompt(topic, sceneText, budget, sceneWords, guidelines),
				SourceText: sceneText,
				Title:      topic,
				Options:    ollamaOpts(numPredict),
			})
			if err == nil && strings.TrimSpace(compressed.Script) != "" {
				normalized = ollamatypes.CleanScript(strings.TrimSpace(compressed.Script))
				totalWords += textutil.CountWords(normalized) - sceneWords
			}
		}

		if action == "expand" || action == "compress" {
			dominant = action
		}
		results[i] = assembleScene(s, normalized)
	}

	return strings.Join(results, "\n\n"), totalWords, dominant, nil
}

// assembleScene rebuilds a single scene block with its marker and body.
// Preamble scenes have no marker; clip and narration scenes keep theirs.
func assembleScene(s ParsedScene, body string) string {
	if s.Kind == "preamble" {
		return body
	}
	if s.Marker == "" {
		return body
	}
	return s.Marker + "\n" + body
}

// sceneBudget returns the word budget for a single clip scene at the given
// 0-based index. Opening and closing scenes get a 1.2x bump because they
// typically carry the hook and the closing beat; middle scenes get the
// even split. Narration scenes are not routed through this helper — the
// caller is responsible for skipping them.
//
// Exposed as a package-private helper so the per-scene budget logic can
// be unit-tested without spinning up a fake generator.
func sceneBudget(perClip, clipIdx, totalClipScenes int) int {
	if totalClipScenes <= 0 {
		return perClip
	}
	if clipIdx == 0 || clipIdx == totalClipScenes-1 {
		return int(float64(perClip) * 1.2)
	}
	return perClip
}

// ollamaOpts is a small helper to build the num_predict option map.
func ollamaOpts(numPredict int) map[string]any {
	if numPredict <= 0 {
		return nil
	}
	return map[string]any{"num_predict": numPredict}
}
