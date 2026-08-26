package detail

// text_track.go defines the domain types for the asset_text_tracks table.
// Each TextTrack is a localized, versioned text resource (transcript,
// description, summary, title, or keywords) attached to a media asset.
//
// Design: one row per (asset_id, language_code, text_kind). Translations
// and re-transcriptions upsert into the same row. The text_hash field
// enables deterministic change detection for source_version computation.

import "time"

// TextTrackKind classifies the type of text content stored in a track.
type TextTrackKind string

const (
	TextTrackTranscript    TextTrackKind = "transcript"
	TextTrackDescription   TextTrackKind = "description"
	TextTrackSummary       TextTrackKind = "summary"
	TextTrackTitle         TextTrackKind = "title"
	TextTrackKeywords      TextTrackKind = "keywords"
	TextTrackVisualSummary TextTrackKind = "visual_summary"
	// TextTrackSearchText is the lexical/semantic text recovered from a
	// historical projection. It is kept as a first-class artifact so the
	// projection can be rebuilt without treating Qdrant as canonical state.
	TextTrackSearchText TextTrackKind = "search_text"
)

// TextTrackSource records the provenance of a text track — how the text
// was obtained. This drives the resolver's priority chain.
type TextTrackSource string

const (
	TextSourceProvided        TextTrackSource = "provided"
	TextSourceYouTubeSubtitle TextTrackSource = "youtube_subtitle"
	TextSourceWhisper         TextTrackSource = "whisper"
	TextSourceTranslation     TextTrackSource = "translation"
	TextSourceManual          TextTrackSource = "manual"
	TextSourceVisualAnalysis  TextTrackSource = "visual_analysis"
	TextSourceQdrantRecovery  TextTrackSource = "qdrant-recovery"
)

// TextTrackStatus is the tri-state lifecycle of a text track.
// READY  = text content is available and usable.
// PENDING = transcription/translation has been requested but not completed.
// FAILED = transcription/translation failed; may be retried.
type TextTrackStatus string

const (
	TextTrackReady   TextTrackStatus = "READY"
	TextTrackPending TextTrackStatus = "PENDING"
	TextTrackFailed  TextTrackStatus = "FAILED"
)

// TextTrack is the domain model for a single localized text resource
// attached to a media asset. It maps 1:1 to a row in asset_text_tracks.
//
// PR-CATALOG-MULTILINGUA step 2 (July 2026): SourceTrackID +
// SourceTextHash extend the canonical surface so the audit trail
// (this row's parent source-language track + parent text hash) is
// persisted at the row level instead of derived from a parent query.
// PR-CATALOG-MULTILINGUA step 4 (July 2026): PromptVersion,
// TranslationKey, and IsCurrent complete the fingerprint +
// audit-trail surface for the lookup-before-translate gate.
type TextTrack struct {
	ID                 int64           `json:"id"`
	AssetID            string          `json:"asset_id"`
	LanguageCode       string          `json:"language_code"`
	TextKind           TextTrackKind   `json:"text_kind"`
	TextContent        string          `json:"text_content"`
	SourceType         TextTrackSource `json:"source_type"`
	SourceLanguageCode string          `json:"source_language_code"`
	IsOriginal         bool            `json:"is_original"`
	Provider           string          `json:"provider"`
	ModelName          string          `json:"model_name"`
	ModelVersion       string          `json:"model_version"`
	// PromptVersion is the prompt-template version that produced
	// this row. EMPTY when the provider does not expose a template
	// taxonomy (matches the model_version convention for
	// text-track provenance). Stored as a TEXT NOT NULL DEFAULT ''
	// column added by migration 155.
	PromptVersion string `json:"prompt_version"`
	TextHash      string `json:"text_hash"`
	SourceVersion string `json:"source_version"`
	// TranslationKey is the deterministic SHA-256 fingerprint of
	// the translation REQUEST (source_text_hash + target_language +
	// translation_model + model_version + prompt_version). Persisted
	// verbatim by the application layer's insert path so the
	// lookup-before-translate gate (FindCurrentForTranslation) can
	// match by index without recomputing the SHA-256 on every
	// materialization cycle. Canonical formula owned by
	// asset.TranslationKey — NEVER re-implement inline.
	// TEXT NOT NULL DEFAULT '' column added by migration 155.
	TranslationKey string `json:"translation_key"`
	// SourceTrackID is the FK to the parent source-language track
	// WHEN this row is a translation or derived artifact
	// (TextSourceTranslation etc.). NULL for rows that ARE the
	// source (e.g. a whisper EN transcript). Persisted as INTEGER
	// nullable FK ON DELETE SET NULL by migration 156, so the
	// audit-trail link survives bulk deletion of the source row
	// (the child row stays; the link becomes NULL, observable
	// in forensic dumps as "translation whose source row was
	// later removed"). godlike/07 honest lock: NULL is
	// semantically-meaningful — it is NOT equivalent to "this
	// row is a source" by accident; callers MUST distinguish.
	SourceTrackID *int64 `json:"source_track_id,omitempty"`
	// SourceTextHash is the persisted SHA-256 of the source text.
	// The lookup-before-translate gate (migration 155) computes
	// translation_key FROM this hash; persisting it on the row
	// means future agents can read the source hash directly
	// without joining to a parent query. TEXT NOT NULL DEFAULT ''
	// column added by migration 156; '' means "no hash available
	// (pre-migration row, OR back-fill pending)" — callers MUST
	// treat '' as a fall-through signal, NOT a hard-error.
	SourceTextHash string `json:"source_text_hash"`
	// IsCurrent is the "this is the live translation for this
	// (asset, language, kind) context" flag. The SQLite partial
	// UNIQUE INDEX idx_asset_text_tracks_current WHERE is_current=1
	// enforces the "at most one current row per context" invariant;
	// when a new translation is inserted, the prior is_current=1
	// row is flipped to is_current=0 atomically within the same
	// transaction (InsertTranslationWithAuditPredecessor). INTEGER
	// NOT NULL DEFAULT 1 column added by migration 155.
	IsCurrent  bool            `json:"is_current"`
	Confidence *float64        `json:"confidence,omitempty"`
	Status     TextTrackStatus `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}
