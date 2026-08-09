// Package types holds shared YouTube domain types extracted from the
// internal/application/youtube mega-package during PR3 Phase 2 (June 2026).
//
// These types are used across multiple files in the parent youtube package
// (metadata_enrich.go, manifest.go, intelligence_sync.go, enrichment.go,
// tag_utils.go, extractor_clean.go) and have been extracted here per
// AGENTS.md Pattern 5 to reduce the parent package's file count.
//
// The parent package re-exports these via zero-copy type aliases
// (type ClipMetadataFile = types.ClipMetadataFile) so existing callers
// compile without rename churn.
//
// PR5 Phase 3 (June 2026): extraction DTOs (ExtractRequest, ExtractResponse,
// ExtractItem, FolderInfo, ExtractStats, DestinationRequest) moved here so
// the new youtube/extraction/ capability service can import them without
// creating an import cycle with the parent youtube package.
//
// PR-G Phase 1b (June 2026): package renamed types→dto during the
// youtube/{types,extraction,metadata,segments,search,tagutil} → 7-way
// sub-package taxonomy melt.
package dto

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ClipMetadataFile is the human-readable metadata saved alongside each clip.
// It is serialized as JSON (metadata_<clip_id>.json) next to the clip MP4 and
// uploaded to Drive alongside the video file.

// LocalizedClipText carries a single localized text resource
// (transcript, description, summary, or title) for a clip segment.
// It is the wire format accepted by the extraction API and is
// converted to domain TextTrack rows during the YouTube write path.
type LocalizedClipText struct {
	LanguageCode       string  `json:"language_code"`
	SourceLanguageCode string  `json:"source_language_code,omitempty"`
	Transcript         string  `json:"transcript,omitempty"`
	Description        string  `json:"description,omitempty"`
	Summary            string  `json:"summary,omitempty"`
	Title              string  `json:"title,omitempty"`
	SourceType         string  `json:"source_type"`
	IsOriginal         bool    `json:"is_original,omitempty"`
	ModelName          string  `json:"model_name,omitempty"`
	ModelVersion       string  `json:"model_version,omitempty"`
	Confidence         float64 `json:"confidence,omitempty"`
}

// Segment represents a time-bounded clip segment with metadata
// extracted from the segment analysis pipeline.
type Segment struct {
	Start            string              `json:"start"`
	End              string              `json:"end"`
	Name             string              `json:"name"`
	Category         string              `json:"category,omitempty"`
	SourceTitle      string              `json:"source_title,omitempty"`
	SourceChannel    string              `json:"source_channel,omitempty"`
	Tags             []string            `json:"tags,omitempty"`
	Summary          string              `json:"summary,omitempty"`
	Topics           []string            `json:"topics,omitempty"`
	Speakers         []string            `json:"speakers,omitempty"`
	MentionedPeople  []string            `json:"mentioned_people,omitempty"`
	Hook             string              `json:"hook,omitempty"`
	QualityScore     float64             `json:"quality_score,omitempty"`
	SearchVisibility string              `json:"search_visibility,omitempty"`
	Texts            []LocalizedClipText `json:"texts,omitempty"`
}

// ── PR5 Phase 3: Extraction DTOs moved from parent package ──────────────

// ExtractRequest is the payload for a YouTube clip extraction request.
type ExtractRequest struct {
	URL            string              `json:"url"`
	Segments       []Segment           `json:"segments"`
	ForceKeyframes bool                `json:"force_keyframes"`
	Normalize      *bool               `json:"normalize,omitempty"`
	KeepAudio      *bool               `json:"keep_audio,omitempty"`
	WriteSummary   *bool               `json:"write_summary,omitempty"`
	Strategy       ExtractionStrategy  `json:"strategy,omitempty"`
	Concurrency    int                 `json:"concurrency,omitempty"`
	Destination    *DestinationRequest `json:"destination,omitempty"`
	// RequireAllLanguagesBeforeVideo overrides the global multilingual gate
	// for this extraction job. false allows a clip to commit with the
	// available transcript language(s) only.
	RequireAllLanguagesBeforeVideo *bool `json:"require_all_languages_before_video,omitempty"`
	Shuffle                        bool  `json:"shuffle,omitempty"`
}

// UnmarshalJSON rejects the pre-destination wire shape instead of silently
// dropping its folder fields. The extraction worker consumes this DTO after
// the async enqueue boundary, so accepting those legacy fields would enqueue
// a job that falls back to the default Drive hierarchy.
func (r *ExtractRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	legacyDestinationFields := []string{
		"group",
		"folder_id",
		"folder_path",
		"subfolder_name",
		"create_subfolder",
	}
	present := make([]string, 0, len(legacyDestinationFields))
	for _, field := range legacyDestinationFields {
		if _, ok := fields[field]; ok {
			present = append(present, field)
		}
	}
	if len(present) > 0 {
		return fmt.Errorf("destination fields must be nested under destination: %s", strings.Join(present, ", "))
	}

	type extractRequestAlias ExtractRequest
	var decoded extractRequestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = ExtractRequest(decoded)
	return nil
}

