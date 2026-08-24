package types

// DefaultLanguage is the default language for script generation
const DefaultLanguage = "it"

// DefaultTemplate is the default template/style for script generation
const DefaultTemplate = "documentary"

// DefaultDuration is the default duration in seconds for script generation.
// PipelineGen targets 10-minute YouTube-style scripts by default
// (600s = ~1400 words at 140 wpm), so the single-script endpoint
// can be called without specifying `duration` and still produce a
// realistically sized output. The batch endpoint already uses 600.
const DefaultDuration = 600

// DefaultTone is the default tone for script generation (used in LLM prompts)
const DefaultTone = "documentary"

// ApplyDefaults applies default values to TextGenerationRequest
func ApplyDefaults(req *TextGenerationRequest) {
	if req.Language == "" {
		req.Language = DefaultLanguage
	}
	if req.Duration == 0 {
		req.Duration = DefaultDuration
	}
	if req.Tone == "" {
		req.Tone = DefaultTone
	}
}

// ApplyDefaultsToRegeneration applies default values to RegenerationRequest
func ApplyDefaultsToRegeneration(req *RegenerationRequest) {
	if req.Language == "" {
		req.Language = DefaultLanguage
	}
	if req.Tone == "" {
		req.Tone = DefaultTone
	}
}
