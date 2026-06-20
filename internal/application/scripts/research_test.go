package scripts

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── ParseResearchPack: valid JSON ─────────────────────────────────────────

func TestParseResearchPack_ValidFullJSON(t *testing.T) {
	input := `{"topic":"Roman Empire","key_facts":["Lasted over 500 years","Capital was Rome"],"sources":[{"url":"https://example.com/rome","title":"Roman History","source_type":"web"}]}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pack.Topic != "Roman Empire" {
		t.Errorf("Topic = %q, want %q", pack.Topic, "Roman Empire")
	}
	if len(pack.KeyFacts) != 2 {
		t.Fatalf("len(KeyFacts) = %d, want 2", len(pack.KeyFacts))
	}
	if pack.KeyFacts[0] != "Lasted over 500 years" {
		t.Errorf("KeyFacts[0] = %q, want %q", pack.KeyFacts[0], "Lasted over 500 years")
	}
	if len(pack.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(pack.Sources))
	}
	if pack.Sources[0].URL != "https://example.com/rome" {
		t.Errorf("Sources[0].URL = %q, want %q", pack.Sources[0].URL, "https://example.com/rome")
	}
	if pack.RawText != "" {
		t.Errorf("RawText should be empty for parsed JSON, got %q", pack.RawText)
	}
}

func TestParseResearchPack_AllFields(t *testing.T) {
	input := `{
		"topic": "Space Exploration",
		"key_facts": [
			"First moon landing in 1969",
			"International Space Station launched in 1998"
		],
		"timeline": [
			{"date": "1957", "event": "Sputnik 1 launched"},
			{"date": "1969", "event": "Apollo 11 moon landing"}
		],
		"controversies": ["Funding for space programs is debated"],
		"important_quotes": ["That's one small step for man"],
		"suggested_angles": ["The future of Mars colonization"],
		"warnings": ["Avoid outdated sources about Pluto's status"],
		"sources": [
			{"url": "https://nasa.gov", "title": "NASA Official Site", "source_type": "web"},
			{"url": "https://example.com/space", "title": "Space History Blog", "source_type": "web"}
		]
	}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pack.Topic != "Space Exploration" {
		t.Errorf("Topic = %q, want %q", pack.Topic, "Space Exploration")
	}
	if len(pack.KeyFacts) != 2 {
		t.Fatalf("len(KeyFacts) = %d, want 2", len(pack.KeyFacts))
	}
	if len(pack.Timeline) != 2 {
		t.Fatalf("len(Timeline) = %d, want 2", len(pack.Timeline))
	}
	if pack.Timeline[0].Date != "1957" || pack.Timeline[0].Event != "Sputnik 1 launched" {
		t.Errorf("Timeline[0] = %+v, want {1957 Sputnik 1 launched}", pack.Timeline[0])
	}
	if len(pack.Controversies) != 1 {
		t.Fatalf("len(Controversies) = %d, want 1", len(pack.Controversies))
	}
	if len(pack.ImportantQuotes) != 1 {
		t.Fatalf("len(ImportantQuotes) = %d, want 1", len(pack.ImportantQuotes))
	}
	if len(pack.SuggestedAngles) != 1 {
		t.Fatalf("len(SuggestedAngles) = %d, want 1", len(pack.SuggestedAngles))
	}
	if len(pack.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1", len(pack.Warnings))
	}
	if len(pack.Sources) != 2 {
		t.Fatalf("len(Sources) = %d, want 2", len(pack.Sources))
	}
}

// ── ParseResearchPack: edge cases ─────────────────────────────────────────

func TestParseResearchPack_JSONInMarkdownCodeBlock(t *testing.T) {
	input := "```json\n{\"topic\":\"AI History\",\"key_facts\":[\"Turing test proposed in 1950\"]}\n```"

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pack.Topic != "AI History" {
		t.Errorf("Topic = %q, want %q", pack.Topic, "AI History")
	}
	if len(pack.KeyFacts) != 1 {
		t.Fatalf("len(KeyFacts) = %d, want 1", len(pack.KeyFacts))
	}
}

func TestParseResearchPack_JSONWithTrailingText(t *testing.T) {
	input := `{"topic":"Ancient Greece","key_facts":["Democracy originated in Athens"]}
Some additional notes from the agent
Generated at 2026-06-11`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pack.Topic != "Ancient Greece" {
		t.Errorf("Topic = %q, want %q", pack.Topic, "Ancient Greece")
	}
	if len(pack.KeyFacts) != 1 {
		t.Fatalf("len(KeyFacts) = %d, want 1", len(pack.KeyFacts))
	}
}

