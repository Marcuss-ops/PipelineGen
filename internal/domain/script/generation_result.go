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
// No durable field uses any, any, or map[string]any.
package script

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// EnvelopeVersion is the canonical schema_version of the
// GenerationEnvelopeResult envelope. Bumped when the envelope shape
// changes in an incompatible way. Always emitted in the result
// payload so callers can deserialise against multiple versions.
const EnvelopeVersion = 2

// Per-item terminal statuses for GenerationResult.Status.
// SUCCEEDED is strict: it is only emitted when the requested
// generation path was fully respected. SUCCEEDED_WITH_WARNINGS
// is emitted when a fallback was used. PARTIALLY_SUCCEEDED is
// reserved for batch/partial outcomes where some artifacts were
// produced but the item cannot be considered fully successful.
// FAILED is the terminal failure status.
const (
	ItemStatusSucceeded             = "SUCCEEDED"
	ItemStatusSucceededWithWarnings = "SUCCEEDED_WITH_WARNINGS"
	ItemStatusPartiallySucceeded    = "PARTIALLY_SUCCEEDED"
	ItemStatusFailed                = "FAILED"
)

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
	Title          string `json:"title,omitempty"`
	Language       string `json:"language,omitempty"`
	Model          string `json:"model,omitempty"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`

	// Script is the compact script surface requested by VidRush
	// consumers. Output remains the canonical historical surface.
	Script ScriptSummary `json:"script,omitempty"`

	// Canonical output (PR 9):
	//   ScriptOutput carries the canonical script text, word count,
	//   and structured specscene.
	Output ScriptOutput `json:"output"`

	// VidRushSegments carries the per-segment semantic extraction
	// and media resolution results used by the VidRush payload.
	Segments []VidRushSegmentResult `json:"segments,omitempty"`

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

	// StageProgress exposes real completed/total counters by phase and
	// language. It is populated from child and inline processor outcomes;
	// percentages remain a compatibility presentation only.
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`

	// Status is the canonical per-item outcome status. It is
	// ItemStatusSucceeded for a clean generation,
	// ItemStatusSucceededWithWarnings when a clip-native generation
	// used the prose fallback (fallback_policy=allow_prose),
	// ItemStatusPartiallySucceeded for partial/batch outcomes, and
	// ItemStatusFailed for terminal failures.
	Status string `json:"status,omitempty"`

	// ModeInfo describes the requested vs actual generation mode
	// for clip-aware sources. Populated when the source involved
	// clips so callers can detect fallback usage.
	ModeInfo *GenerationModeInfo `json:"mode_info,omitempty"`

	AudioMode     string              `json:"audio_mode,omitempty"`
	AudioStrategy string              `json:"audio_strategy,omitempty"`
	FinalAudio    *FinalAudioArtifact `json:"final_audio,omitempty"`

	// Quality carries the editorial quality gate outcome. It is
	// always populated so callers can inspect the per-item quality
	// metrics even when the gate passes.
	Quality *GenerationQuality `json:"quality,omitempty"`

	// Provenance carries the complete generation provenance block
	// (doc_id, doc_link, source_type, source_text_hash, clip_ids,
	// requested_mode, used_mode, fallback_used, model, prompt_version,
	// planner_version).
	Provenance *GenerationProvenance `json:"provenance,omitempty"`
}

type FinalAudioArtifact struct {
	AssetID              string `json:"audio_asset_id"`
	Path                 string `json:"path"`
	DriveLink            string `json:"drive_link,omitempty"`
	Container            string `json:"container,omitempty"`
	AudioContractVersion string `json:"audio_contract_version"`
	AudioPlanVersion     string `json:"audio_plan_version"`
	AudioPlanSHA256      string `json:"audio_plan_sha256"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	Codec                string `json:"codec"`
	Profile              string `json:"profile"`
	SampleRate           int    `json:"sample_rate"`
	Channels             int    `json:"channels"`
	ChannelLayout        string `json:"channel_layout"`
	Bitrate              int64  `json:"bitrate"`
	DurationUS           int64  `json:"duration_us,omitempty"`
	DurationMS           int64  `json:"duration_ms"`
	StartPTS             int64  `json:"start_pts"`
	SizeBytes            int64  `json:"size_bytes"`
	FinalMix             bool   `json:"final_mix"`
	CopyEligible         bool   `json:"copy_eligible"`
}

