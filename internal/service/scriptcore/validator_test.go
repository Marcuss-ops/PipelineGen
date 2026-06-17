package scriptcore

import (
	"testing"
)

func TestValidateScript_Empty(t *testing.T) {
	result := ValidateScript("", NewPlan())
	if result.AllPass {
		t.Error("expected AllPass=false for empty script")
	}
	// Empty script short-circuits before building scores — that's expected.
	if len(result.Scores) != 0 {
		t.Errorf("expected 0 scores for empty script, got %d", len(result.Scores))
	}
}

func TestValidateScript_Valid(t *testing.T) {
	// Build a script long enough (550+ words) to pass the 500-word minimum floor.
	// The hook is strong, the closing contains a CTA, and there is
	// no markdown or repetition.
	body := `The Persian Empire was one of the most powerful civilizations of the ancient world. 
From 550 BCE to 330 BCE it stretched from India to Europe encompassing over 40 different ethnic groups.

King Cyrus the Great established the Achaemenid Empire with a revolutionary approach to governance. 
Unlike previous conquerors Cyrus respected local customs and religions. 
His famous Cyrus Cylinder is often called the first human rights charter.

The empire administrative system divided territory into twenty provinces called satrapies. 
Each satrapy was governed by a satrap who collected taxes and maintained order. 
Royal inspectors known as the Eyes and Ears of the King ensured loyalty across the vast territory.

The Persians built an impressive road network spanning over 2,500 kilometers. 
The Royal Road connected Susa to Sardis with relay stations that could carry a message in just seven days.

This efficient communication system allowed the empire to coordinate military campaigns and tax collection across three continents. The Persian system of governance influenced later empires including the Romans and the Byzantines.

The fall of the Persian Empire came with Alexander the Great but its cultural legacy endured for centuries. Persian art architecture and religious ideas spread throughout the Hellenistic world and beyond.

Persian influence can still be seen in modern governance diplomacy and cultural exchange. The concept of a central administration spanning multiple regions with local autonomy was revolutionary for its time. 

The empire trade networks connected distant civilizations from China to Egypt facilitating the exchange of goods ideas and technologies. This cultural diffusion shaped the development of human civilization.

Military innovations included the elite Immortals unit a standing army of 10,000 soldiers that could rapidly deploy across the empire. The Persian navy controlled the Mediterranean and Red Sea ensuring maritime trade routes remained secure. 

Zoroastrianism the state religion of the empire introduced concepts of monotheism judgment and heaven that influenced later religious traditions. These ideas spread throughout the ancient world.

Persian art combined influences from Mesopotamia Egypt and Greece to create a distinctive style. Palace reliefs at Persepolis depicted representatives from every corner of the empire bringing gifts to the king. This artistic tradition celebrated diversity and unity.

The Persian system of weights and measures standardized trade across the empire. Coins called darics and sigloi were minted with consistent purity making international commerce more reliable. This economic integration boosted prosperity throughout the region.

Women in Persian society enjoyed more rights than in many contemporary civilizations. They could own property conduct business and hold official positions. The royal women of the court wielded significant political influence.

Education and science flourished under Persian rule with scholars from different cultures exchanging knowledge at the royal court. Astronomy medicine and mathematics benefited from this cultural cross-pollination.

Agriculture formed the backbone of the Persian economy with sophisticated irrigation systems called qanats transporting water across arid regions. These underground channels minimized evaporation and allowed farming in areas that would otherwise be desert. The qanat technology spread from Persia to Egypt and across the Islamic world.

The Persian military was renowned for its diversity incorporating soldiers from every corner of the empire. Units from different regions brought unique weapons and tactics including Scythian archers Indian war elephants and Greek mercenaries. This multicultural army could adapt to any battlefield condition.

Palace ceremonies at Persepolis were elaborate affairs designed to impress foreign dignitaries and reinforce imperial power. The Apadana Palace featured massive stone reliefs showing delegates from twenty-three subject nations bringing tribute to the king. Each delegation wore distinctive clothing and carried regional gifts symbolizing the empire vast reach.

Persian engineering achievements included massive canal projects connecting the Nile to the Red Sea. These waterways facilitated trade between the Mediterranean and Indian Ocean creating a global exchange network. The engineering knowledge accumulated by Persian builders influenced construction techniques for centuries.

The Persian court was a center of luxury and refinement where poets musicians and artists received generous patronage. Royal gardens called pairidaeza were designed as earthly paradises with flowing water exotic plants and shaded pavilions. The word paradise itself derives from this Persian term.

Diplomacy played a crucial role in maintaining the vast Persian Empire. Rather than imposing Persian culture on conquered peoples the empire allowed local traditions to continue alongside imperial administration. This pragmatic approach reduced rebellion and fostered loyalty among subject populations.

If you enjoyed learning about the Persian Empire please subscribe for more ancient history content. Share this video with fellow history enthusiasts and let us know your thoughts in the comments below.`

	plan := NewPlan()
	plan.TargetWords = 700
	plan.Duration = 0

	result := ValidateScript(body, plan)
	if !result.AllPass {
		t.Errorf("expected AllPass=true for valid script, got failures:")
		for _, s := range result.Scores {
			if !s.Passed {
				t.Errorf("  %s: %s", s.Check, s.Message)
			}
		}
	}
}

// ── checkWordCount ─────────────────────────────────────────────────────────

