package generation

import "encoding/json"

// EnvelopeVersionV2 is the canonical version tag for the unified
// generation request envelope.
const EnvelopeVersionV2 = 2

// SourceKind identifies the upstream source used to resolve a
// generation request into a normalized plan.
type SourceKind string

const (
	SourceKindText    SourceKind = "text"
	SourceKindClips   SourceKind = "clips"
	SourceKindCatalog SourceKind = "catalog"
	SourceKindSearch  SourceKind = "search"
	SourceKindBatch   SourceKind = "batch"
)

// EnvelopeItem is a single item in a batch-style request.
type EnvelopeItem struct {
	Title      string          `json:"title,omitempty"`
	SourceText string          `json:"source_text,omitempty"`
	ClipIDs    []string        `json:"clip_ids,omitempty"`
	Source     SourceKind      `json:"source,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Options    map[string]any  `json:"options,omitempty"`
}

// GenerationEnvelopeV2 is the unified external command shape.
type GenerationEnvelopeV2 struct {
	Version       int            `json:"version"`
	Type          Type           `json:"type"`
	Source        SourceKind     `json:"source"`
	Title         string         `json:"title,omitempty"`
	Language      string         `json:"language,omitempty"`
	Tone          string         `json:"tone,omitempty"`
	Style         string         `json:"style,omitempty"`
	Model         string         `json:"model,omitempty"`
	SourceText    string         `json:"source_text,omitempty"`
	ClipIDs       []string       `json:"clip_ids,omitempty"`
	NumClips      int            `json:"num_clips,omitempty"`
	DriveFolderID string         `json:"drive_folder_id,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
	Items         []EnvelopeItem `json:"items,omitempty"`
	PostProcess   []string       `json:"post_process,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// GenerationResult is the canonical post-processing output shape.
// It is intentionally generic so the same result type can carry
// scripts, docs, images, or a batch aggregation.
type GenerationResult struct {
	OK        bool           `json:"ok"`
	Type      Type           `json:"type,omitempty"`
	Title     string         `json:"title,omitempty"`
	Source    SourceKind     `json:"source,omitempty"`
	Script    any            `json:"script,omitempty"`
	RawScript string         `json:"raw_script,omitempty"`
	Result    any            `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ResolvedGenerationPlan is the normalized, internal-only plan used
// by engine and post-processors.
type ResolvedGenerationPlan struct {
	Type          Type
	Source        SourceKind
	Title         string
	Language      string
	Tone          string
	Style         string
	Model         string
	SourceText    string
	ClipIDs       []string
	NumClips      int
	DriveFolderID string
	Options       map[string]any
	Items         []EnvelopeItem
	PostProcess   []string
	Metadata      map[string]any
	Fingerprint   string
	CacheKey      string
}
