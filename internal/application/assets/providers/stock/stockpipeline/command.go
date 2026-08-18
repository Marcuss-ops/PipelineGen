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
	Queries                        []SearchQuery       `json:"queries"`
	DirectURLs                     []string            `json:"direct_urls,omitempty"`
	DriveURLs                      []string            `json:"drive_urls,omitempty"`
	Clips                          []ClipSpec          `json:"clips,omitempty"`
	TotalMinutes                   int                 `json:"total_minutes"`
	TargetTotalDurationSeconds     int                 `json:"target_total_duration_seconds,omitempty"`
	TargetDurationPerSourceSeconds int                 `json:"target_duration_per_source_seconds,omitempty"`
	ClipsPerSource                 int                 `json:"clips_per_source,omitempty"`
	ClipDurationSeconds            int                 `json:"clip_duration_seconds,omitempty"`
	DownloadMode                   string              `json:"download_mode,omitempty"`
	ChunkDuration                  int                 `json:"chunk_duration,omitempty"`
	ClipDuration                   int                 `json:"clip_duration,omitempty"`
	SecondsPerSegment              int                 `json:"seconds_per_segment,omitempty"`
	NoAudio                        bool                `json:"no_audio,omitempty"`
	NoEffects                      bool                `json:"no_effects,omitempty"`
	NoTransitions                  bool                `json:"no_transitions,omitempty"`
	MaxVideos                      int                 `json:"max_videos,omitempty"`
	Subfolder                      string              `json:"subfolder"`
	FolderName                     string              `json:"folder_name"`
	DriveFolderID                  string              `json:"drive_folder_id,omitempty"`
	FolderID                       string              `json:"folder_id,omitempty"`
	Metadata                       *ChunkMetadataInput `json:"metadata,omitempty"`
	// Async selects broker dispatch when true. JSON omission and an
	// explicit "async": false both decode to false and run synchronously
	// through the runner; callers that need queued execution must send
	// "async": true explicitly.
	Async bool `json:"async,omitempty"`
	// Persist enables media_assets writing in sync mode (only when
	// Async=false). See StockRunPayload.Persist for the rationale.
	Persist bool `json:"persist,omitempty"`
}

// ValidateDurationContract validates the unambiguous per-source duration
// contract. Zero values mean the legacy request shape is in use; once any
// explicit field is supplied, all values must be present and consistent.
func ValidateDurationContract(total, perSource, clips, clipDuration int, mode string) error {
	if total == 0 && perSource == 0 && clips == 0 && clipDuration == 0 && mode == "" {
		return nil
	}
	if total <= 0 || perSource <= 0 || clips <= 0 || clipDuration <= 0 {
		return fmt.Errorf("explicit stock duration contract requires positive target_total_duration_seconds, target_duration_per_source_seconds, clips_per_source and clip_duration_seconds")
	}
	if perSource != clips*clipDuration {
		return fmt.Errorf("target_duration_per_source_seconds must equal clips_per_source × clip_duration_seconds (%d != %d)", perSource, clips*clipDuration)
	}
	if total%perSource != 0 {
		return fmt.Errorf("target_total_duration_seconds must be a multiple of target_duration_per_source_seconds")
	}
	if mode != "sections_only" {
		return fmt.Errorf("download_mode must be sections_only")
	}
	if clipDuration < 3 || clipDuration > 30 {
		return fmt.Errorf("clip_duration_seconds must be between 3 and 30 seconds")
	}
	return nil
}

