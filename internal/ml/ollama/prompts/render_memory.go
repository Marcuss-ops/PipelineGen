package prompts

import (
	"bytes"
	"fmt"
)

// RenderMemoryEnriched renders the enriched memory prompt body (sections + instruction + user request).
func (c *Config) RenderMemoryEnriched(title, prompt, language string, sections []MemorySection) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	var buf bytes.Buffer

	for _, sec := range sections {
		switch sec.Type {
		case "channel":
			buf.WriteString(c.MemoryEnriched.Sections.ChannelMemory + "\n")
		case "past":
			buf.WriteString(c.MemoryEnriched.Sections.PastScripts + "\n")
		case "research":
			buf.WriteString(c.MemoryEnriched.Sections.ResearchMemory + "\n")
		default:
			buf.WriteString(c.MemoryEnriched.Sections.AdditionalContext + "\n")
		}
		for _, item := range sec.Items {
			buf.WriteString(fmt.Sprintf("- %s\n", item))
		}
		buf.WriteString("\n")
	}

	buf.WriteString(c.MemoryEnriched.Instruction + "\n\n")
	buf.WriteString(c.MemoryEnriched.UserRequestLabel + "\n")

	line, err := render(c.MemoryEnriched.WriteScriptLine, map[string]any{"Title": title})
	if err != nil {
		return "", err
	}
	buf.WriteString(line + "\n")

	if prompt != "" && prompt != title {
		detailLine, err := render(c.MemoryEnriched.DetailsLine, map[string]any{"Prompt": prompt})
		if err != nil {
			return "", err
		}
		buf.WriteString(detailLine + "\n")
	}

	langLine, err := render(c.MemoryEnriched.LanguageLine, map[string]any{"Language": language})
	if err != nil {
		return "", err
	}
	buf.WriteString(langLine + "\n")

	return buf.String(), nil
}

// RenderMemoryFreshVariant renders the fresh variant prompt.
func (c *Config) RenderMemoryFreshVariant(basePrompt string, fragments []string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("prompts config not initialized")
	}
	var buf bytes.Buffer
	buf.WriteString(basePrompt)
	buf.WriteString("\n\n")
	buf.WriteString(c.MemoryFreshVariant.AngleShiftHeader + "\n")
	buf.WriteString(c.MemoryFreshVariant.AngleShiftBody)

	buf.WriteString("\n")
	buf.WriteString(c.MemoryFreshVariant.AvoidListHeader + "\n")
	buf.WriteString(c.MemoryFreshVariant.AvoidListIntro + "\n")
	for _, frag := range fragments {
		buf.WriteString("- " + frag + "\n")
	}
	buf.WriteString(c.MemoryFreshVariant.AvoidListFooter + "\n\n")
	buf.WriteString(c.MemoryFreshVariant.FinalInstruction + "\n")

	return buf.String(), nil
}
