package types

// OutputMode declares the contractual shape the model must emit for
// a given request. Empty value means the caller does not require a
// structured contract (legacy prose behaviour — PR 1 deprecates this
// for the canonical script pipeline).
//
// Engine.Generate (internal/application/scripts/engine.go) sets
// OutputMode = OutputModeScriptV1 unconditionally for script
// generation in PR 1. The decode path uses DecodeModelOutput, with
// the legacy array decoder as the cache-fallback path.
type OutputMode string

const (
	// OutputModeScriptV1 demands the canonical ModelScriptOutputV1
	// shape ("internal/domain/script/model_output.go"):

	//   {
	//     "schema_version": 1,
	//     "text": "<complete script>",
	//     "specscene": { "version": 1, "scenes": [...] }
	//   }

	// Calls without this value continue to receive legacy prose and
	// will be rejected by the engine's payload decoder.
	OutputModeScriptV1 OutputMode = "script_v1"
)

type TextGenerationRequest struct {
	Language        string
	Duration        int // Duration in seconds (kept for backward compat)
	DurationMinutes int // Preferred: explicit duration in minutes
	MinWords        int // Optional: override the duration-derived word count target
	MaxChars        int // Optional: hard character limit per response; 0 = unlimited
	Tone            string
	Model           string
	Prompt          string
	SourceText      string
	Title           string
	ClipIDs         []string // For structured output: the expected clip_id values
	Options         map[string]any
	WebContext      string // Optional: pre-fetched web search results injected into the prompt

	// OutputMode declares the contractual shape the caller requires.
	// See OutputModeScriptV1. When empty, the engine treats the
	// request as legacy prose (PR 1: deprecated path).
	OutputMode OutputMode

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
