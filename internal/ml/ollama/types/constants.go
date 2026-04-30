package types

const (
	// DefaultModel is the fallback model if none is specified
	DefaultModel = "gemma3:4b"

	// Speech constants for duration estimation
	WordsPerMinute = 140
	SecondsPerWord = 3 // Rough estimate for non-speech tasks or very slow speech

	// LLM Filtering
	MarkerNarrator = "🎙️ Narrative Script"
	MarkerTimeline = "⏱️ Timeline"

	// Technical limits and defaults
	DefaultTimeoutSeconds   = 120
	CircuitBreakerFailures  = 3
	CircuitBreakerTimeout   = 30
	MaxRetries              = 3
	StreamBufferSize        = 100
	MaxArtlistTags          = 5
	DefaultDesiredSegments  = 4
	DefaultEntityCount      = 2
	SuggestionTemperature   = 0.2
	SuggestionNumPredict    = 128
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