// DestinationRequest specifies the target folder for extraction output.
type DestinationRequest struct {
	Group           string `json:"group,omitempty"`
	FolderID        string `json:"folder_id,omitempty"`
	FolderPath      string `json:"folder_path,omitempty"`
	SubfolderName   string `json:"subfolder_name,omitempty"`
	CreateSubfolder bool   `json:"create_subfolder"`
}

// ExtractResponse is the result of a clip extraction operation.
type ExtractResponse struct {
	OK              bool          `json:"ok"`
	SourceURL       string        `json:"source_url"`
	VideoID         string        `json:"video_id,omitempty"`
	Folder          *FolderInfo   `json:"folder,omitempty"`
	Stats           *ExtractStats `json:"stats,omitempty"`
	Items           []ExtractItem `json:"items"`
	Error           string        `json:"error,omitempty"`
	DriveFolderID   string        `json:"drive_folder_id,omitempty"`
	DriveFolderPath string        `json:"drive_folder_path,omitempty"`
}

// FolderInfo holds resolved folder metadata for an extraction run.
type FolderInfo struct {
	ID               string `json:"id"`
	LocalFolderPath  string `json:"local_folder_path"`
	DriveFolderID    string `json:"drive_folder_id,omitempty"`
	DriveFolderPath  string `json:"drive_folder_path,omitempty"`
	ManifestTXTPath  string `json:"manifest_txt_path,omitempty"`
	ManifestJSONPath string `json:"manifest_json_path,omitempty"`
}

// ExtractStats tracks the outcome counts for an extraction run.
type ExtractStats struct {
	Requested int `json:"requested"`
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// ── PR4 Phase 1 (June 2026): TopicSearchRequest moved here from the
//     alias file types.go so the external HTTP handlers can import the
//     canonical struct via `yttypes.TopicSearchRequest` instead of
//     `youtube.TopicSearchRequest` (which was an inline-defined struct
//     in the now-deprecated shim file). After PR4-B finalisation
//     (internal sweep + ports.go/types.go deletion), this is the canonical
//     home and the alias file's struct definition is removed. ──

// TopicSearchRequest is the payload for the YouTube topic-search endpoint
// (POST /api/media/clips/search, GET .../search).
type TopicSearchRequest struct {
	Q     string `form:"q" json:"q" binding:"required"`
	Limit int    `form:"limit" json:"limit"`
	Sort  string `form:"sort" json:"sort"`
}

// ExtractItem represents a single processed clip from an extraction run.
type ExtractItem struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	Start           string `json:"start"`
	End             string `json:"end"`
	StartSeconds    int    `json:"start_seconds,omitempty"`
	EndSeconds      int    `json:"end_seconds,omitempty"`
	Duration        int    `json:"duration_seconds,omitempty"`
	Filename        string `json:"filename,omitempty"`
	FileHash        string `json:"file_hash,omitempty"`
	LocalPath       string `json:"local_path,omitempty"`
	DriveLink       string `json:"drive_link,omitempty"`
	DriveFileID     string `json:"drive_file_id,omitempty"`
	DownloadLink    string `json:"download_link,omitempty"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`
	DriveFolderPath string `json:"drive_folder_path,omitempty"`
}

// ── Commit C DTOs (PR-C-YouTube-Cutover, June 2026) ───────────────────────
//
// ProcessSegmentCommand is the typed input to the ProcessYouTubeSegmentUseCase.
// It carries only the per-segment fields the use case needs — the existing
// ExtractRequest is intentionally NOT reused to keep the use case decoupled
// from any URL-level aggregation the caller might perform.
//
// ProcessSegmentResult is the typed output. Item is the same ExtractItem the
// existing handlers already serialize, so back-compat is preserved without
// naming churn. The remaining fields (ID/FileHash/DriveFileID/DriveLink/
// IndexedRequestID) pre-populate the most-promoted clip record fields for
// callers that want to skip the Items deserialization.

type ProcessSegmentCommand struct {
	VideoID         string
	Segment         Segment
	Index           int
	PolicyVersion   string
	OutDir          string
	DriveFolderID   string
	DriveFolderPath string
	VideoURL        string
	ForceKeyframes  bool
	Normalize       *bool
	KeepAudio       *bool
	// Strategy is the typed ExtractionStrategy (Commit 2/6 #2).
	// The legacy `string` alias was promoted to a typed enum so
	// `cmd.Strategy == StrategyReplace` is a typed comparison
	// (no more string-literal typos). VideoCutRequest.Strategy
	// is still a string; the use case casts `string(cmd.Strategy)`
	// at the port boundary (process_segment.go::Execute).
	Strategy    ExtractionStrategy
	Destination *DestinationRequest
	// RequireAllLanguagesBeforeVideo is the per-job override propagated from
	// ExtractRequest. nil preserves the process-wide policy.
	RequireAllLanguagesBeforeVideo *bool
}

type ProcessSegmentResult struct {
	ID               string
	FileHash         string
	DriveFileID      string
	DriveLink        string
	IndexedRequestID string
	Status           string // "processed" | "skipped" | "failed"
	Error            error
	Item             ExtractItem
}

