// Package script — generation_result.go defines the canonical
// GenerationResult and its nested result types. Every generation
// (single or batch item) produces exactly one GenerationResult.
//
// PR 9 (June 2026): canonical nested shape added — Output, Source,
// Cache, Artifacts replace the old flat fields.
// PR 13 (June 2026): deprecated flat fields removed — all consumers
// now use the nested canonical fields exclusively.
//
// No durable field uses interface{}, any, or map[string]any.
package script

// GenerationResult is the canonical output of a single generation
// item. It carries the generated script, postprocessor outputs,
// and timing metadata. The caller matches it to the original
// GenerationItemV2 via the ID field.
type GenerationResult struct {
	// ItemID echoes GenerationItemV2.ID for correlation.
	ItemID string `json:"item_id,omitempty"`

	// ScriptID is the persisted script row ID, set when SaveToDB
	// was enabled on the plan. Zero when persistence is disabled.
	ScriptID int64 `json:"script_id,omitempty"`

	// ID is the legacy field; use ItemID instead.
	// Deprecated: use ItemID.
	ID string `json:"id,omitempty"`

	// ── Identity ──────────────────────────────────────────────────────
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Model    string `json:"model,omitempty"`

	// ── Canonical output (PR 9) ───────────────────────────────────────
	// Output carries the canonical script text, word count, and
	// structured specscene. This is THE single durable output shape.
	Output ScriptOutput `json:"output"`

	// ── Canonical source trace (PR 9) ─────────────────────────────────
	// Source records where the generation input came from.
	Source SourceTrace `json:"source,omitempty"`

	// ── Canonical cache info (PR 9) ───────────────────────────────────
	// Cache records the memory gate outcome.
	Cache CacheResult `json:"cache,omitempty"`

	// ── Canonical artifacts (PR 9) ────────────────────────────────────
	// Artifacts holds postprocessor outputs in one typed bundle.
	Artifacts ArtifactResult `json:"artifacts,omitempty"`

	// ── Timings ───────────────────────────────────────────────────────
	Timings GenerationTimings `json:"timings,omitempty"`

	// ── Warnings (non-fatal per-postprocessor) ─────────────────────────
	Warnings []string `json:"warnings,omitempty"`
}

// ScriptOutput is the canonical embedded output of script generation.
// Text is the single canonical script-text field. WordCount is derived
// from Text by the engine. SpecScene carries the structured scene
// breakdown with asset bindings.
type ScriptOutput struct {
	Text      string          `json:"text"`
	WordCount int             `json:"word_count"`
	SpecScene SpecSceneOutput `json:"specscene"`
}

// SourceTrace records where the generation input came from.
type SourceTrace struct {
	// SearchResults holds raw search hits (catalog or semantic).
	SearchResults []SearchResultItem `json:"search_results,omitempty"`
	// AcceptedClipIDs lists the clip IDs used in generation.
	AcceptedClipIDs []string `json:"accepted_clip_ids,omitempty"`
}

// CacheResult records the memory gate outcome.
type CacheResult struct {
	Status string `json:"status,omitempty"` // "exact_hit", "generated"
	Hit    bool   `json:"hit"`
}

// ArtifactResult holds all postprocessor outputs in one typed bundle.
//
// PR 3 (June 2026): EntitiesJSON replaced with typed Entities. The
// pre-PR-3 EntitiesJSON TODO has been resolved — the canonical entity
// shape now carries typed Person, Place, Concept slices, plus a Raw
// field for backward read-compat with pre-PR-3 rows.
type ArtifactResult struct {
	// Document holds the Google Doc link + ID.
	Document *DocumentArtifact `json:"document,omitempty"`
	// Metadata holds YouTube-style metadata.
	Metadata []VideoMetadata `json:"metadata,omitempty"`
	// Entities holds the typed entity extraction output. Replaces
	// the pre-PR-3 EntitiesJSON string.
	Entities *EntityResult `json:"entities,omitempty"`
}

// Entity is one item extracted by the entities processor.
//
// PR 3 (June 2026): typed shape replaces the pre-PR-3 free-form
// string-array. Value is the canonical entity name; Score (when
// present) is the confidence returned by the entity extractor.
// Future PRs will tighten the entity struct (type label, span
// offsets, etc.) once the entity extraction LLM emits typed slots.
type Entity struct {
	Value string  `json:"value"`
	Score float32 `json:"score,omitempty"`
}

