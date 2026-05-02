package types

type TextGenerationRequest struct {
	Language   string
	Duration   int
	Tone       string
	Model      string
	Prompt     string
	SourceText string
	Title      string
	Options    map[string]interface{}
}

type RegenerationRequest struct {
	Language       string
	Model          string
	OriginalScript string
	Title          string
	Tone           string
	Options        map[string]interface{}
}

type GenerationResult struct {
	Script      string
	WordCount   int
	EstDuration int
	Model       string
	Prompt      string
}
