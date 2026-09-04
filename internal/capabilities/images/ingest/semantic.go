package ingest

import "context"

// SemanticPort is the provider-neutral boundary for optional semantic image metadata enrichment.
type SemanticPort interface {
	GeneratePayload(context.Context, SemanticWriteRequest) (*SemanticPayload, string, error)
	Write(context.Context, SemanticWriteRequest) (*SemanticWriteResult, error)
}

type SemanticWriteRequest struct {
	AssetID    string
	AssetType  string
	MediaType  string
	Source     string
	SourceType string
	Generator  string
	Retriever  string
	PageURL    string
	ImageURL   string
	License    string
	Author     string
	Style      string
	Prompt     string
	LocalPath  string
	TempDir    string
	Extensions []map[string]any
	GroupID    string
	Assets     []map[string]any
}

type SemanticWriteResult struct {
	LocalPath string
	Payload   *SemanticPayload
}

type SemanticPayload struct {
	AssetID             string
	PromptOriginal      string
	Style               []string
	Tags                []string
	Subjects            []string
	SearchText          string
	AssetType           string
	SemanticDescription string
	ConceptTags         []string
	Mood                []string
	Categories          []string
	VisualObjects       []string
	EmotionalTone       []string
	RetrievalScore      *float64
}

func ImageSemanticExtension(width, height int) []map[string]any {
	return []map[string]any{{
		"type": "image", "width": width, "height": height,
		"format": "", "dominant_color": "", "file_size": 0,
	}}
}