// EntityResult is the typed entity extraction output. Carries grouped
// slots (Persons, Places, Concepts) plus a Raw field for backward
// read-compat with pre-PR-3 untyped JSON dump rows.
//
// PR 3 (June 2026): introduced to replace the pre-PR-3 EntitiesJSON
// string. The Persons/Places/Concepts slices are empty by default —
// the entity extractor is responsible for parsing the postgen LLM
// output into these slots. Empty slices still yield a valid
// EntityResult (callers see a consistent shape across all generation
// flows).
type EntityResult struct {
	Persons  []Entity `json:"persons,omitempty"`
	Places   []Entity `json:"places,omitempty"`
	Concepts []Entity `json:"concepts,omitempty"`
	// Raw is the original postgen LLM JSON string, kept for backward
	// read-compat with rows written before PR 3.
	Raw string `json:"raw,omitempty"`
}

// DocumentArtifact holds the output of the document postprocessor.
type DocumentArtifact struct {
	DocLink string `json:"doc_link,omitempty"`
	DocID   string `json:"doc_id,omitempty"`
	Status  string `json:"status,omitempty"` // "completed", "failed"
}

// VideoMetadata holds YouTube-style metadata for a script result.
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// VoiceoverResult holds the result of a single voiceover generation.
// Deprecated: use SpecScene.Bindings.Voiceover.
type VoiceoverResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	Status     string `json:"status"` // "completed", "failed"
	Link       string `json:"link,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	Language   string `json:"language,omitempty"`
}

// SceneImageResult holds the result of a single scene image generation.
// Deprecated: use SpecScene.Bindings.Image.
type SceneImageResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	ImageURL   string `json:"image_url,omitempty"`
	DriveLink  string `json:"drive_link,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// ClipSceneResult holds a single clip-anchored scene from clip-aware
// generation.
// Deprecated: use SpecScene.Bindings.Clip.
type ClipSceneResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	ClipID     string `json:"clip_id,omitempty"`
	DriveLink  string `json:"drive_link,omitempty"`
	Kind       string `json:"kind,omitempty"` // "clip", "narration"
}

// GenerationTimings holds elapsed-time metrics for each generation phase.
type GenerationTimings struct {
	SourceResolveMs int64 `json:"source_resolve_ms,omitempty"`
	PlanBuildMs     int64 `json:"plan_build_ms,omitempty"`
	EngineMs        int64 `json:"engine_ms,omitempty"`

	// Per-postprocessor timings (keyed by processor name).
	PostprocessMs map[string]int64 `json:"postprocess_ms,omitempty"`

	TotalMs int64 `json:"total_ms"`
}

// ── GenerationEnvelopeResult (PR 10) ──────────────────────────────

// GenerationEnvelopeResult is the canonical typed result returned by
// the script.generate job handler. It carries either a single
// GenerationResult or a batch via Items + Summary. The handler
// builds this struct and serialises it to the job-system map at the
// boundary layer.
//
// No durable field uses interface{}, any, or map[string]any.
type GenerationEnvelopeResult struct {
	OK bool `json:"ok"`

	// Single carries the result when the envelope contained exactly
	// one item. Never marshalled directly — toMap() flattens it.
	Single *GenerationResult `json:"-"`

	// Items carries per-item outcomes when the envelope contained
	// multiple items. Each item has exactly one of Result or Error.
	Items []GenerationEnvelopeItem `json:"items,omitempty"`

	// Summary aggregates multi-item counts.
	Summary *GenerationEnvelopeSummary `json:"summary,omitempty"`

	// Warnings carries non-per-item observations.
	Warnings []string `json:"warnings,omitempty"`
}

// GenerationEnvelopeItem records the outcome of a single item within
// a multi-item result. It aliases GenerateManyItemResult from the
// application layer — the two are structurally identical and any
// field addition must be mirrored in both types.
//
// TODO(PR 12): consolidate with GenerateManyItemResult.
type GenerationEnvelopeItem struct {
	ItemID string            `json:"item_id"`
	Result *GenerationResult `json:"result,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// GenerationEnvelopeSummary holds aggregate counts for a multi-item
// result.
type GenerationEnvelopeSummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}
