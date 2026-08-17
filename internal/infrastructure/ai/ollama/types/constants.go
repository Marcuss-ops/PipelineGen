package types

const (
	// Speech constants for duration estimation
	WordsPerMinute = 140

	// LLM Filtering
	MarkerNarrator = "🎙️ Narrative Script"
	MarkerTimeline = "⏱️ Timeline"

	// Technical limits and defaults
	DefaultTimeoutSeconds  = 600 // 10 minutes — Ollama generation can be slow for long scripts
	CircuitBreakerFailures = 3
	CircuitBreakerTimeout  = 15
	MaxRetries             = 3
	StreamBufferSize       = 100
	DefaultTemperature     = 0.35
	DefaultNumPredict      = 16384
	// DefaultNumCtx is the Ollama context window sent for script generation.
	// Research-sourced prompts embed the full resolved source text twice
	// (editorial "Source text:" block + the template's "REFERENCE INPUT"
	// block), which can reach ~5k tokens — past Ollama's 4096 default. At
	// 4096 the prompt eats the whole window and the model returns a single
	// token (done_reason=length), failing the min_words gate. 16384 fits the
	// worst research prompt with room for a full narration and is cheap for
	// the quantized gemma4 models in use.
	DefaultNumCtx         = 16384
	DefaultTopP           = 0.9
	SuggestionTemperature = 0.2
	SuggestionNumPredict  = 128
)

// List of words/phrases to filter out from LLM output across different languages
var StopPhrases = []string{
	"okay, here",
	"word count",
	"notes:",
	"introduzione:",
	"conclusione:",
	"scena ",
	"capitolo ",
	"paragrafo ",
	"ecco lo script",
	"ecco il tuo",
	"here is the",
	"certainly!",
	"sure,",
}

// List of speaker labels to remove from start of lines
var SpeakerLabels = []string{
	"narratore",
	"narrator",
	"voce",
	"voice",
	"speaker",
	"host",
	"intervistatore",
	"personaggio",
	"io",
	"me",
}

// List of meta-content types to remove between brackets
var MetaContentTypes = []string{
	"musica", "immagini", "scena", "inquadratura", "audio", "video",
	"clip", "montaggio", "sottofondo", "background", "visual",
	"transition", "transizione", "voce", "voice", "sound", "fx",
	"inizio", "fine", "end", "start", "music", "shot",
}
