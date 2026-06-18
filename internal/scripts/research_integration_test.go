package scripts

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResearchToWriteScriptPipeline simulates the full integration flow that
// phase_research.go performs when the Python agent produces structured JSON:
//
//  1. agent output (ResearchPack JSON)
//  2. ParseResearchPack()  → structured pack
//  3. FormatResearchContext()  → formatted text for LLM prompt
//  4. Build ScriptGenerationPlan with AgentResearch + WebContext
//  5. Build WriteScriptRequest with Plan
//  6. Plan fields flow correctly through plan-apply block
//  7. ValidateScript passes on a script written from the research
//
// This test does NOT call Engine.WriteScript (it needs a real server), but
// it validates every step the handler performs before that call.
func TestResearchToWriteScriptPipeline(t *testing.T) {
	// ── Step 1: Simulate agent output (like the Python agent now produces) ─
	agentJSON := `{
		"topic": "Roman Empire Collapse",
		"key_facts": [
			"The Western Roman Empire fell in 476 CE",
			"Economic decline weakened the empire over centuries",
			"Barbarian invasions contributed to the collapse",
			"The empire split into Eastern and Western halves in 395 CE",
			"Corruption and political instability were major factors",
			"Military overspending strained the economy",
			"The loss of tax revenue from conquered territories hurt Rome"
		],
		"timeline": [
			{"date": "117 CE", "event": "Roman Empire reaches maximum territorial extent"},
			{"date": "284 CE", "event": "Diocletian splits empire into administrative divisions"},
			{"date": "395 CE", "event": "Final division into Eastern and Western Roman Empires"},
			{"date": "410 CE", "event": "Visigoths sack Rome"},
			{"date": "476 CE", "event": "Romulus Augustulus deposed; Western Empire falls"}
		],
		"controversies": [
			"Lead poisoning theory is debated by modern historians",
			"Primary vs secondary role of barbarian migrations"
		],
		"important_quotes": [
			"Rome was not built in a day",
			"The decline of Rome was the natural and inevitable effect of immoderate greatness"
		],
		"suggested_angles": [
			"Economic factors as the primary cause of collapse",
			"The role of climate change and environmental factors",
			"Comparing Rome's fall to modern societal challenges"
		],
		"warnings": [
			"Avoid overly simplistic single-cause explanations",
			"Be careful with anachronistic comparisons to modern politics"
		],
		"sources": [
			{"url": "https://en.wikipedia.org/wiki/Fall_of_the_Western_Roman_Empire", "title": "Fall of the Western Roman Empire", "source_type": "web"},
			{"url": "https://www.britannica.com/event/Fall-of-the-Roman-Empire", "title": "Fall of the Roman Empire", "source_type": "web"}
		]
	}`

	// ── Step 2: ParseResearchPack ─────────────────────────────────────────
	pack, err := ParseResearchPack(agentJSON)
	if err != nil {
		t.Fatalf("ParseResearchPack failed: %v", err)
	}
	if pack == nil {
		t.Fatal("ParseResearchPack returned nil")
	}

	// Verify all fields are parsed correctly — this is the data that flows
	// into the ScriptGenerationPlan and ultimately into the LLM prompt.
	if pack.Topic != "Roman Empire Collapse" {
		t.Errorf("Topic = %q, want %q", pack.Topic, "Roman Empire Collapse")
	}
	if len(pack.KeyFacts) != 7 {
		t.Errorf("len(KeyFacts) = %d, want 7", len(pack.KeyFacts))
	}
	if len(pack.Timeline) != 5 {
		t.Errorf("len(Timeline) = %d, want 5", len(pack.Timeline))
	}
	if len(pack.Controversies) != 2 {
		t.Errorf("len(Controversies) = %d, want 2", len(pack.Controversies))
	}
	if len(pack.ImportantQuotes) != 2 {
		t.Errorf("len(ImportantQuotes) = %d, want 2", len(pack.ImportantQuotes))
	}
	if len(pack.SuggestedAngles) != 3 {
		t.Errorf("len(SuggestedAngles) = %d, want 3", len(pack.SuggestedAngles))
	}
	if len(pack.Warnings) != 2 {
		t.Errorf("len(Warnings) = %d, want 2", len(pack.Warnings))
	}
	if len(pack.Sources) != 2 {
		t.Errorf("len(Sources) = %d, want 2", len(pack.Sources))
	}
	if pack.RawText != "" {
		t.Errorf("RawText should be empty for valid JSON, got %q", pack.RawText)
	}

	// ── Step 3: FormatResearchContext ─────────────────────────────────────
	formatted := FormatResearchContext(pack)
	if formatted == "" {
		t.Fatal("FormatResearchContext returned empty string")
	}

	// Verify the formatted output contains key sections
	sectionChecks := []string{
		"Research Topic: Roman Empire Collapse",
		"Key Facts:",
		"- The Western Roman Empire fell in 476 CE",
		"- Military overspending strained the economy",
		"Timeline:",
		"- 476 CE: Romulus Augustulus deposed; Western Empire falls",
		"Controversies / Debated Points:",
		"- Lead poisoning theory is debated by modern historians",
		"Important Quotes:",
		"- \"Rome was not built in a day\"",
		"Suggested Angles:",
		"- Economic factors as the primary cause of collapse",
		"Warnings:",
		"- ⚠️  Avoid overly simplistic single-cause explanations",
		"Sources:",
		"- [Fall of the Western Roman Empire](https://en.wikipedia.org/wiki/Fall_of_the_Western_Roman_Empire)",
	}
	for _, check := range sectionChecks {
		if !strings.Contains(formatted, check) {
			t.Errorf("formatted output should contain:\n  %q\nfull:\n%s", check, formatted)
		}
	}

	// ── Step 4: Build ScriptGenerationPlan ────────────────────────────────
	// This is what phase_research.go does after receiving the structured pack.
	plan := &ScriptGenerationPlan{
		Topic:         "Roman Empire Collapse",
		Title:         "The Fall of Rome",
		Language:      "en",
		Tone:          "documentary",
		Mode:          "clip_to_script",
		Duration:      600,
		TargetWords:   700,
		SourceText:    "",
		Prompt:        "Roman Empire Collapse",
		WebContext:    formatted, // formatted research → LLM prompt
		AgentResearch: pack,      // structured data → available for source mapping
		UseMemory:     true,
		SaveToDB:      false,
		PromptVersion: "v1",
	}

	// ── Step 5: Build WriteScriptRequest with Plan ────────────────────────
	// Verify the plan-apply block correctly copies fields.
	writeReq := WriteScriptRequest{
		Plan: plan,
		// No top-level overrides — all fields come from Plan
	}

	// Simulate the plan-apply block from Engine.WriteScript.
	// We do this manually here to verify correctness without needing an Engine.
	if writeReq.Plan != nil {
		if writeReq.Topic == "" {
			writeReq.Topic = writeReq.Plan.Topic
		}
		if writeReq.Title == "" {
			writeReq.Title = writeReq.Plan.Title
		}
		if writeReq.Language == "" {
			writeReq.Language = writeReq.Plan.Language
		}
		if writeReq.Tone == "" {
			writeReq.Tone = writeReq.Plan.Tone
		}
		if writeReq.Mode == "" {
			writeReq.Mode = writeReq.Plan.Mode
		}
		if writeReq.Duration <= 0 {
			writeReq.Duration = writeReq.Plan.Duration
		}
		if writeReq.MinWords <= 0 {
			writeReq.MinWords = writeReq.Plan.TargetWords
		}
		if writeReq.Prompt == "" {
			writeReq.Prompt = writeReq.Plan.Prompt
		}
		if writeReq.SourceText == "" {
			writeReq.SourceText = writeReq.Plan.SourceText
		}
		if writeReq.WebContext == "" {
			writeReq.WebContext = writeReq.Plan.WebContext
		}
		if writeReq.PromptVersion == "" {
			writeReq.PromptVersion = writeReq.Plan.PromptVersion
		}
	}

	// ── Step 6: Verify plan fields flowed correctly ───────────────────────
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Topic", writeReq.Topic, "Roman Empire Collapse"},
		{"Title", writeReq.Title, "The Fall of Rome"},
		{"Language", writeReq.Language, "en"},
		{"Tone", writeReq.Tone, "documentary"},
		{"Mode", writeReq.Mode, "clip_to_script"},
		{"Prompt", writeReq.Prompt, "Roman Empire Collapse"},
		{"SourceText", writeReq.SourceText, ""},
		{"PromptVersion", writeReq.PromptVersion, "v1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("WriteScriptRequest.%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	intChecks := []struct {
		name string
		got  int
		want int
	}{
		{"Duration", writeReq.Duration, 600},
		{"MinWords", writeReq.MinWords, 700},
	}
	for _, c := range intChecks {
		if c.got != c.want {
			t.Errorf("WriteScriptRequest.%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	// Verify WebContext contains the formatted research
	if !strings.Contains(writeReq.WebContext, "Roman Empire Collapse") {
		t.Error("WebContext should contain the research topic")
	}
	if !strings.Contains(writeReq.WebContext, "Barbarian invasions") {
		t.Error("WebContext should contain key facts from research")
	}

	// ── Step 7: ValidateScript on a script written from this research ─────
	// This simulates the post-generation validation from GenerateText.
	// A realistic script based on the Roman Empire research should pass all
	// quality checks (word count, no markdown, no stage directions,
	// acceptable repetition, strong hook, CTA in closing).
	script := `The Roman Empire did not fall in a single day. It took centuries of gradual decline, 
economic pressure, and political decay before the Western Empire finally collapsed in 476 CE. 
Understanding why Rome fell helps us understand our own civilization vulnerabilities.

In 117 CE the Roman Empire reached its maximum territorial extent stretching from Britain to 
Egypt and from Spain to Mesopotamia. Governing such a vast territory required an enormous 
administrative and military apparatus. The cost of maintaining legions on every frontier 
gradually drained the imperial treasury. Roman governors were tasked with managing these vast regions 
and collecting taxes from dozens of distinct cultures and economic systems.

Diocletian recognized the administrative challenge when he split the empire into eastern and 
western halves in 284 CE. This division became permanent in 395 CE creating two distinct 
empires with different trajectories. The Eastern Empire based in Constantinople would survive 
for another thousand years but the Western Empire was already showing cracks. The administrative reforms 
of Diocletian were intended to improve governance but they also created new layers of bureaucracy.

Military overspending was a critical factor. At its peak Rome maintained over 300,000 soldiers 
across three continents. Paying salaries supplying equipment and building fortifications consumed 
most of the imperial budget. As conquests stopped the flow of plunder and slaves that had 
financed earlier expansion the empire had to raise taxes on an already struggling population. 
The Roman military was once the source of the empire strength but it became an enormous financial burden 
that successive emperors could not sustain without constant territorial expansion.

Corruption pervaded every level of Roman government. Provincial governors extorted wealth 
from their subjects. Tax collectors pocketed a portion of what they collected. The imperial 
court was rife with intrigue and assassination. Between 235 and 284 CE Rome saw twenty 
emperors and most died violently. This period of instability known as the Crisis of the Third Century 
almost caused the empire to collapse centuries before its eventual fall in 476.

The loss of tax revenue from conquered territories created a vicious cycle. Less money meant 
fewer soldiers which meant less territorial control which meant even less tax revenue. 
Barbarian groups crossed the Rhine and Danube borders with increasing frequency. In 410 CE 
the Visigoths famously sacked Rome itself a psychological blow from which the Western Empire 
never fully recovered. The sack of Rome sent shockwaves throughout the Mediterranean world 
and marked a turning point in the empire decline.

By 476 CE the last Western emperor Romulus Augustulus was deposed by the Germanic general 
Odoacer. The Eastern Empire continued but the Western Roman Empire had ceased to exist as 
a political entity. Its legacy however endured in law language architecture and governance 
systems that still shape Western civilization today. The Latin language evolved into the Romance languages 
and Roman legal principles form the foundation of many modern legal systems across Europe.

Historians debate whether Rome fell or merely transformed. The lead poisoning theory which 
blamed the decline on lead pipes and vessels has been largely discredited. What remains clear 
is that the fall of Rome was not caused by a single factor but by the interaction of economic 
military political and social pressures over centuries. Modern scholarship emphasizes the complex interplay 
of internal decay and external pressures that gradually eroded the empire from within.

Understanding the complexity of Rome decline is essential for anyone interested in history. 
The parallels to modern challenges make this ancient story surprisingly relevant today. 
Subscribe for more historical deep dives and share your thoughts on what caused Rome fall 
in the comments below.`

	result := ValidateScript(script, plan)

	// Build a map for easy lookup
	scoreMap := make(map[string]ValidationScore)
	for _, s := range result.Scores {
		scoreMap[s.Check] = s
	}

	// Check each score
	scoreChecks := []struct {
		check    string
		wantPass bool
		desc     string
	}{
		{"word_count_ok", true, "script should be within word count bounds"},
		{"no_markdown", true, "no markdown formatting"},
		{"no_stage_directions", true, "no bracketed stage directions"},
		{"no_repetition", true, "no excessive 4-gram repetition"},
		{"hook_strength", true, "hook should not be a generic opener"},
		{"cta_present", true, "should have a call-to-action in closing"},
	}

	for _, sc := range scoreChecks {
		s, ok := scoreMap[sc.check]
		if !ok {
			t.Errorf("missing check %q", sc.check)
			continue
		}
		if s.Passed != sc.wantPass {
			t.Errorf("%s: Passed=%v, want %v | Message=%q — %s", sc.check, s.Passed, sc.wantPass, s.Message, sc.desc)
		}
	}

	if !result.AllPass {
		t.Error("ValidateScript.AllPass should be true for a script written from research")
		// Log all failing scores for debugging
		for _, s := range result.Scores {
			if !s.Passed {
				t.Logf("  failing: %s = %s (value=%d)", s.Check, s.Message, s.Value)
			}
		}
	}
}

// TestResearchPipelineFallback verifies that when the agent output is plain
// text (not JSON), the pipeline gracefully falls back to raw text mode.
// This simulates what happens when the Python agent hasn't been updated yet
// or when the LLM produces invalid JSON.
func TestResearchPipelineFallback(t *testing.T) {
	rawAgentOutput := `The Roman Empire was one of the most powerful civilizations of the ancient world.
It lasted for over 1000 years and its influence can still be felt today.
The empire fell due to a combination of economic decline, military overspending,
and political corruption.`

	// ── Parse (should fall back to raw text) ──
	pack, err := ParseResearchPack(rawAgentOutput)
	if err != nil {
		t.Fatalf("ParseResearchPack expected no error for text fallback: %v", err)
	}
	if pack == nil {
		t.Fatal("ParseResearchPack returned nil")
	}
	if pack.RawText != rawAgentOutput {
		t.Errorf("RawText should preserve original text")
	}

	// ── Format (should return RawText as-is) ──
	formatted := FormatResearchContext(pack)
	if formatted != rawAgentOutput {
		t.Errorf("FormatResearchContext should return RawText for fallback pack")
	}

	// ── Build plan with raw text as WebContext ──
	plan := &ScriptGenerationPlan{
		Topic:         "Roman Empire",
		Title:         "Roman Empire",
		Language:      "en",
		Tone:          "documentary",
		Mode:          "text",
		Duration:      600,
		TargetWords:   700,
		Prompt:        "Roman Empire",
		SourceText:    rawAgentOutput,
		WebContext:    formatted,
		AgentResearch: pack,
		UseMemory:     true,
	}

	// ── Verify plan fields ──
	if plan.WebContext != rawAgentOutput {
		t.Errorf("WebContext should be raw text, got %q", plan.WebContext)
	}
	if plan.SourceText != rawAgentOutput {
		t.Errorf("SourceText should be raw text, got %q", plan.SourceText)
	}
	if plan.AgentResearch == nil {
		t.Fatal("AgentResearch should be set (fallback pack)")
	}
	if plan.AgentResearch.RawText != rawAgentOutput {
		t.Errorf("AgentResearch.RawText should preserve input")
	}
}

// TestResearchPipelineStructuredCacheKey verifies that the formatted research
// context produces stable cache keys. Two identical research packs should
// produce identical formatted output.
func TestResearchPipelineCacheKeyStability(t *testing.T) {
	pack1 := &ResearchPack{
		Topic: "Space Exploration",
		KeyFacts: []string{
			"First moon landing in 1969",
			"ISS launched in 1998",
		},
		Sources: []ResearchSource{
			{URL: "https://nasa.gov", Title: "NASA"},
		},
	}

	pack2 := &ResearchPack{
		Topic: "Space Exploration",
		KeyFacts: []string{
			"First moon landing in 1969",
			"ISS launched in 1998",
		},
		Sources: []ResearchSource{
			{URL: "https://nasa.gov", Title: "NASA"},
		},
	}

	ctx1 := FormatResearchContext(pack1)
	ctx2 := FormatResearchContext(pack2)

	if ctx1 != ctx2 {
		t.Error("identical research packs should produce identical formatted output")
	}
}

// TestResearchPipelineJSONRoundTrip verifies that the full round trip works:
// Go ResearchPack → JSON → Python agent output → ParseResearchPack → Go ResearchPack.
// This ensures compatibility between the Go ResearchPack struct and the Python
// agent's JSON output format.
func TestResearchPipelineJSONRoundTrip(t *testing.T) {
	original := &ResearchPack{
		Topic: "Artificial Intelligence",
		KeyFacts: []string{
			"Turing test proposed in 1950",
			"Deep Blue defeated Kasparov in 1997",
		},
		Timeline: []TimelineEntry{
			{Date: "1950", Event: "Turing test proposed"},
			{Date: "1997", Event: "Deep Blue beats Kasparov"},
		},
		Controversies: []string{
			"AI safety and alignment debates",
		},
		ImportantQuotes: []string{
			"AI is the new electricity",
		},
		SuggestedAngles: []string{
			"The rise of large language models",
		},
		Warnings: []string{
			"Bias in training data",
		},
		Sources: []ResearchSource{
			{URL: "https://example.com/ai", Title: "AI History", SourceType: "web"},
		},
	}

	// Marshal to JSON (simulating what the Python agent would produce)
	jsonBytes, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Parse back (simulating what ParseResearchPack does in Go handler)
	parsed, err := ParseResearchPack(string(jsonBytes))
	if err != nil {
		t.Fatalf("ParseResearchPack failed: %v", err)
	}
	if parsed == nil {
		t.Fatal("ParseResearchPack returned nil")
	}

	// Verify round trip
	if parsed.Topic != original.Topic {
		t.Errorf("Topic = %q, want %q", parsed.Topic, original.Topic)
	}
	if len(parsed.KeyFacts) != len(original.KeyFacts) {
		t.Errorf("len(KeyFacts) = %d, want %d", len(parsed.KeyFacts), len(original.KeyFacts))
	}
	if parsed.KeyFacts[0] != original.KeyFacts[0] {
		t.Errorf("KeyFacts[0] = %q, want %q", parsed.KeyFacts[0], original.KeyFacts[0])
	}
	if len(parsed.Timeline) != len(original.Timeline) {
		t.Errorf("len(Timeline) = %d, want %d", len(parsed.Timeline), len(original.Timeline))
	}
	if len(parsed.Sources) != len(original.Sources) {
		t.Errorf("len(Sources) = %d, want %d", len(parsed.Sources), len(original.Sources))
	}

	// Verify formatted output is the same
	formattedOriginal := FormatResearchContext(original)
	formattedParsed := FormatResearchContext(parsed)
	if formattedOriginal != formattedParsed {
		t.Error("FormatResearchContext should produce identical output for round-tripped data")
	}
}
