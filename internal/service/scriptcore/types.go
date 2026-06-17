package scriptcore

// GenerateRequest holds the inputs for a single script generation call.
// This is the shared contract used by all 3 endpoints (single, batch, source).
type GenerateRequest struct {
	Language        string
	Duration        int // seconds (legacy, prefer DurationMinutes)
	DurationMinutes int // explicit duration in minutes — clearer than Duration
	MinWords        int
	Tone            string
	Model           string
	Title           string
	Prompt          string // the final prompt (may be memory-enriched)
	SourceText      string
	WebContext      string
	ChannelID       string
	Mode            string // "text", "book", "clip_to_script"

	// Memory gate options
	UseMemory    bool
	ForceRefresh bool

	// LLM options
	NumPredict  int
	Temperature float64
}

// GenerateResult holds the output of a script generation call.
type GenerateResult struct {
	Script      string
	WordCount   int
	EstDuration int
	Model       string
	Prompt      string // the raw prompt used for debug
}

// MemoryGateContext holds the resolved memory gate state.
type MemoryGateContext struct {
	EnrichedPrompt string
	CacheHit       bool
	ExactOutput    any // *gemmamemory.GenerationOutput when hit
	SourceGenID    string
}
