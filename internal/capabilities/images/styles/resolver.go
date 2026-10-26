package styles

// SourceBackend abstracts style lookup independently of YAML/DB storage.
type SourceBackend interface {
	GetStyle(styleID string) (StyleSnapshot, error)
}

type StyleSnapshot struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
	DestinationKey string
	Enabled        bool
}

type StyleResolver interface {
	Resolve(styleID, provider, model string) (ResolvedStyle, error)
	Validate(styleID, provider, model string) error
}

const DefaultStyleID = "default"

type failingResolver struct{ err error }