// StockCommand is the canonical internal command the API layer hands to
// StockUseCase.Submit. It is independent of the API request types and the
// job-payload shape used by HandleJob — the use case and the worker each
// derive their own serialisation.
type StockCommand struct {
	SearchQueries []string
	// SearchQueryLimits is the per-query result cap for
	// search-and-run requests, aligned 1:1 with SearchQueries by
	// index. Zero means "use the runtime default". Only the
	// search-and-run request shape carries per-query limits; the
	// legacy /run path leaves this nil.
	SearchQueryLimits              []int
	DirectURLs                     []string
	DriveURLs                      []string
	Clips                          []ClipSpec
	TotalMinutes                   int
	TargetTotalDurationSeconds     int
	TargetDurationPerSourceSeconds int
	ClipsPerSource                 int
	ClipDurationSeconds            int
	DownloadMode                   string
	MaxVideos                      int
	ChunkDuration                  int
	ClipDuration                   int
	SecondsPerSegment              int
	NoAudio                        bool
	NoEffects                      bool
	NoTransitions                  bool
	Subfolder                      string
	FolderName                     string
	DriveFolderID                  string
	FolderID                       string
	Metadata                       *ChunkMetadataInput
	// Async: submitter-chosen sync vs jobs-broker dispatch decision.
	// Mirrors the same field on StockSearchAndRunRequest + StockRunPayload
	// for end-to-end wire-shape audit trail.
	Async bool
	// Persist enables media_assets writing in sync mode (only when
	// Async=false). Threaded through to RunInput so the Service can
	// route the sync path through the resilient orchestrator with a
	// synthetic broker lease.
	Persist bool
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
		SearchQueries:                  append([]string(nil), p.SearchQueries...),
		DirectURLs:                     append([]string(nil), p.DirectURLs...),
		DriveURLs:                      append([]string(nil), p.DriveURLs...),
		Clips:                          append([]ClipSpec(nil), p.Clips...),
		TotalMinutes:                   p.TotalMinutes,
		TargetTotalDurationSeconds:     p.TargetTotalDurationSeconds,
		TargetDurationPerSourceSeconds: p.TargetDurationPerSourceSeconds,
		ClipsPerSource:                 p.ClipsPerSource,
		ClipDurationSeconds:            p.ClipDurationSeconds,
		DownloadMode:                   p.DownloadMode,
		ChunkDuration:                  p.ChunkDuration,
		ClipDuration:                   p.ClipDuration,
		SecondsPerSegment:              p.SecondsPerSegment,
		NoAudio:                        p.NoAudio,
		NoEffects:                      p.NoEffects,
		NoTransitions:                  p.NoTransitions,
		MaxVideos:                      p.MaxVideos,
		Subfolder:                      p.Subfolder,
		FolderName:                     p.FolderName,
		DriveFolderID:                  p.DriveFolderID,
		FolderID:                       p.FolderID,
		Metadata:                       metadata,
		Async:                          p.Async,
		Persist:                        p.Persist,
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
	limits := make([]int, 0, len(r.Queries))
	for _, q := range r.Queries {
		queries = append(queries, q.Q)
		limits = append(limits, q.Limit)
	}
	var metadata *ChunkMetadataInput
	if r.Metadata != nil {
		metadata = &ChunkMetadataInput{
			Title:            r.Metadata.Title,
			Description:      r.Metadata.Description,
			BlockDescription: r.Metadata.BlockDescription,
			Tags:             r.Metadata.Tags,
			Category:         r.Metadata.Category,
			Author:           r.Metadata.Author,
			Extra:            r.Metadata.Extra,
		}
	}
	return &StockCommand{
		SearchQueries:                  queries,
		SearchQueryLimits:              limits,
		DirectURLs:                     append([]string(nil), r.DirectURLs...),
		DriveURLs:                      append([]string(nil), r.DriveURLs...),
		Clips:                          append([]ClipSpec(nil), r.Clips...),
		TotalMinutes:                   r.TotalMinutes,
		TargetTotalDurationSeconds:     r.TargetTotalDurationSeconds,
		TargetDurationPerSourceSeconds: r.TargetDurationPerSourceSeconds,
		ClipsPerSource:                 r.ClipsPerSource,
		ClipDurationSeconds:            r.ClipDurationSeconds,
		DownloadMode:                   r.DownloadMode,
		ChunkDuration:                  r.ChunkDuration,
		ClipDuration:                   r.ClipDuration,
		SecondsPerSegment:              r.SecondsPerSegment,
		NoAudio:                        r.NoAudio,
		NoEffects:                      r.NoEffects,
		NoTransitions:                  r.NoTransitions,
		MaxVideos:                      r.MaxVideos,
		Subfolder:                      r.Subfolder,
		FolderName:                     r.FolderName,
		DriveFolderID:                  r.DriveFolderID,
		FolderID:                       r.FolderID,
		Metadata:                       metadata,
		Async:                          r.Async,
		Persist:                        r.Persist,
	}, nil
}

func chunkMetadataFromRunPayload(m *StockRunPayloadMetadata) *ChunkMetadataInput {
	if m == nil {
		return nil
	}
	return &ChunkMetadataInput{
		Title:            m.Title,
		Description:      m.Description,
		BlockDescription: m.BlockDescription,
		Tags:             m.Tags,
		Category:         m.Category,
		Author:           m.Author,
		Extra:            m.Extra,
	}
}

