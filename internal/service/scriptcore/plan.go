package scriptcore

// ScriptGenerationPlan is the unified plan that every generation endpoint
// (GenerateText, GenerateBatch, GenerateFromClips, agent_script_writer)
// populates before calling the Engine.  This replaces the ad-hoc per-endpoint
// request structs with a single contract so that:
//
//   - All endpoints pass through the same validation and enrichment logic.
//   - Post-generation validators (word count, language, repetition, hook, CTA)
//     check against the same plan regardless of which endpoint produced the script.
//   - The memory gate, source resolver, and research pipeline receive
//     consistent inputs.
//
// Zero values are acceptable — each field documents its default.
type ScriptGenerationPlan struct {
	// ── Identity ──────────────────────────────────────────────────────
	Title     string // document / video title
	Topic     string // search topic or subject
	Language  string // ISO 639-1 code (default: "it" for GenerateFromClips text-only, "en" elsewhere)
	Tone      string // "documentary", "educational", "commentary", etc.
	Model     string // Ollama model name
	ChannelID string // memory gate channel
	Mode      string // "text", "batch", "clip_to_script", "book"

	// ── Sizing ────────────────────────────────────────────────────────
	Duration    int     // target duration in seconds
	DurationMin int     // target duration in minutes (preferred)
	TargetWords int     // explicit word-count target (0 = derived from Duration)
	NumPredict  int     // LLM num_predict override (0 = use server default)
	Temperature float64 // LLM temperature override (0 = use server default)

	// ── Input sources ─────────────────────────────────────────────────
	SourceText    string        // inline source text or YouTube URL
	Prompt        string        // base prompt (may be enriched by memory gate)
	WebContext    string        // pre-researched web context (SearXNG results)
	Guidelines    string        // writing guidelines / constraints
	OutlineTopic  string        // topic for outline generation (batch)
	AgentResearch *ResearchPack // structured research from agent_script_writer.py

	// ── Memory gate ───────────────────────────────────────────────────
	UseMemory    bool // enable memory gate (default: true)
	ForceRefresh bool // bypass memory cache

	// ── Post-generation options ───────────────────────────────────────
	SaveToDB            bool // persist to scripts table
	CreateDoc           bool // create Google Doc
	ExtractEntities     bool // run entity + insight extraction
	GenerateMetadata    bool // generate YouTube metadata (title/desc/tags)
	PromptVersion       string
	EditorPromptVersion string
	QAPromptVersion     string

	// ── Clip-aware generation (GenerateFromClips) ───────────────────────
	VisualStyle    string // "cinematic", "whiteboard", "slides"
	ImagesPerScene int    // image variants per scene (default 2, max 5)
	SceneCount     int    // max scenes (default 10, max 30)
	Width          int    // image width
	Height         int    // image height
	ImageModel     string // image generation model
	RecommendClips bool   // attach YouTube clip recommendations

	// ── Translation ───────────────────────────────────────────────────
	Languages []string // additional target languages for translation
}

// ResearchPack holds the structured output from agent_script_writer.py.
// Instead of dumping free-form text into SourceText, the agent produces
// a JSON object that the Go handler can parse and pass to the plan.
type ResearchPack struct {
	Topic           string           `json:"topic"`
	KeyFacts        []string         `json:"key_facts"`
	Timeline        []TimelineEntry  `json:"timeline,omitempty"`
	Controversies   []string         `json:"controversies,omitempty"`
	ImportantQuotes []string         `json:"important_quotes,omitempty"`
	Sources         []ResearchSource `json:"sources,omitempty"`
	SuggestedAngles []string         `json:"suggested_angles,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`

	// Fallback: when the agent cannot produce structured output
	// (old version, error during parsing), this holds the raw text.
	RawText string `json:"raw_text,omitempty"`
}

// TimelineEntry is a single item in the research timeline.
type TimelineEntry struct {
	Date   string `json:"date,omitempty"`
	Event  string `json:"event"`
	Source string `json:"source,omitempty"`
}

// NewPlan returns a ScriptGenerationPlan with sensible defaults.
// Callers override fields directly for endpoint-specific values.
func NewPlan() *ScriptGenerationPlan {
	return &ScriptGenerationPlan{
		Language:  "en",
		Tone:      "documentary",
		Duration:  600, // 10 minutes default
		UseMemory: true,
	}
}
