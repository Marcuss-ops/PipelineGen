package stockpipeline

import (
	"fmt"
)

// SearchQuery is one term in a StockSearchAndRunRequest. The API layer
// binds JSON bodies directly onto a slice of these via the
// stockpipeline-package request type below — no api-local mirror is
// needed, so the application boundary stays one-directional.
type SearchQuery struct {
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

// StockSearchAndRunRequest is the canonical request shape for the
// search-and-run endpoint. The api package re-exports its handlers'
// request DTOs from here (handler binds JSON onto this type).
type StockSearchAndRunRequest struct {
	Queries       []SearchQuery       `json:"queries"`
	TotalMinutes  int                 `json:"total_minutes"`
	ChunkDuration int                 `json:"chunk_duration,omitempty"`
	ClipDuration  int                 `json:"clip_duration,omitempty"`
	NoAudio       bool                `json:"no_audio,omitempty"`
	NoEffects     bool                `json:"no_effects,omitempty"`
	NoTransitions bool                `json:"no_transitions,omitempty"`
	MaxVideos     int                 `json:"max_videos,omitempty"`
	Subfolder     string              `json:"subfolder"`
	FolderName    string              `json:"folder_name"`
	FolderID      string              `json:"folder_id,omitempty"`
	Metadata      *ChunkMetadataInput `json:"metadata,omitempty"`
}

// StockCommand is the canonical internal command the API layer hands to
// StockUseCase.Submit. It is independent of the API request types and the
// job-payload shape used by HandleJob — the use case and the worker each
// derive their own serialisation.
type StockCommand struct {
	SearchQueries []string
	DirectURLs    []string
	TotalMinutes  int
	MaxVideos     int
	ChunkDuration int
	ClipDuration  int
	NoAudio       bool
	NoEffects     bool
	NoTransitions bool
	Subfolder     string
	FolderName    string
	FolderID      string
	Metadata      *ChunkMetadataInput
	// Progress, when non-nil, is invoked by the runner with percent +
	// message at each pipeline stage boundary. Mirrors RunInput.Progress
	// (post-S2a unification — see docs/operations/06 note).
	Progress func(percent int, message string)
}

// FromRunPayload converts the run-pipeline request body (POST /run
// binds JSON directly to *StockRunPayload) into a StockCommand.
func FromRunPayload(p *StockRunPayload) (*StockCommand, error) {
	if p == nil {
		return nil, fmt.Errorf("stockpipeline: FromRunPayload: nil *StockRunPayload")
	}
	metadata := chunkMetadataFromRunPayload(p.Metadata)
	return &StockCommand{
		SearchQueries: append([]string(nil), p.SearchQueries...),
		DirectURLs:    append([]string(nil), p.DirectURLs...),
		TotalMinutes:  p.TotalMinutes,
		ChunkDuration: p.ChunkDuration,
		ClipDuration:  p.ClipDuration,
		NoAudio:       p.NoAudio,
		NoEffects:     p.NoEffects,
		NoTransitions: p.NoTransitions,
		MaxVideos:     p.MaxVideos,
		Subfolder:     p.Subfolder,
		FolderName:    p.FolderName,
		FolderID:      p.FolderID,
		Metadata:      metadata,
	}, nil
}

// FromSearchAndRunRequest converts the search-and-run request body
// (POST /search-and-run binds JSON directly to *StockSearchAndRunRequest)
// into a StockCommand.
//
// HTTP-specific validation (TotalMinutes default, ClipDuration range,
// empty-query check) runs at the api handler; this converter assumes
// its inputs are already validated. Use stock.FromAPIRequest (in
// internal/application/assets/providers/stock/usecase.go) to apply
// validation automatically.
func FromSearchAndRunRequest(r *StockSearchAndRunRequest) (*StockCommand, error) {
	if r == nil {
		return nil, fmt.Errorf("stockpipeline: FromSearchAndRunRequest: nil *StockSearchAndRunRequest")
	}
	queries := make([]string, 0, len(r.Queries))
	for _, q := range r.Queries {
		queries = append(queries, q.Q)
	}
	var metadata *ChunkMetadataInput
	if r.Metadata != nil {
		metadata = &ChunkMetadataInput{
			Title:       r.Metadata.Title,
			Description: r.Metadata.Description,
			Tags:        r.Metadata.Tags,
			Category:    r.Metadata.Category,
			Author:      r.Metadata.Author,
			Extra:       r.Metadata.Extra,
		}
	}
	return &StockCommand{
		SearchQueries: queries,
		TotalMinutes:  r.TotalMinutes,
		ChunkDuration: r.ChunkDuration,
		ClipDuration:  r.ClipDuration,
		NoAudio:       r.NoAudio,
		NoEffects:     r.NoEffects,
		NoTransitions: r.NoTransitions,
		MaxVideos:     r.MaxVideos,
		Subfolder:     r.Subfolder,
		FolderName:    r.FolderName,
		FolderID:      r.FolderID,
		Metadata:      metadata,
	}, nil
}

func chunkMetadataFromRunPayload(m *StockRunPayloadMetadata) *ChunkMetadataInput {
	if m == nil {
		return nil
	}
	return &ChunkMetadataInput{
		Title:       m.Title,
		Description: m.Description,
		Tags:        m.Tags,
		Category:    m.Category,
		Author:      m.Author,
		Extra:       m.Extra,
	}
}

// ToRunInput projects a StockCommand onto the runner's internal input
// shape. Single-step metadata conversion (StockCommand.Metadata and
// RunInput.Metadata share the underlying ChunkMetadataInput type), plus
// the Progress callback mapping.
func (c *StockCommand) ToRunInput() *RunInput {
	if c == nil {
		return nil
	}
	return &RunInput{
		SearchQueries: append([]string(nil), c.SearchQueries...),
		DirectURLs:    append([]string(nil), c.DirectURLs...),
		TotalMinutes:  c.TotalMinutes,
		ChunkDuration: c.ChunkDuration,
		ClipDuration:  c.ClipDuration,
		NoAudio:       c.NoAudio,
		NoEffects:     c.NoEffects,
		NoTransitions: c.NoTransitions,
		MaxVideos:     c.MaxVideos,
		Subfolder:     c.Subfolder,
		FolderName:    c.FolderName,
		FolderID:      c.FolderID,
		Metadata:      c.Metadata,
		Progress:      c.Progress,
	}
}

// ToJobPayload converts a StockCommand to the map[string]any shape the
// jobs system expects on the wire. This is the canonical replacement
// for the legacy stockPayloadToMap helper which round-tripped through
// json.Marshal/Unmarshal and silently dropped Marshal errors.
//
// Manual field-by-field assignment is intentional: it preserves the
// `omitempty`-equivalent semantics the previous Marshal produced, and
// it lets any future struct tag change on StockRunPayload diverge from
// the jobs wire-shape (per AGENTS.md Pattern 0 — wire-shape concerns
// stay separate from domain DTO concerns).
func (c *StockCommand) ToJobPayload() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	payload := make(map[string]any, 14)
	if len(c.SearchQueries) > 0 {
		payload["search_queries"] = c.SearchQueries
	}
	if len(c.DirectURLs) > 0 {
		payload["direct_urls"] = c.DirectURLs
	}
	payload["total_minutes"] = c.TotalMinutes
	if c.ChunkDuration != 0 {
		payload["chunk_duration"] = c.ChunkDuration
	}
	if c.ClipDuration != 0 {
		payload["clip_duration"] = c.ClipDuration
	}
	if c.NoAudio {
		payload["no_audio"] = c.NoAudio
	}
	if c.NoEffects {
		payload["no_effects"] = c.NoEffects
	}
	if c.NoTransitions {
		payload["no_transitions"] = c.NoTransitions
	}
	if c.MaxVideos > 0 {
		payload["max_videos"] = c.MaxVideos
	}
	if c.Subfolder != "" {
		payload["subfolder"] = c.Subfolder
	}
	if c.FolderName != "" {
		payload["folder_name"] = c.FolderName
	}
	if c.FolderID != "" {
		payload["folder_id"] = c.FolderID
	}
	if c.Metadata != nil {
		md := make(map[string]any, 6)
		if c.Metadata.Title != "" {
			md["title"] = c.Metadata.Title
		}
		if c.Metadata.Description != "" {
			md["description"] = c.Metadata.Description
		}
		if len(c.Metadata.Tags) > 0 {
			md["tags"] = c.Metadata.Tags
		}
		if c.Metadata.Category != "" {
			md["category"] = c.Metadata.Category
		}
		if c.Metadata.Author != "" {
			md["author"] = c.Metadata.Author
		}
		if len(c.Metadata.Extra) > 0 {
			md["extra"] = c.Metadata.Extra
		}
		payload["metadata"] = md
	}
	return payload
}
