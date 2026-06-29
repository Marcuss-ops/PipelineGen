// Package script — generation_result.go defines the canonical
// GenerationResult and its nested result types. Every generation
// (single or batch item) produces exactly one GenerationResult.
//
// PR 7 (June 2026): the canonical job-emitted envelope shape is
//
//	GenerationEnvelopeResult {
//	    Version  int
//	    OK       bool
//	    Items    []GenerationEnvelopeItem
//	    Summary  GenerationEnvelopeSummary
//	    Warnings []string
//	}
//
// The legacy `Single` field was REMOVED. Single-item jobs emit the
// same canonical envelope with len(Items)==1. The job broker
// boundary layer (jobs.Service.RegisterHandler dispatch) is the
// only legal user of map[string]any; everything inside the
// application layer stays typed.
//
// PR 3 (June 2026): Entities is the canonical typed field; the
// raw JSON read-only mirror is still emitted for legacy consumers
// but new writers MUST populate `Entities` directly.
//
// No durable field uses interface{}, any, or map[string]any.
package script

// EnvelopeVersion is the canonical schema_version of the
// GenerationEnvelopeResult envelope. Bumped when the envelope shape
// changes in an incompatible way. Always emitted in the result
// payload so callers can deserialise against multiple versions.
const EnvelopeVersion = 2