func TestParseResearchPack_EmptyJSONObject(t *testing.T) {
	// Empty JSON object has no relevant research fields → should fall back
	input := `{}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pack.Topic != "{}" {
		t.Errorf("expected Topic to be first-line of fallback text, got %q", pack.Topic)
	}
	if pack.RawText != "{}" {
		t.Errorf("expected RawText to preserve input, got %q", pack.RawText)
	}
}

func TestParseResearchPack_EmptyInput(t *testing.T) {
	_, err := ParseResearchPack("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error message should mention empty, got: %v", err)
	}
}

func TestParseResearchPack_WhitespaceOnly(t *testing.T) {
	_, err := ParseResearchPack("   \n\t  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only input")
	}
}

func TestParseResearchPack_MalformedJSON(t *testing.T) {
	input := `{"topic": "Broken JSON", "key_facts": [missing quotes]}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if pack.RawText != input {
		t.Errorf("expected RawText to preserve input in fallback")
	}
}

func TestParseResearchPack_PlainTextNoJSON(t *testing.T) {
	input := `This is a plain text output from the agent.
It contains no JSON whatsoever.
Just some research notes about a topic.`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if pack.RawText != input {
		t.Errorf("expected RawText to preserve input")
	}
	if pack.Topic == "" {
		t.Error("expected Topic to be extracted from first line")
	}
}

func TestParseResearchPack_JSONWithOnlySources(t *testing.T) {
	// Sources alone should be enough to accept as structured
	input := `{"sources":[{"url":"https://example.com","title":"Example","source_type":"web"}]}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pack.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(pack.Sources))
	}
	if pack.Sources[0].URL != "https://example.com" {
		t.Errorf("Sources[0].URL = %q", pack.Sources[0].URL)
	}
	if pack.RawText != "" {
		t.Errorf("RawText should be empty for parsed JSON, got %q", pack.RawText)
	}
}

func TestParseResearchPack_KeyFactsAlone(t *testing.T) {
	// KeyFacts alone should be enough to accept as structured
	input := `{"key_facts":["Only fact here"]}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pack.KeyFacts) != 1 {
		t.Fatalf("len(KeyFacts) = %d, want 1", len(pack.KeyFacts))
	}
}

func TestParseResearchPack_JSONWithOnlyExtraFields(t *testing.T) {
	// JSON has fields but none of the recognized research fields → fallback
	input := `{"name":"test","version":1,"data":[1,2,3]}`

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if pack.RawText != input {
		t.Errorf("expected RawText to preserve input for unrecognized JSON structure")
	}
}

// ── FormatResearchContext ─────────────────────────────────────────────────

func TestFormatResearchContext_WithParsedPack(t *testing.T) {
	pack := &script.ResearchPack{
		Topic: "Roman Empire",
		KeyFacts: []string{
			"Lasted over 500 years",
			"Capital was Rome",
		},
		Timeline: []script.TimelineEntry{
			{Date: "753 BCE", Event: "Founding of Rome"},
			{Date: "476 CE", Event: "Fall of Western Empire"},
		},
		Controversies:   []string{"Debate over lead poisoning theory"},
		ImportantQuotes: []string{"All roads lead to Rome"},
		SuggestedAngles: []string{"Military innovations"},
		Warnings:        []string{"Bias in Roman historical sources"},
		Sources: []script.ResearchSource{
			{URL: "https://example.com/rome", Title: "Roman History"},
		},
	}

	output := FormatResearchContext(pack)

	// Check all sections are present
	checks := []string{
		"Research Topic: Roman Empire",
		"Key Facts:",
		"- Lasted over 500 years",
		"- Capital was Rome",
		"Timeline:",
		"- 753 BCE: Founding of Rome",
		"- 476 CE: Fall of Western Empire",
		"Controversies / Debated Points:",
		"- Debate over lead poisoning theory",
		"Important Quotes:",
		"- \"All roads lead to Rome\"",
		"Suggested Angles:",
		"- Military innovations",
		"Warnings:",
		"- ⚠️  Bias in Roman historical sources",
		"Sources:",
		"- [Roman History](https://example.com/rome)",
	}

	for _, c := range checks {
		if !strings.Contains(output, c) {
			t.Errorf("expected output to contain:\n  %q\nfull output:\n%s", c, output)
		}
	}
}

func TestFormatResearchContext_RawTextFallback(t *testing.T) {
	raw := "This is raw text from the agent without JSON."
	pack := &script.ResearchPack{RawText: raw}

	output := FormatResearchContext(pack)
	if output != raw {
		t.Errorf("expected RawText to be returned as-is, got %q", output)
	}
}

