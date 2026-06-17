package scriptcore

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseResearchPack attempts to parse the agent output as a ResearchPack JSON.
// If the output is not valid JSON, it falls back to wrapping the raw text in a
// ResearchPack with RawText set — this handles the transitional period where
// the Python agent has not yet been updated to emit structured output.
func ParseResearchPack(raw string) (*ResearchPack, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty research output")
	}

	// Attempt to find JSON in the output — the agent may wrap it in
	// markdown code blocks or print metadata after the script text.
	jsonStart := strings.Index(raw, "{")
	jsonEnd := strings.LastIndex(raw, "}")

	// Try full-document parse first
	var pack ResearchPack
	if jsonStart == 0 && jsonEnd == len(raw)-1 {
		if err := json.Unmarshal([]byte(raw), &pack); err == nil {
			// Only accept if it has at least one research field filled
			if pack.Topic != "" || len(pack.KeyFacts) > 0 || len(pack.Sources) > 0 {
				return &pack, nil
			}
		}
	}

	// Try parsing just the JSON portion (handles trailing console output)
	if jsonStart != -1 && jsonEnd > jsonStart {
		candidate := raw[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(candidate), &pack); err == nil {
			if pack.Topic != "" || len(pack.KeyFacts) > 0 || len(pack.Sources) > 0 {
				return &pack, nil
			}
		}
	}

	// Fallback: wrap raw text
	return &ResearchPack{
		Topic:   extractTopic(raw),
		RawText: raw,
	}, nil
}

// FormatResearchContext formats a ResearchPack into a human-readable text
// block suitable for injecting into the LLM prompt as WebContext.
func FormatResearchContext(pack *ResearchPack) string {
	if pack == nil {
		return ""
	}

	// If we only have raw text, return it as-is
	if pack.RawText != "" {
		return pack.RawText
	}

	var b strings.Builder

	if pack.Topic != "" {
		b.WriteString(fmt.Sprintf("Research Topic: %s\n\n", pack.Topic))
	}

	if len(pack.KeyFacts) > 0 {
		b.WriteString("Key Facts:\n")
		for _, f := range pack.KeyFacts {
			b.WriteString(fmt.Sprintf("- %s\n", f))
		}
		b.WriteString("\n")
	}

	if len(pack.Timeline) > 0 {
		b.WriteString("Timeline:\n")
		for _, t := range pack.Timeline {
			if t.Date != "" {
				b.WriteString(fmt.Sprintf("- %s: %s\n", t.Date, t.Event))
			} else {
				b.WriteString(fmt.Sprintf("- %s\n", t.Event))
			}
		}
		b.WriteString("\n")
	}

	if len(pack.Controversies) > 0 {
		b.WriteString("Controversies / Debated Points:\n")
		for _, c := range pack.Controversies {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
		b.WriteString("\n")
	}

	if len(pack.ImportantQuotes) > 0 {
		b.WriteString("Important Quotes:\n")
		for _, q := range pack.ImportantQuotes {
			b.WriteString(fmt.Sprintf("- \"%s\"\n", q))
		}
		b.WriteString("\n")
	}

	if len(pack.SuggestedAngles) > 0 {
		b.WriteString("Suggested Angles:\n")
		for _, a := range pack.SuggestedAngles {
			b.WriteString(fmt.Sprintf("- %s\n", a))
		}
		b.WriteString("\n")
	}

	if len(pack.Warnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, w := range pack.Warnings {
			b.WriteString(fmt.Sprintf("- ⚠️  %s\n", w))
		}
		b.WriteString("\n")
	}

	if len(pack.Sources) > 0 {
		b.WriteString("Sources:\n")
		for _, s := range pack.Sources {
			line := s.Title
			if s.URL != "" {
				line = fmt.Sprintf("[%s](%s)", s.Title, s.URL)
			}
			b.WriteString(fmt.Sprintf("- %s\n", line))
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

// extractTopic attempts to extract a topic string from free-form text.
func extractTopic(text string) string {
	lines := strings.SplitN(strings.TrimSpace(text), "\n", 3)
	if len(lines) > 0 {
		candidate := strings.TrimSpace(lines[0])
		// Skip empty lines and headers
		if candidate != "" && !strings.HasPrefix(candidate, "#") {
			if len(candidate) > 200 {
				candidate = candidate[:200]
			}
			return candidate
		}
	}
	return ""
}
