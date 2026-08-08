package images

import "context"

// SemanticPort is the application-owned boundary for optional semantic
// metadata enrichment. Infrastructure adapters translate their native DTOs
// to these stable application types at the composition root.
type SemanticPort interface {
	GeneratePayload(context.Context, SemanticWriteRequest) (*SemanticPayload, string, error)
	Write(context.Context, SemanticWriteRequest) (*SemanticWriteResult, error)
}

// SemanticWriteRequest carries the metadata inputs needed by image enrichment.
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

// SemanticWriteResult is the result of a semantic metadata write.
type SemanticWriteResult struct {
	LocalPath string
	Payload   *SemanticPayload
}

// SemanticPayload is the provider-neutral semantic metadata shape consumed
// by image metadata persistence.
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

func buildImageSemanticExtension(width, height int) []map[string]any {
	return []map[string]any{{
		"type": "image", "width": width, "height": height,
		"format": "", "dominant_color": "", "file_size": 0,
	}}
}