func TestFormatResearchContext_NilPack(t *testing.T) {
	output := FormatResearchContext(nil)
	if output != "" {
		t.Errorf("expected empty string for nil pack, got %q", output)
	}
}

func TestFormatResearchContext_EmptyPack(t *testing.T) {
	pack := &script.ResearchPack{}
	output := FormatResearchContext(pack)
	if output != "" {
		t.Errorf("expected empty string for empty pack, got %q", output)
	}
}

func TestFormatResearchContext_EmptySections(t *testing.T) {
	// Pack with Topic set but all sections empty — should only render topic line
	pack := &script.ResearchPack{
		Topic: "Only Topic",
	}

	output := FormatResearchContext(pack)
	if output != "Research Topic: Only Topic" {
		t.Errorf("expected just topic line, got %q", output)
	}
}

func TestFormatResearchContext_TimelineWithoutDates(t *testing.T) {
	// Timeline entries may omit the Date field. In that case they should be
	// rendered as bare event bullets without a date prefix.
	pack := &script.ResearchPack{
		Topic: "Persian Empire",
		Timeline: []script.TimelineEntry{
			{Event: "Cyrus the Great unites Persia"},
			{Event: "Persian wars against Greek city-states"},
			{Event: "Alexander the Great conquers Persia"},
		},
	}

	output := FormatResearchContext(pack)

	// Should contain timeline header
	if !strings.Contains(output, "Timeline:") {
		t.Errorf("output should contain 'Timeline:' section\noutput:\n%s", output)
	}

	// Should contain events WITHOUT date prefix
	checks := []string{
		"- Cyrus the Great unites Persia",
		"- Persian wars against Greek city-states",
		"- Alexander the Great conquers Persia",
	}
	for _, c := range checks {
		if !strings.Contains(output, c) {
			t.Errorf("output should contain:\n  %q\nfull output:\n%s", c, output)
		}
	}

	// Should NOT contain any colon-separated date prefix on timeline lines
	// (e.g., "- 123 BCE: Event" should NOT appear since dates are empty)
	if strings.Contains(output, ":") {
		// The only colon should be in "Research Topic: Persian Empire" and "Timeline:"
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "- ") && strings.Contains(line, ":") {
				t.Errorf("timeline entry without date should not contain colon, got: %q", line)
			}
		}
	}
}

func TestFormatResearchContext_MixedTimelineDatesAndNoDates(t *testing.T) {
	// Realistic scenario: some timeline entries have dates, others don't.
	// The function must handle each entry individually.
	pack := &script.ResearchPack{
		Topic: "Renaissance Art",
		Timeline: []script.TimelineEntry{
			{Date: "1300", Event: "Proto-Renaissance begins in Italy"},
			{Event: "Da Vinci's Mona Lisa is painted"},
			{Date: "1508", Event: "Michelangelo starts Sistine Chapel ceiling"},
			{Event: "Raphael's School of Athens is completed"},
		},
	}

	output := FormatResearchContext(pack)

	// Entries WITH dates should have date: event format
	if !strings.Contains(output, "- 1300: Proto-Renaissance begins in Italy") {
		t.Errorf("dated entry should render as '- date: event'\noutput:\n%s", output)
	}
	if !strings.Contains(output, "- 1508: Michelangelo starts Sistine Chapel ceiling") {
		t.Errorf("dated entry should render as '- date: event'\noutput:\n%s", output)
	}

	// Entries WITHOUT dates should have just the event
	if !strings.Contains(output, "- Da Vinci's Mona Lisa is painted") {
		t.Errorf("undated entry should render as '- event'\noutput:\n%s", output)
	}
	if !strings.Contains(output, "- Raphael's School of Athens is completed") {
		t.Errorf("undated entry should render as '- event'\noutput:\n%s", output)
	}
}

// ── extractTopic (indirect) ───────────────────────────────────────────────

func TestParseResearchPack_ExtractTopicFromFirstLine(t *testing.T) {
	input := "Ancient Egypt\nThe civilization along the Nile river developed over three millennia."

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if pack.Topic != "Ancient Egypt" {
		t.Errorf("expected Topic extracted from first line, got %q", pack.Topic)
	}
}

func TestParseResearchPack_ExtractTopicSkipsHeader(t *testing.T) {
	input := "# Research Notes\n\nThis is some research about quantum computing."

	pack, err := ParseResearchPack(input)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if pack.Topic != "" {
		t.Errorf("expected empty Topic for header-only first line, got %q", pack.Topic)
	}
}
