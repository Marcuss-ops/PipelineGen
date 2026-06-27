package types

type TextGenerationRequest struct {
	Language        string
	Duration        int // Duration in seconds (kept for backward compat)
	DurationMinutes int // Preferred: explicit duration in minutes
	MinWords        int    // Optional: override the duration-derived word count target
	MaxChars        int    // Optional: hard character limit per response; 0 = unlimited
	Tone            string
	Model           string
	Prompt          string
	SourceText      string
	Title           string
	ClipIDs         []string // For structured output: the expected clip_id values
	Options         map[string]any
	WebContext      string // Optional: pre-fetched web search results injected into the prompt

	// Diversity knobs (all optional). When zero, GenerateScript fills in safe
	// defaults — including a per-call random seed so two runs on the same
	// prompt do not produce byte-identical scripts.
	Temperature float64
	TopP        float64
	Seed        int  // 0 = randomize per call
	NoSeed      bool // when true, do NOT send a seed to Ollama (model handles its own sampling)
}

type RegenerationRequest struct {
	Language       string
	Model          string
	OriginalScript string
	Title          string
	Tone           string
	Options        map[string]any
}

type GenerationResult struct {
	Script      string
	WordCount   int
	EstDuration int
	Model       string
	Prompt      string
}