// GenerationResult is the canonical output of a single generation
// item. It carries the generated script, postprocessor outputs,
// and timing metadata. The caller matches it to the original
// GenerationItemV2 via the ItemID field.
//
// PR 9 (June 2026): the deprecated `ID` field was REMOVED.
// Use ItemID for correlation. The legacy ID-to-ItemID aliasing
// gateway is gone — callers that previously read `result.ID`
// now read `result.ItemID`. Production code has zero references
// to `GenerationResult.ID` (verified by Check 13 of
// scripts/ci-architectural-checks.sh, which forbids any future
// reintroduction).
type GenerationResult struct {
	// ItemID echoes GenerationItemV2.ID for correlation.
	ItemID string `json:"item_id,omitempty"`

	// ScriptID is the persisted script row ID, set when SaveToDB
	// was enabled on the plan. Zero when persistence is disabled.
	ScriptID int64 `json:"script_id,omitempty"`

	// Identity
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Model    string `json:"model,omitempty"`

	// Canonical output (PR 9):
	//   ScriptOutput carries the canonical script text, word count,
	//   and structured specscene.
	Output ScriptOutput `json:"output"`

	// Canonical source trace (PR 9):
	//   Source records where the generation input came from.
	Source SourceTrace `json:"source,omitempty"`

	// Canonical cache info (PR 9):
	//   Cache records the memory gate outcome.
	Cache CacheResult `json:"cache,omitempty"`

	// Canonical artifacts (PR 9):
	//   ArtifactResult bundles every postprocessor output.
	Artifacts ArtifactResult `json:"artifacts,omitempty"`

	// Timings
	Timings GenerationTimings `json:"timings,omitempty"`

	// Warnings (non-fatal per-postprocessor)
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
type ArtifactResult struct {
	// Document holds the Google Doc link + ID.
	Document *DocumentArtifact `json:"document,omitempty"`
	// Metadata holds YouTube-style metadata.
	Metadata []VideoMetadata `json:"metadata,omitempty"`
	// Entities is the canonical typed V1 entity output (PR 3).
	// Producers MUST populate Entities directly from the
	// EntityExtractor port; consumers MUST read fields
	// (Persons / Places / Concepts) rather than parsing any
	// raw JSON.
	Entities *EntityResult `json:"entities,omitempty"`
	// EntitiesJSON holds a read-only JSON-marshalled view of
	// Entities. Populated by buildGenerationResult for
	// backward-compatibility with downstream consumers that
	// still parse raw JSON. New producers MUST NOT generate
	// entities from this field alone — see PR 3 spec:
	// "Non generare nuovi record basati esclusivamente sul
	// campo Raw". Persists only as a courtesy round-trip
	// marshalling of Entities.
	EntitiesJSON string `json:"entities_json,omitempty"`
}

// DocumentArtifact holds the output of the document postprocessor.
type DocumentArtifact struct {
	DocLink string `json:"doc_link,omitempty"`
	DocID   string `json:"doc_id,omitempty"`
	Status  string `json:"status,omitempty"` // "completed", "failed"
}

// VideoMetadata holds YouTube-style metadata for a script result.
//
// TranslationStatus (PR 0.6 close-out, June 2026) is the explicit marker
// for whether the Title / Description / Tags fields are realised
// translations. Values:
//
//	"translated"    — Title/Description/Tags are populated from a
//	                   successful translator call (or directly from the
//	                   English source for Language=="en"). This is the
//	                   "happy path" — fields reflect their canonical
//	                   translation.
//	"untranslated"  — Translator returned an error or produced an empty
//	                   string. Title/Description/Tags are explicitly
//	                   cleared (empty/empty/nil) so callers cannot
//	                   mistakenly surface the original `Title` or
//	                   `enDesc` text as a "successful translation".
//	                 Per godlike/07 (no-fake-availability), the
//	                   silent-success fallback was removed; this status
//	                   is the only legal alternative to "translated".
//	""              — Backward-compat: legacy callers that pre-date the
//	                   field. Treated as "translated" for reading
//	                   purposes (the field pre-existed as a populated
//	                   payload).
//
// P0.18 (successive wave) will replace the per-item string status with
// a richer TranslationError field; until then this string sentinel is
// the contract every reader consumes.
type VideoMetadata struct {
	Language          string   `json:"language"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Tags              []string `json:"tags"`
	TranslationStatus string   `json:"translation_status,omitempty"`
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

// GenerationEnvelopeResult is the canonical typed envelope
// returned by the script.generate job handler. PR 7 contract:
//
//   - Version is ALWAYS EnvelopeVersion (2).
//   - OK is derived from per-item outcomes.
//   - Items holds exactly one entry per input item. For a
//     single-item run, len(Items)==1 with the canonical
//     GenerationResult embedded.
//   - Summary holds the aggregate counts. For a single-item run,
//     Total=Succeeded+Failed=1.
//   - Warnings holds non-per-item observations.
//
// The legacy `Single` field is GONE. Single-item runs and
// multi-item runs now emit the same canonical shape.
//
// No durable field uses interface{}, any, or map[string]any.
type GenerationEnvelopeResult struct {
	// Version tracks the envelope schema_version. Bumped when the
	// shape changes incompatibly. Always EnvelopeVersion (2)
	// today.
	Version int `json:"version"`

	// OK is true when every item succeeded (Summary.Failed == 0).
	OK bool `json:"ok"`

	// Items holds per-item outcomes. Always populated; even a
	// single-item run has len(Items)==1.
	Items []GenerationEnvelopeItem `json:"items"`

	// Summary aggregates the per-item counts. For single items:
	// Total=Succeeded+Failed=1.
	Summary GenerationEnvelopeSummary `json:"summary"`

	// Warnings carries non-per-item observations.
	Warnings []string `json:"warnings,omitempty"`
}

// GenerationEnvelopeItem records the outcome of a single item within
// a multi-item result. PR 7 (June 2026) unifies the typed shape
// with the application-layer per-item record so callers no longer
// need distinct decoder paths for single vs batch outcomes.
//
// Any field addition must update both the canonical type and the
// alias declaration below.
type GenerationEnvelopeItem struct {
	ItemID string            `json:"item_id"`
	Result *GenerationResult `json:"result,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// GenerateManyItemResult is the canonical alias for the
// application-layer per-item record (GenerateManyResult.Items).
// PR 7 (June 2026) consolidation: the two are the same typed
// object; aliasing removes the parallel-struct drift. To add a
// field, edit GenerationEnvelopeItem (above); the alias flows
// through automatically.
type GenerateManyItemResult = GenerationEnvelopeItem

// GenerationEnvelopeSummary holds aggregate counts for a multi-item
// result. Always emitted (even for single-item runs) so callers can
// apply uniform shape-sensitivity without conditional paths.
//
// PR 7 change: Summary is now a value type, not a pointer.
// Empty values still marshal to the JSON present key.
type GenerationEnvelopeSummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}
