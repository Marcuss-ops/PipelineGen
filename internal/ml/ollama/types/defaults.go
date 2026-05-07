package types

// DefaultLanguage is the default language for script generation
const DefaultLanguage = "it"

// DefaultTemplate is the default template/style for script generation
const DefaultTemplate = "documentary"

// DefaultDuration is the default duration in seconds for script generation
const DefaultDuration = 60

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
