package scripts

// ── NarrativeStrategy ───────────────────────────────────────────────────
//
// NarrativeStrategy is a Type-specific preset that controls every phase of
// the clip-to-script pipeline: narrative planning, source text construction,
// output validation, and normalisation.
//
// The strategy is resolved once from a central registry and used by
// PlanNarrative, BuildSourceText, and — in future PRs — by the validator
// and normaliser. This ensures Type is a structural property, not just a
// textual hint buried at the bottom of a prompt.
//
// To add a new video Type:
//   1. Define the strategy in init()
//   2. Add it to allStrategies
//   3. Update the Type validation in the API handler (if needed)
// =========================================================================

// NarrativeStrategy prescribes the narrative roles, system prompt, output
// contract and structural rules for a specific video Type.
type NarrativeStrategy struct {
	// Type is the canonical name (e.g. "compilation", "story").
	Type string

	// TaskIdentity is the opening sentence of the LLM prompt, e.g.
	// "You are writing a structured compilation script based on real video clips."
	TaskIdentity string

	// PlannerSystem is the system prompt for PlanNarrative's LLM call.
	PlannerSystem string

	// PlannerRoles are the narrative roles the planner may assign.
	// The planner's output will use these roles instead of documentary defaults.
	PlannerRoles []string

	// RolesHelp is a one-line description shown in the prompt.
	RolesHelp string

	// OutputFormat tells the writer how to structure each scene block.
	OutputFormat string

	// AllowNarrationScenes permits narration-only blocks without a clip.
	AllowNarrationScenes bool
}

// allStrategies is the central registry, keyed by Type name.
var allStrategies map[string]NarrativeStrategy