// DocumentAudioRef is the already-published, language-scoped final audio
// reference projected into a document. It contains no local path and is
// intentionally independent from the audio compiler.
type DocumentAudioRef struct {
	AssetID    string `json:"audio_asset_id"`
	Language   string `json:"language"`
	DriveLink  string `json:"drive_link"`
	DurationMS int64  `json:"duration_ms"`
	SHA256     string `json:"sha256,omitempty"`
}

// GenerationModeInfo describes the requested and actual generation
// mode for a clip-aware generation item.
type GenerationModeInfo struct {
	// RequestedMode is the mode the caller requested. For
	// source.type=clips this is always "clip_native".
	RequestedMode string `json:"requested_mode"`

	// UsedMode is the mode that actually produced the output.
	// It is "clip_native" when the model emitted scenes and
	// "prose" when the prose-fallback heuristic was used.
	UsedMode string `json:"used_mode"`

	// FallbackUsed is true when the pipeline fell back to prose
	// because the model did not produce a 1:1 clip-to-scene plan.
	FallbackUsed bool `json:"fallback_used"`
}

// GenerationProvenance records the complete provenance metadata for
// a single generation item and the document it produced.
type GenerationProvenance struct {
	// DocID is the Google Doc document ID.
	DocID string `json:"doc_id,omitempty"`

	// DocLink is the Google Doc edit URL.
	DocLink string `json:"doc_link,omitempty"`

	// SourceType is the canonical source type ("text", "clips",
	// "catalog", "search", "curate").
	SourceType string `json:"source_type,omitempty"`

	// SourceTextHash is the SHA-256 hex digest of the source text
	// and clip evidence assembled text used for generation.
	SourceTextHash string `json:"source_text_hash,omitempty"`

	// ClipIDs lists the accepted clip IDs used in generation.
	ClipIDs []string `json:"clip_ids,omitempty"`

	// RequestedMode is the requested generation mode (e.g.
	// "clip_native" for source.type=clips).
	RequestedMode string `json:"requested_mode,omitempty"`

	// UsedMode is the actual generation mode ("clip_native" or
	// "prose").
	UsedMode string `json:"used_mode,omitempty"`

	// FallbackUsed is true when the pipeline fell back to prose.
	FallbackUsed bool `json:"fallback_used,omitempty"`

	// Model is the LLM model used for generation.
	Model string `json:"model,omitempty"`

	// PromptVersion is the prompt version used for generation.
	PromptVersion string `json:"prompt_version,omitempty"`

	// PlannerVersion is the planner version used for generation.
	PlannerVersion string `json:"planner_version,omitempty"`
}

// GenerationQuality records the editorial quality gate outcome for a
// single generation item. It is always populated so callers can
// inspect the metrics regardless of whether the gate passed.
type GenerationQuality struct {
	// LanguageRequested is the target language from the plan.
	LanguageRequested string `json:"language_requested"`

	// LanguageDetected is the language detected from the generated
	// text by the lightweight stop-word heuristic.
	LanguageDetected string `json:"language_detected"`

	// SourceTextCoverage is the ratio (0..1) of generated content
	// words that appear in the source text or clip evidence.
	SourceTextCoverage float64 `json:"source_text_coverage"`

	// SourceTextCoverageStatus makes an absent source explicit instead of
	// turning an unevaluated check into an artificial passing ratio.
	SourceTextCoverageStatus string `json:"source_text_coverage_status"`

	// ClipEvidenceCoverage is the ratio (0..1) of accepted clips
	// that are bound to a scene. For non-clip sources this is 1.0.
	ClipEvidenceCoverage float64 `json:"clip_evidence_coverage"`

	// UnsupportedClaims is the count of named entities in the
	// generated text that do not appear in the source text or clip
	// evidence.
	UnsupportedClaims int `json:"unsupported_claims"`

	// TargetWords is the requested target word count.
	TargetWords int `json:"target_words"`

	// ActualWords is the actual generated word count.
	ActualWords int `json:"actual_words"`

	// Passed is true when every quality gate check passed.
	Passed bool `json:"passed"`
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

	// Script mirrors Status using the cache vocabulary expected by
	// VidRush consumers ("HIT_EXACT", "MISS", ...). The historical
	// Status/Hit pair stays for compatibility.
	Script string `json:"script,omitempty"`

	// Segments exposes per-segment cache state keyed by segment_id.
	Segments map[string]SegmentCacheState `json:"segments,omitempty"`
}

// ScriptSummary is the compact script envelope surfaced by VidRush.
type ScriptSummary struct {
	Text      string `json:"text"`
	WordCount int    `json:"word_count"`
}

