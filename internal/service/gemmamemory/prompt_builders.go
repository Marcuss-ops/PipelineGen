package gemmamemory

import (
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama/prompts"
	"velox/go-master/pkg/textutil"
)

// BuildEnrichedPrompt adds memory context to the user's generation request.
func BuildEnrichedPrompt(req MemoryGateRequest, hits []MemoryHit) string {
	if len(hits) == 0 {
		return ""
	}

	policy := req.Policy
	if policy.MaxMemoryChars <= 0 {
		policy = DefaultMemoryPolicy()
	}

	sections := classifyHits(hits)

	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderMemoryEnriched(req.Title, req.Prompt, req.Language, sections)
		if err == nil {
			return truncateToPolicy(rendered, policy)
		}
	}

	return buildEnrichedPromptFallback(req, sections, policy)
}

// BuildFreshVariantPrompt builds a prompt that forces the LLM to produce a
// genuinely different script when an exact cache hit is detected.
func BuildFreshVariantPrompt(basePrompt string, exact *GenerationOutput) string {
	trimmed := strings.TrimSpace(basePrompt)
	if exact == nil || strings.TrimSpace(exact.OutputText) == "" {
		return trimmed
	}

	policy := DefaultMemoryPolicy()
	fragments := extractFragments(exact.OutputText, 5, 12)

	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderMemoryFreshVariant(trimmed, fragments)
		if err == nil {
			return truncateToPolicy(rendered, policy)
		}
	}

	return buildFreshVariantFallback(trimmed, fragments, policy)
}

// classifyHits groups memory hits into typed sections for the enriched prompt.
func classifyHits(hits []MemoryHit) []prompts.MemorySection {
	var channelRules, pastScripts, research, topicContext []MemoryHit
	for _, h := range hits {
		switch {
		case h.Source == "channel_style":
			channelRules = append(channelRules, h)
		case h.Source == "topic_key" && (h.Entry.MemoryType == MemoryTypeSuccessfulHook || h.Entry.MemoryType == MemoryTypeScriptStructure):
			pastScripts = append(pastScripts, h)
		case h.Source == "topic_key" && (h.Entry.MemoryType == MemoryTypeTopicResearch || h.Entry.MemoryType == MemoryTypeCharacterProfile):
			research = append(research, h)
		case h.Source == "search", h.Source == "recent":
			pastScripts = append(pastScripts, h)
		default:
			topicContext = append(topicContext, h)
		}
	}

	const perItemSummaryChars = 200
	const maxPastScriptItems = 2
	const pastScriptSummaryChars = 150

	var sections []prompts.MemorySection

	if len(channelRules) > 0 {
		items := make([]string, 0, len(channelRules))
		for _, h := range channelRules {
			items = append(items, fmt.Sprintf("- %s", textutil.Truncate(h.Entry.Summary, perItemSummaryChars)))
		}
		sections = append(sections, prompts.MemorySection{Type: "channel", Items: items})
	}

	if len(pastScripts) > 0 {
		items := make([]string, 0, maxPastScriptItems)
		for i, h := range pastScripts {
			if i >= maxPastScriptItems {
				break
			}
			items = append(items, fmt.Sprintf("- [%s] %s", h.Entry.MemoryType, textutil.Truncate(h.Entry.Summary, pastScriptSummaryChars)))
		}
		sections = append(sections, prompts.MemorySection{Type: "past", Items: items})
	}

	if len(research) > 0 {
		items := make([]string, 0, len(research))
		for _, h := range research {
			items = append(items, fmt.Sprintf("- [%s] %s", h.Entry.MemoryType, textutil.Truncate(h.Entry.Summary, perItemSummaryChars)))
		}
		sections = append(sections, prompts.MemorySection{Type: "research", Items: items})
	}

	if len(topicContext) > 0 {
		items := make([]string, 0, len(topicContext))
		for _, h := range topicContext {
			items = append(items, fmt.Sprintf("- %s", textutil.Truncate(h.Entry.Summary, perItemSummaryChars)))
		}
		sections = append(sections, prompts.MemorySection{Type: "other", Items: items})
	}

	return sections
}

func truncateToPolicy(text string, policy MemoryPolicy) string {
	if policy.MaxMemoryChars > 0 && len(text) > policy.MaxMemoryChars {
		return text[:policy.MaxMemoryChars] + "\n[...memory context truncated to prevent verbatim reuse...]"
	}
	return text
}

func buildEnrichedPromptFallback(req MemoryGateRequest, sections []prompts.MemorySection, policy MemoryPolicy) string {
	var b strings.Builder
	for _, sec := range sections {
		switch sec.Type {
		case "channel":
			b.WriteString("CHANNEL MEMORY:\n")
		case "past":
			b.WriteString("RELEVANT PAST SCRIPTS:\n")
		case "research":
			b.WriteString("RESEARCH MEMORY:\n")
		default:
			b.WriteString("ADDITIONAL CONTEXT:\n")
		}
		for _, item := range sec.Items {
			b.WriteString(item + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Use the above memory as context for writing. Treat it as inspiration only: do not copy phrasing, openings, or paragraph order from prior runs. Reuse facts and style, but write a fresh version.\n\n")
	b.WriteString("USER REQUEST:\n")
	b.WriteString(fmt.Sprintf("Write a script about: %s\n", req.Title))
	if req.Prompt != "" && req.Prompt != req.Title {
		b.WriteString(fmt.Sprintf("Details: %s\n", req.Prompt))
	}
	b.WriteString(fmt.Sprintf("Language: %s\n", req.Language))

	return truncateToPolicy(b.String(), policy)
}

func buildFreshVariantFallback(basePrompt string, fragments []string, policy MemoryPolicy) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\n[FRESH_VARIANT_INSTRUCTIONS]\n")
	b.WriteString("A previous run on the same topic already exists. You MUST write a GENUINELY DIFFERENT version. To achieve this:\n")
	b.WriteString("- Choose a DIFFERENT narrative angle or perspective.\n")
	b.WriteString("- Use a DIFFERENT opening hook.\n")
	b.WriteString("- Vary the pacing.\n")
	b.WriteString("- Introduce at least ONE new example, anecdote, or reference.\n")
	b.WriteString("- Reorder the sections.\n")
	b.WriteString("- Change the closing.\n\n")

	b.WriteString("[PREVIOUS_RUN_AVOID_LIST]\n")
	b.WriteString("Do NOT reuse any of these specific phrases from the previous run:\n")
	for _, frag := range fragments {
		b.WriteString("- " + frag + "\n")
	}
	b.WriteString("[/PHRASES_TO_AVOID]\n\n")
	b.WriteString("Write a fresh version with a completely different angle.\n")

	return truncateToPolicy(b.String(), policy)
}

// extractFragments returns up to maxFragments short snippets sampled from the text.
func extractFragments(text string, maxFragments, wordWindow int) []string {
	if wordWindow < 6 {
		wordWindow = 12
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var out []string
	step := len(words) / (maxFragments + 1)
	if step < wordWindow {
		step = wordWindow
	}
	for i := 0; i < len(words) && len(out) < maxFragments; i += step {
		end := i + wordWindow
		if end > len(words) {
			end = len(words)
		}
		if end-i < 4 {
			continue
		}
		frag := strings.Join(words[i:end], " ")
		if len(frag) > 180 {
			frag = frag[:180] + "..."
		}
		out = append(out, frag)
	}
	return out
}