func init() {
	allStrategies = make(map[string]NarrativeStrategy, 4)

	allStrategies["compilation"] = NarrativeStrategy{
		Type: "compilation",
		TaskIdentity: "You are writing a fast-paced compilation script based on real video clips. " +
			"This is NOT a documentary — it is a tightly-paced show where each clip is introduced with energy, " +
			"plays, and then gets a quick punchline before moving on.",
		PlannerSystem: `You are planning a COMEDY COMPILATION video. Your expertise is:
1. Identifying clips with the strongest comedic potential
2. Ordering them for maximum laughs — start strong, escalate, end with a callback
3. Assigning narrative roles that build a comedic arc
4. Ensuring variety — avoid two clips with the same type of humour back-to-back

You ALWAYS respond with valid JSON only — no markdown, no extra text.`,
		PlannerRoles: []string{"hook", "setup", "escalation", "visual example", "punchline", "callback", "closing payoff"},
		RolesHelp:    "hook, setup, escalation, visual example, punchline, callback, closing payoff",
		OutputFormat: `For EACH clip write EXACTLY this structure:

1. INTRO: A funny setup that builds anticipation for what is ABOUT to happen.
   - Tease the situation with a witty observation
   - Describe what the audience is about to see
   - Add a surprising detail or twist

2. [The audience watches the clip — no narration during the clip]

3. TRANSITION: A quick comedic remark linking to the next clip

OVERALL STRUCTURE:
- INTRO PARAGRAPH: Energetic hook that sets up the compilation theme
- For each clip: INTRO → [clip plays] → TRANSITION
- OUTRO PARAGRAPH: Callback to the opening hook, memorable ending

RULES:
- Write in present tense, as if the host is talking to the audience live
- Use voice-over host style, energetic and engaging`,
		AllowNarrationScenes: false,
	}

	allStrategies["story"] = NarrativeStrategy{
		Type: "story",
		TaskIdentity: "You are writing a STORY-DRIVEN NARRATIVE based on real video clips. " +
			"This is a three-act story with a clear narrative arc: setup, conflict, resolution.",
		PlannerSystem: `You are planning a STORY-DRIVEN NARRATIVE video. Your expertise is:
1. Identifying clips that advance a central narrative
2. Ordering them in a three-act structure (hook → conflict → resolution)
3. Assigning roles that build dramatic tension
4. Ensuring every scene contributes to the story arc

You ALWAYS respond with valid JSON only — no markdown, no extra text.`,
		PlannerRoles: []string{"hook", "inciting incident", "escalation", "setback", "climax", "resolution"},
		RolesHelp:    "hook, inciting incident, escalation, setback, climax, resolution",
		OutputFormat: `ACT 1 (HOOK): Introduce the central question or conflict
ACT 2 (RISING ACTION): Build tension through examples and stakes
ACT 3 (RESOLUTION): Answer the question, resolve the conflict

RULES:
- Every scene must advance the story
- Create suspense at scene transitions
- End each act with a mini-cliffhanger
- Let the story reveal the theme rather than stating it explicitly`,
		AllowNarrationScenes: true,
	}

	allStrategies["interview"] = NarrativeStrategy{
		Type: "interview",
		TaskIdentity: "You are writing an INTERVIEW-DRIVEN script based on real video clips. " +
			"The clips contain interview material — your job is to weave them into a flowing conversational narrative.",
		PlannerSystem: `You are planning an INTERVIEW-BASED video. Your expertise is:
1. Identifying the most revealing moments from each interview clip
2. Ordering clips to create a conversational flow
3. Assigning roles that build from question to insight
4. Connecting clips through thematic threads

You ALWAYS respond with valid JSON only — no markdown, no extra text.`,
		PlannerRoles: []string{"opening question", "context", "answer", "follow-up", "contradiction", "key revelation", "conclusion"},
		RolesHelp:    "opening question, context, answer, follow-up, contradiction, key revelation, conclusion",
		OutputFormat: `For each clip:
1. CONTEXT: Set up who is speaking and what topic they are discussing
2. HIGHLIGHT: Describe the most interesting moment from the clip
3. INSIGHT: Add a brief observation about what this reveals

RULES:
- Create a conversational flow between clips
- Use quotes from the clips where available
- End with a reflective conclusion that ties the interviews together`,
		AllowNarrationScenes: true,
	}

	// "" and "documentary" both resolve to documentary
	allStrategies["documentary"] = NarrativeStrategy{
		Type: "documentary",
		TaskIdentity: "You are writing a DOCUMENTARY NARRATIVE based on real video clips. " +
			"Your script must be authoritative, informative, and flowing.",
		PlannerSystem: `You are a professional documentary editor planning a video narrative. Your expertise is:
1. Analysing video clips and their content
2. Finding the best narrative order for a compelling story
3. Assigning narrative roles to each clip
4. Ensuring the flow makes logical and emotional sense

You ALWAYS respond with valid JSON only — no markdown, no extra text.`,
		PlannerRoles: []string{"opening", "context", "explanation", "evidence", "transition", "conclusion"},
		RolesHelp:    "opening, context, explanation, evidence, transition, conclusion",
		OutputFormat: `1. Rewrite the clip descriptions in your own words — do NOT copy-paste them verbatim.
2. Create a flowing, cinematic narration that connects clips smoothly.
3. Use voice-over narrative style, as if reading a documentary script.
4. Ground every statement in the evidence above. Do NOT invent facts.
5. Begin with a strong INTRO paragraph that hooks the viewer and sets up the theme.
6. End with a compelling OUTRO paragraph that wraps up and leaves a lasting impression.`,
		AllowNarrationScenes: true,
	}
}

// ResolveStrategy returns the NarrativeStrategy for the given Type.
// Empty string and unrecognised values default to "documentary".
func ResolveStrategy(videoType string) NarrativeStrategy {
	if s, ok := allStrategies[videoType]; ok {
		return s
	}
	// Default to documentary
	return allStrategies["documentary"]
}

// ValidTypes returns all registered Type names (for validation and API docs).
func ValidTypes() []string {
	types := make([]string, 0, len(allStrategies))
	for t := range allStrategies {
		if t != "" {
			types = append(types, t)
		}
	}
	return types
}

// ── Version constant (bump when prompts change) ─────────────────────────

// NarrativeStrategyVersion is embedded in fingerprints so that prompt
// changes invalidate the memory cache automatically.
const NarrativeStrategyVersion = "v1"