// ArtifactResult holds all postprocessor outputs in one typed bundle.
type ArtifactResult struct {
	// Voiceovers groups the published Drive links by language. Local
	// filesystem paths are intentionally excluded from this public surface.
	Voiceovers []VoiceoverLanguageArtifact `json:"voiceovers,omitempty"`
	// Document is the idempotently published Google Doc for this script.
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

// VoiceoverLanguageArtifact is the public multilingual voiceover index for
// one generated script. Links are kept per scene because each scene is a
// separate Drive audio artifact.
type VoiceoverLanguageArtifact struct {
	Language   string   `json:"language"`
	Status     string   `json:"status"`
	DriveLinks []string `json:"drive_links,omitempty"`
}

// DocumentArtifact is the stable result surface for a published Google Doc.
type DocumentArtifact struct {
	DocID           string `json:"doc_id"`
	DocLink         string `json:"doc_link"`
	Renderer        string `json:"renderer,omitempty"`
	SpecSceneSHA256 string `json:"specscene_sha256,omitempty"`
	SceneCount      int    `json:"scene_count,omitempty"`
	Language        string `json:"language,omitempty"`
}

// GenerationTimings holds elapsed-time metrics for each generation phase.
//
// Migration note: the audio-* fields below (tts_total_ms, audio_mix_ms,
// audio_encode_ms, audio_probe_ms, audio_hash_ms, audio_pipeline_total_ms,
// final_audio_duration_ms, and the audio_plan/timeline/clip_audio_prepare
// triple) are a legacy projection. The canonical durable audio timing
// contract is scriptgeneration.AudioPipelineMetrics
// (internal/capabilities/scripts/model.go, JSON "audio_metrics"). Add new
// audio timing fields there, not here; this struct is migration-only and must
// converge onto that capability contract.
type GenerationTimings struct {
	SourceResolveMs int64 `json:"source_resolve_ms,omitempty"`
	PlanBuildMs     int64 `json:"plan_build_ms,omitempty"`
	EngineMs        int64 `json:"engine_ms,omitempty"`

	// Per-postprocessor timings (keyed by processor name).
	PostprocessMs map[string]int64 `json:"postprocess_ms,omitempty"`

	// VidRush stage timings. They remain flat and machine-readable so an
	// operator can compare cold/warm runs without parsing processor labels.
	SegmentExtractionMs     int64 `json:"segment_extraction_ms,omitempty"`
	QueryGenerationMs       int64 `json:"query_generation_ms,omitempty"`
	ArtlistSearchMs         int64 `json:"artlist_search_ms,omitempty"`
	ArtlistStreamResolveMs  int64 `json:"artlist_stream_resolve_ms,omitempty"`
	ArtlistDownloadMs       int64 `json:"artlist_download_ms,omitempty"`
	InternetImageSearchMs   int64 `json:"internet_image_search_ms,omitempty"`
	InternetImageDownloadMs int64 `json:"internet_image_download_ms,omitempty"`
	ImageGenerationQueueMs  int64 `json:"image_generation_queue_ms,omitempty"`
	ImageGenerationMs       int64 `json:"image_generation_ms,omitempty"`
	DriveUploadMs           int64 `json:"drive_upload_ms,omitempty"`
	SQLiteMs                int64 `json:"sqlite_ms,omitempty"`
	QdrantMs                int64 `json:"qdrant_ms,omitempty"`
	BindingMs               int64 `json:"binding_ms,omitempty"`
	TTSTotalMs              int64 `json:"tts_total_ms,omitempty"`
	TTSCalls                int   `json:"tts_calls,omitempty"`
	TimelineCompileMs       int64 `json:"timeline_compile_ms,omitempty"`
	AudioPlanCompileMs      int64 `json:"audio_plan_compile_ms,omitempty"`
	ClipAudioPrepareMs      int64 `json:"clip_audio_prepare_ms,omitempty"`
	AudioMixMs              int64 `json:"audio_mix_ms,omitempty"`
	AudioEncodeMs           int64 `json:"audio_encode_ms,omitempty"`
	AudioEncodePasses       int   `json:"audio_encode_passes,omitempty"`
	AudioProbeMs            int64 `json:"audio_probe_ms,omitempty"`
	AudioHashMs             int64 `json:"audio_hash_ms,omitempty"`
	AudioPipelineTotalMs    int64 `json:"audio_pipeline_total_ms,omitempty"`
	FinalAudioDurationMS    int64 `json:"final_audio_duration_ms,omitempty"`

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
// No durable field uses any, any, or map[string]any.
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
	ItemID    string            `json:"item_id"`
	Result    *GenerationResult `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
	ErrorCode string            `json:"error_code,omitempty"`
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