// ── Commit 2/6 (PR-C-YouTube-Cutover, June 2026) ───────────────────────
//
// ExtractionStrategy, SegmentPolicy, ClipAsset, and FailureCode are
// the typed ports / DTOs the Correttezza commit introduces. They live
// here (not in the use case package) so the application-layer ports
// and the infrastructure-side adapters reference the same canonical
// types without an import cycle (usecase → dto → ports → adapters).
//
// The full clip metadata builder is Commit 4 scope; Commit 2 ships
// the typed shape + the canonical fields so the ClipAtomicWriter
// port can move to ClipAsset end-to-end without further churn.

// ExtractionStrategy is the typed value the use case reads to decide
// cache bypass / replacement semantics. The constants below are the
// only recognised values; the handler normalises unknown strings
// to StrategyVerify at the API boundary.
type ExtractionStrategy string

const (
	// StrategyVerify is the default. Cache hit short-circuits the
	// pipeline (idempotent re-run on already-processed clips).
	StrategyVerify ExtractionStrategy = "verify"
	// StrategySkip skips re-processing even on cache miss (used by
	// the channel monitor when a video is already in the broker
	// pipeline for another reason).
	StrategySkip ExtractionStrategy = "skip"
	// StrategyReplace bypasses the cache lookup entirely so a
	// re-extract under the same clipID always re-runs the full
	// 9-step pipeline (used by the metadata-policy bump flow).
	StrategyReplace             ExtractionStrategy = "replace"
	StrategyYouTubeStockPartial ExtractionStrategy = "youtube_stock_partial"
)

// SegmentPolicy is the duration gate applied to every segment
// (LLM-discovered or API-supplied). MinDuration/MaxDuration are
// seconds; zero means "use default" (the canonical defaults are
// 4s / 60s, matching the user-requested clip-duration window —
// no effects, no transitions are applied; the YouTube extraction
// endpoint only cuts, preserves audio, uploads to Drive, writes
// to media_assets and indexes in Qdrant).
type SegmentPolicy struct {
	MinDuration int
	MaxDuration int
}

// DefaultSegmentPolicy returns the canonical Min=4s / Max=60s
// bounds. Both the LLM-discovered path and the API-supplied path
// in ProcessYouTubeSegmentUseCase.Execute apply this when the
// caller-supplied SegmentPolicy has zero values on either field.
func DefaultSegmentPolicy() SegmentPolicy {
	return SegmentPolicy{MinDuration: 4, MaxDuration: 60}
}

// ValidDuration applies Min/Max duration bounds to a derived
// segment duration (endSec - startSec). Returns nil on pass,
// typed *usecase.ExtractionError with FailureCodeDurationOutOfRange
// on fail. The caller is the use case's Step 1; the helper lives
// here so the policy is enforced in one place.
func (p SegmentPolicy) ValidDuration(duration int) bool {
	policy := p
	if policy.MinDuration == 0 {
		policy.MinDuration = DefaultSegmentPolicy().MinDuration
	}
	if policy.MaxDuration == 0 {
		policy.MaxDuration = DefaultSegmentPolicy().MaxDuration
	}
	return duration >= policy.MinDuration && duration <= policy.MaxDuration
}

// ClipAssetDrive bundles the Drive-side fields the ClipAtomicWriter
// needs in one nested struct. Keeping these out of ClipAsset's top
// level makes the DB column mapping (10 columns) explicit and
// refactor-resistant.
type ClipAssetDrive struct {
	FolderID    string
	FolderPath  string
	FileID      string
	WebViewLink string
}

// ClipAssetCoordinates bundles the timestamp-derived fields the
// ClipAtomicWriter needs (start/end in seconds + total duration).
// Kept nested to match the verdict's canonical shape (commit 2 #6).
type ClipAssetCoordinates struct {
	StartSec int
	EndSec   int
	Duration int
}

// ClipAsset is the canonical, strongly-typed internal domain entity
// the use case passes to the ClipAtomicWriter. The verdict's P1 #6
// mandates "il writer deve ricevere il record canonico, non un DTO
// di risposta HTTP" — ClipAsset is that record. ExtractItem stays
// the HTTP response shape; ClipAsset is the writer-bound canonical.
type ClipAsset struct {
	ID            string
	VideoID       string
	LocalPath     string
	FileHash      string
	SearchText    string
	Drive         ClipAssetDrive
	Coordinates   ClipAssetCoordinates
	Metadata      CanonicalClipMetadata
	PolicyVersion string
	// Texts carries the payload-provided localized texts (transcripts,
	// descriptions, etc.) that the ClipAtomicWriter persists as
	// asset_text_tracks in the same transaction as media_assets.
	// When non-empty, the writer converts them to domain TextTrack
	// rows and upserts them atomically. This eliminates the race
	// where a separate TextTrackResolver.Save() call could fail
	// silently after Step 9 committed.
	Texts []LocalizedClipText `json:"texts,omitempty"`
}