// ToRunInput projects a StockCommand onto the runner's internal input
// shape. Single-step metadata conversion (StockCommand.Metadata and
// RunInput.Metadata share the underlying ChunkMetadataInput type), plus
// the Progress callback mapping and the Persist flag.
func (c *StockCommand) ToRunInput() *RunInput {
	if c == nil {
		return nil
	}
	return &RunInput{
		SearchQueries:                  append([]string(nil), c.SearchQueries...),
		SearchQueryLimits:              append([]int(nil), c.SearchQueryLimits...),
		DirectURLs:                     append([]string(nil), c.DirectURLs...),
		DriveURLs:                      append([]string(nil), c.DriveURLs...),
		Clips:                          append([]ClipSpec(nil), c.Clips...),
		TotalMinutes:                   c.TotalMinutes,
		TargetTotalDurationSeconds:     c.TargetTotalDurationSeconds,
		TargetDurationPerSourceSeconds: c.TargetDurationPerSourceSeconds,
		ClipsPerSource:                 c.ClipsPerSource,
		ClipDurationSeconds:            c.ClipDurationSeconds,
		DownloadMode:                   c.DownloadMode,
		ChunkDuration:                  c.ChunkDuration,
		ClipDuration:                   c.ClipDuration,
		SecondsPerSegment:              c.SecondsPerSegment,
		NoAudio:                        c.NoAudio,
		NoEffects:                      c.NoEffects,
		NoTransitions:                  c.NoTransitions,
		MaxVideos:                      c.MaxVideos,
		Subfolder:                      c.Subfolder,
		FolderName:                     c.FolderName,
		DriveFolderID:                  c.DriveFolderID,
		FolderID:                       c.FolderID,
		Metadata:                       c.Metadata,
		Progress:                       c.Progress,
		Persist:                        c.Persist,
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
	payload := make(map[string]any, 16)
	if len(c.SearchQueries) > 0 {
		payload["search_queries"] = c.SearchQueries
	}
	if len(c.SearchQueryLimits) > 0 {
		payload["search_query_limits"] = c.SearchQueryLimits
	}
	if len(c.DirectURLs) > 0 {
		payload["direct_urls"] = c.DirectURLs
	}
	if len(c.DriveURLs) > 0 {
		payload["drive_urls"] = c.DriveURLs
	}
	if len(c.Clips) > 0 {
		// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): defensive copy
		// of c.Clips so post-construction mutations to the caller's
		// slice do not leak into the jobs wire shape. The struct copy
		// shares the Tags slice header with the source, so we deep-copy
		// Tags too (godlike/06 SSOT: every boundary takes its own copy).
		// Mirrors the pattern in FromRunPayload / FromSearchAndRunRequest
		// / ToRunInput. The struct copy is sufficient for all value-typed
		// fields (Title, Description, URL, StartSec, EndSec, Round,
		// Category, Slug) — future fields are auto-included.
		clipsCopy := make([]ClipSpec, len(c.Clips))
		for i, clip := range c.Clips {
			clipsCopy[i] = clip
			if len(clip.Tags) > 0 {
				clipsCopy[i].Tags = append([]string(nil), clip.Tags...)
			}
		}
		payload["clips"] = clipsCopy
	}
	payload["total_minutes"] = c.TotalMinutes
	if c.TargetTotalDurationSeconds != 0 {
		payload["target_total_duration_seconds"] = c.TargetTotalDurationSeconds
	}
	if c.TargetDurationPerSourceSeconds != 0 {
		payload["target_duration_per_source_seconds"] = c.TargetDurationPerSourceSeconds
	}
	if c.ClipsPerSource != 0 {
		payload["clips_per_source"] = c.ClipsPerSource
	}
	if c.ClipDurationSeconds != 0 {
		payload["clip_duration_seconds"] = c.ClipDurationSeconds
	}
	if c.DownloadMode != "" {
		payload["download_mode"] = c.DownloadMode
	}
	if c.ChunkDuration != 0 {
		payload["chunk_duration"] = c.ChunkDuration
	}
	if c.ClipDuration != 0 {
		payload["clip_duration"] = c.ClipDuration
	}
	if c.SecondsPerSegment != 0 {
		payload["seconds_per_segment"] = c.SecondsPerSegment
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
	if c.DriveFolderID != "" {
		payload["drive_folder_id"] = c.DriveFolderID
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
		if c.Metadata.BlockDescription != "" {
			md["block_description"] = c.Metadata.BlockDescription
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
	if c.Async {
		payload["async"] = true
	}
	return payload
}
