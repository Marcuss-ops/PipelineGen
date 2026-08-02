package types

import "encoding/json"

// OutputMode declares the contractual shape the model must emit for
// a given request. Empty value means the caller does not require a
// structured contract (legacy prose behaviour — deprecated for the
// canonical script pipeline).
//
// LLM-PLAIN-TEXT-CONTRACT wave (PR-2, July 2026): the canonical
// script pipeline now requests OutputModePlainText. The engine
// (internal/application/scripts/usecase/engine_generate.go) sets
// OutputMode = OutputModePlainText unconditionally; the LLM emits
// raw narrative prose per the plainTextInstruction suffix (see
// engine_prompt.go), and the downstream SceneSynthesizer + scene
// binder (internal/application/scripts/scene/) + postprocessor
// pipeline own all structured fields. OutputModeScriptV1 is
// RETAINED for backward-compat with cached rows and pre-wave
// callers; the decode path uses jsonextract.Scanner (mode
// depends on the schema of the stored rows, NOT on this flag).
type OutputMode string

const (
	// OutputModePlainText is the canonical default for the script
	// pipeline post-LLM-PLAIN-TEXT-CONTRACT wave. The model is
	// NOT asked to emit JSON, a schema, scene IDs, scene indexes,
	// or kind labels — the downstream pipeline derives ALL of
	// those from the raw prose (see engine_prompt.go::plainTextInstruction).
	//
	// Wire effect: the Format json.RawMessage field on the
	// TextGenerationRequest is left empty for this mode (no Ollama
	// native JSON-mode constraint). The native Ollama Format field
	// stays available for test rigs and future non-script callers.
	OutputModePlainText OutputMode = "plain_text"

	// OutputModeScriptV1 demands the canonical ModelScriptOutputV1
	// shape ("internal/domain/script/model_output.go") — RETAINED
	// for backward-compat with pre-wave cached rows:
	//
	//   {
	//     "schema_version": 1,
	//     "text": "<complete script>",
	//     "specscene": { "version": 1, "scenes": [...] }
	//   }
	//
	// LLM-PLAIN-TEXT-CONTRACT wave: no new caller should request
	// this mode. The const remains so the decode path can still
	// promote legacy cached rows (a pre-wave cache row stored as
	// JSON is unambiguous, regardless of the current request mode).
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
	// DisableWebSearch prevents implicit SearXNG augmentation for isolated
	// generation contracts such as stock_only and clip_only.
	DisableWebSearch bool

	// GroundingPolicy controls how source_text and clip evidence
	// interact in the model prompt. Values: clips_primary,
	// source_primary, balanced. Empty means no special grounding
	// instructions are appended.
	GroundingPolicy string

	// OutputMode declares the contractual shape the caller requires.
	// See OutputModePlainText (canonical) and OutputModeScriptV1
	// (backward-compat). When empty, the wire request behaves as
	// legacy prose (no JSON-mode constraint set).
	OutputMode OutputMode

	// Format is the Ollama native output-format constraint
	// (P0.2, June 2026). When set, Ollama forces the model response
	// to be syntactically valid JSON matching the supplied shape
	// (a string such as `"json"` for unconstrained JSON, or a
	// JSON-Schema object). This is a TOP-LEVEL body field on
	// Ollama's `/api/chat` endpoint — it is NOT inside `options`.
	//
	// LLM-PLAIN-TEXT-CONTRACT wave (PR-2): Format is set to
	// `"json"` ONLY when OutputMode == OutputModeScriptV1 (the
	// legacy path). For OutputModePlainText (canonical), Format
	// is left empty — Ollama's native generation stays in prose mode.
	// Native json-mode does NOT enforce a schema — the prompt
	// suffix (engine_prompt.go::plainTextInstruction) does that.
	Format json.RawMessage `json:"format,omitempty"`

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