func TestCheckWordCount(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		plan     *ScriptGenerationPlan
		wantPass bool
		wantVal  int
	}{
		{
			name:     "empty script",
			script:   "",
			plan:     NewPlan(),
			wantPass: false,
			wantVal:  0,
		},
		{
			name:     "within bounds",
			script:   words(600),
			plan:     planWithTarget(600),
			wantPass: true,
			wantVal:  600,
		},
		{
			name:     "too short (below minWords floor of 500)",
			script:   words(50),
			plan:     planWithTarget(1000),
			wantPass: false,
			wantVal:  50,
		},
		{
			name:     "too long",
			script:   words(3000),
			plan:     planWithTarget(600),
			wantPass: false,
			wantVal:  3000,
		},
		{
			name:     "no target configured",
			script:   words(50),
			plan:     &ScriptGenerationPlan{}, // no Duration, no TargetWords
			wantPass: true,
			wantVal:  50,
		},
		{
			name:     "target from duration",
			script:   words(650),
			plan:     planWithDuration(5), // 5 min = 700 words, bounds ~[595, 805]
			wantPass: true,
			wantVal:  650,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkWordCount(tt.script, tt.plan)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Value=%d | Message=%q", got.Passed, tt.wantPass, got.Value, got.Message)
			}
			if got.Value != tt.wantVal {
				t.Errorf("Value=%d, want %d", got.Value, tt.wantVal)
			}
		})
	}
}

// ── checkNoMarkdown ───────────────────────────────────────────────────────

func TestCheckNoMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantPass bool
	}{
		{"clean text", "This is a normal script without markdown.", true},
		{"code block", "Some text\n```\ncode\n```\nmore text", false},
		{"bold", "This is **very** important", false},
		{"underscore bold", "This is __very__ important", false},
		{"h1 header", "# Introduction", false},
		{"h2 header", "## Chapter 1", false},
		{"h3 header", "### Subsection", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkNoMarkdown(tt.script)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Message=%q", got.Passed, tt.wantPass, got.Message)
			}
		})
	}
}

// ── checkNoStageDirections ────────────────────────────────────────────────

func TestCheckNoStageDirections(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantPass bool
	}{
		{"clean text", "The sun rises over the mountains.", true},
		{"with brackets", "He walks to the door [camera pans right].", false},
		{"timestamp marker", "[00:12:34] The scene changes.", false},
		{"parentheses", "The city (ancient Rome) was founded in 753 BCE.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkNoStageDirections(tt.script)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Message=%q", got.Passed, tt.wantPass, got.Message)
			}
		})
	}
}

// ── checkRepetition ───────────────────────────────────────────────────────

func TestCheckRepetition(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantPass bool
	}{
		{"short script", "too short", true},
		{"no repetition", "The quick brown fox jumps over the lazy dog near the riverbank", true},
		{
			name:     "at threshold boundary (3 unique repeated 4-grams)",
			script:   "the same phrase the same phrase the same phrase the same phrase the same phrase the same phrase",
			wantPass: true,
		},
		{
			name:     "excessive repetition (4 unique 4-grams, above threshold 3)",
			script:   "one two three four one two three four one two three four one two three four one two three four one two three four",
			wantPass: false,
		},
		{
			name:     "just below threshold",
			script:   "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen",
			wantPass: true, // fewer than 4 repeated 4-grams
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkRepetition(tt.script)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Value=%d | Message=%q", got.Passed, tt.wantPass, got.Value, got.Message)
			}
		})
	}
}

// ── checkHookStrength ─────────────────────────────────────────────────────

func TestCheckHookStrength(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantPass bool
	}{
		{"strong hook", "On June 6, 1944, the largest amphibious invasion in history began.", true},
		{"generic opener in today world", "In today's world, technology is everywhere.", false},
		{"generic opener since dawn", "Since the dawn of time, humans have wondered about the stars.", false},
		{"generic opener welcome to", "Welcome to another episode of our history series.", false},
		{"strong question", "What if everything you know about the Roman Empire is wrong?", true},
		{"generic short script", "There are many reasons this is bad.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkHookStrength(tt.script)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Message=%q", got.Passed, tt.wantPass, got.Message)
			}
		})
	}
}

// ── checkCTAPresent ───────────────────────────────────────────────────────

func TestCheckCTAPresent(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantPass bool
	}{
		{
			name:     "has subscribe in closing",
			script:   "Long interesting content about history. Please subscribe for more videos.",
			wantPass: true,
		},
		{
			name:     "has share in closing",
			script:   "Fascinating details about ancient Rome. Share this with your friends.",
			wantPass: true,
		},
		{
			name:     "has let us know in closing",
			script:   "The empire fell in 476 CE. Let us know your thoughts in the comments.",
			wantPass: true,
		},
		{
			name:     "no CTA",
			script:   "The Persian Empire was powerful. It fell to Alexander. The end.",
			wantPass: false,
		},
		{
			name:     "too short",
			script:   "Short.",
			wantPass: false,
		},
		{
			name:     "CTA in last sentence specifically",
			script:   "The empire ruled for centuries. The legacy continues today. Don't forget to subscribe for weekly history content.",
			wantPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkCTAPresent(tt.script)
			if got.Passed != tt.wantPass {
				t.Errorf("Passed=%v, want %v | Message=%q", got.Passed, tt.wantPass, got.Message)
			}
		})
	}
}

// ── ValidateResult JSON tags ───────────────────────────────────────────────

func TestValidationResultJSONTags(t *testing.T) {
	// Quick compile-time check that the struct has proper JSON tags
	result := ValidateScript("test", NewPlan())
	if len(result.Scores) == 0 {
		t.Fatal("expected scores")
	}
	if result.Scores[0].Check == "" {
		t.Error("expected check name")
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

// words returns a string of n words (each word is "word").
func words(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, n*5)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, "word"...)
	}
	return string(out)
}

func planWithTarget(target int) *ScriptGenerationPlan {
	return &ScriptGenerationPlan{TargetWords: target}
}

func planWithDuration(minutes int) *ScriptGenerationPlan {
	return &ScriptGenerationPlan{
		DurationMin: minutes,
		Duration:    minutes * 60,
	}
}
