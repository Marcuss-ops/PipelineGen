// Package asset — ClipSemanticMetadata (PR-CANONICAL-CLIP-METADATA, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the ClipSemanticMetadata type — the unified
// domain-level representation of a media clip's semantic metadata.
//
// Problem this solves:
//
//	YouTube has CanonicalClipMetadata (35 fields) at
//	internal/capabilities/youtube/dto/metadata_types.go and Stock has
//	ChunkState + ChunkMetadataEntry + StockRunMetadata (40+ fields) at
//	internal/capabilities/assets/providers/stock/stockpipeline/. Both
//	pipelines independently write metadata that eventually lands in
//	Qdrant's AssetData (60+ fields) at
//	internal/platform/qdrant/indexing/index_writer_types.go.
//
// This type unifies the two source-of-truth representations so:
//   - Qdrant PayloadMapper reads from ClipSemanticMetadata (via infra adapter)
//   - SearchTextComposer's AsSearchTextInput() derives from ClipSemanticMetadata
//   - Future pipelines (Artlist, Voiceover) adopt the same shape
//
// Adapters:
//   - FromCanonicalClipMetadata (application layer, follow-up PR)
//   - FromStockRunMetadata + FromChunkState (application layer, follow-up PR)
//   - AsAssetData (infra layer, follow-up PR in internal/platform/qdrant/)
//
// godlike/07 minimum-blast-radius: additive-only. Existing per-pipeline
// metadata types continue working; callers that adopt ClipSemanticMetadata
// deprecate their local derivation over time.
package asset

import (
	"strconv"
	"strings"
)

// ClipSemanticMetadata is the canonical domain-level representation of a
// media clip's semantic metadata. It unifies YouTube's CanonicalClipMetadata
// and Stock's ChunkState + StockRunMetadata into a single shape that
// Qdrant's AssetData and the SearchTextComposer can consume.
//
// Field naming convention:
//   - Identity fields use the pipeline-canonical names (AssetID, JobID)
//   - Content fields are pipeline-agnostic (Title, Description, Tags)
//   - Drive fields use the canonical locator name (DriveLink, not DrivePath
//     or RemoteWebViewLink)
//   - Timestamps use float64 (Stock uses float64; YouTube adapters cast int)
//   - ContentHash is the canonical name for both YouTube's ContentHash and
//     Stock's SHA256
//
// godlike/06 SSOT: this struct is the SOLE canonical owner of the unified
// metadata field set. The 19 first-class Qdrant fields (PR-006) and the
// timestamp/drive enrichment fields (PR-TIMESTAMP-FOLDER-LINK) all map
// 1:1 from this type to AssetData.
type ClipSemanticMetadata struct {
	// ── Identity & Provenance ──────────────────────────────────────────

	// AssetID is the canonical media_assets.id primary key.
	// YouTube: yt_{videoID}_{start}_{end}_{policy}
	// Stock: planner:{sha256-prefix}:{index}
	AssetID string `json:"asset_id,omitempty"`

	// JobID is the job identifier that produced this clip.
	// YouTube: from outbox envelope; Stock: from RunInput.FolderID.
	JobID string `json:"job_id,omitempty"`

	// WorkflowID is the workflow identifier for the pipeline run.
	// YouTube: same as JobID; Stock: from RunInput.FolderID.
	WorkflowID string `json:"workflow_id,omitempty"`

	// RunFingerprint is the deterministic run identifier.
	// YouTube: from outbox; Stock: from orchestrator step 1.
	RunFingerprint string `json:"run_fingerprint,omitempty"`

	// PolicyVersion is the extraction policy version tag (e.g. "v1",
	// "stock_timestamp_v1"). Used for supersede gate CAS.
	PolicyVersion string `json:"policy_version,omitempty"`

	// ContentHash is the SHA-256 fingerprint of the asset's content.
	// YouTube: SHA-256 of searchable text; Stock: SHA-256 of video bytes.
	// Canonical name for both YouTube's ContentHash and Stock's SHA256.
	// This is the source_version the IndexingHandler supersede gate uses.
	ContentHash string `json:"content_hash,omitempty"`

	// ── Content ────────────────────────────────────────────────────────

	// Title is the human-readable clip title.
	// YouTube: ClipMetadataInput.Title or derived from segment name;
	// Stock: ChunkState.Title (propagated from ClipPlan).
	Title string `json:"title,omitempty"`

	// Description is the long-form prose description / summary.
	// YouTube: CanonicalClipMetadata.Summary (the 240-char clip summary);
	// Stock: ChunkState.Description (the human-readable English summary).
	Description string `json:"description,omitempty"`

	// Summary is an optional shorter abstract distinct from Description.
	// YouTube: CanonicalClipMetadata.Summary maps to Description above
	// (the 240-char clip summary IS the description for YouTube).
	// Stock: empty (Description is the primary prose field).
	// Kept for future pipelines that have both a short and long summary.
	Summary string `json:"summary,omitempty"`

	// Hook is the attention-grabbing opening phrase.
	// YouTube: LLM-discovered hook; Stock: empty.
	Hook string `json:"hook,omitempty"`

	// Tags is the merged-deduplicated tag list.
	// YouTube: union of SourceTags + ClipTags + SearchKeywords + Topics +
	// Speakers + MentionedPeople + Tags (deduplicated);
	// Stock: ChunkState.Tags.
	Tags []string `json:"tags,omitempty"`

	// Category is the content category (e.g. "boxing", "tutorial").
	// YouTube: empty (NormalizedGroup maps to Group, not Category);
	// Stock: ChunkState.Category.
	Category string `json:"category,omitempty"`

	// SearchVisibility is the LLM-assigned visibility tier.
	// YouTube: from metadata builder; Stock: empty.
	SearchVisibility string `json:"search_visibility,omitempty"`

	// ── Quality & Timing ───────────────────────────────────────────────

	// QualityScore is the weighted composite score in [0.0, 1.0].
	// YouTube: from metadata builder; Stock: empty (not computed).
	QualityScore float64 `json:"quality_score,omitempty"`

	// SponsorSegment is true when the clip matches sponsored-content regex.
	// YouTube: from metadata builder; Stock: false.
	SponsorSegment bool `json:"sponsor_segment,omitempty"`

	// StartSec is the clip start timestamp in seconds.
	// YouTube: int cast to float64; Stock: ChunkState.StartSec (float64).
	StartSec float64 `json:"start_sec,omitempty"`

	// EndSec is the clip end timestamp in seconds.
	EndSec float64 `json:"end_sec,omitempty"`

	// DurationSec is the clip duration (EndSec - StartSec).
	// Pre-computed for convenience; callers may also compute from the above.
	DurationSec float64 `json:"duration_sec,omitempty"`

	// ── Source & Origin ────────────────────────────────────────────────

	// SourceProvider identifies the source platform.
	// YouTube: "youtube"; Stock: "pexels", "pixabay", "youtube", "unknown".
	SourceProvider string `json:"source_provider,omitempty"`

	// SourceURL is the original source URL.
	// YouTube: video URL; Stock: ChunkState.SourceURL.
	SourceURL string `json:"source_url,omitempty"`

	// SourceVideoID is the provider-native video ID.
	// YouTube: YouTube video ID (e.g. "vdC5GXxS-qU");
	// Stock: ChunkState.SourceVideoID (YouTube video ID when provider=youtube).
	SourceVideoID string `json:"source_video_id,omitempty"`

	// Origin is the content origin descriptor.
	// YouTube: empty; Stock: "stock" (from finalizer metadata).
	Origin string `json:"origin,omitempty"`

	// Destination is the content destination descriptor.
	// YouTube: empty; Stock: "stock" (from finalizer metadata).
	Destination string `json:"destination,omitempty"`

	// NormalizedGroup is the canonical group name for folder routing.
	// YouTube: "general", "tutorial", etc.; Stock: empty.
	NormalizedGroup string `json:"normalized_group,omitempty"`

	// ── Drive & Locators ───────────────────────────────────────────────

	// DriveLink is the canonical Drive web-view link.
	// YouTube: DrivePath (WebViewLink); Stock: RemoteWebViewLink.
	// Canonical name for the Drive locator consumed by Qdrant payload.
	DriveLink string `json:"drive_link,omitempty"`

	// DriveFileID is the Google Drive file ID.
	// YouTube: empty (not always available); Stock: RemoteFileID.
	DriveFileID string `json:"drive_file_id,omitempty"`

	// LocalPath is the local filesystem path.
	// YouTube: empty (ephemeral); Stock: ChunkState.LocalPath.
	LocalPath string `json:"local_path,omitempty"`

	// TimestampDriveFolderLink is the WebViewLink of the parent timestamp
	// Drive folder. Stock only (PR-TIMESTAMP-FOLDER-LINK).
	TimestampDriveFolderLink string `json:"timestamp_drive_folder_link,omitempty"`

	// TimestampFolderID is the Google Drive folder ID of the parent
	// timestamp folder. Stock only (PR-TIMESTAMP-FOLDER-LINK).
	TimestampFolderID string `json:"timestamp_folder_id,omitempty"`

	// ── LLM Enrichment ────────────────────────────────────────────────

	// Event is the event label (e.g. "Pacquiao vs Broner press conference").
	// YouTube: empty; Stock: StockRunMetadata.Event (plumbing-on-nil).
	Event string `json:"event,omitempty"`

	// Round is the boxing-style round number. Zero = not specified.
	// YouTube: empty; Stock: ChunkState.Round / StockRunMetadata.Round.
	Round int `json:"round,omitempty"`

	// Scene is the scene description.
	// YouTube: empty; Stock: StockRunMetadata.Scene (plumbing-on-nil).
	Scene string `json:"scene,omitempty"`

	// Subject is the content subject.
	// YouTube: empty; Stock: StockRunMetadata.Subject (plumbing-on-nil).
	Subject string `json:"subject,omitempty"`

	// Entities are LLM-discovered entities.
	// YouTube: empty; Stock: StockRunMetadata.Entities (plumbing-on-nil).
	Entities []string `json:"entities,omitempty"`

	// Speakers are people speaking in the clip.
	// YouTube: from metadata builder; Stock: empty.
	Speakers []string `json:"speakers,omitempty"`

	// MentionedPeople are people mentioned (not speaking) in the clip.
	// YouTube: from metadata builder; Stock: empty.
	MentionedPeople []string `json:"mentioned_people,omitempty"`

	// Topics are the topic labels for semantic indexing.
	// YouTube: from metadata builder; Stock: empty.
	Topics []string `json:"topics,omitempty"`

	// ── Workflow / Indexing ────────────────────────────────────────────

	// ChunkIndex is the zero-based chunk index within the pipeline run.
	// YouTube: 0 (single clip per run); Stock: ChunkState.Index.
	ChunkIndex int `json:"chunk_index,omitempty"`

	// TotalChunks is the total number of chunks in the pipeline run.
	// YouTube: 1; Stock: ChunkState.TotalChunks.
	TotalChunks int `json:"total_chunks,omitempty"`

	// IndexingStatus is the lifecycle state hint for the Qdrant payload.
	// YouTube: "INDEXING_PENDING" (projection-time literal);
	// Stock: StockRunMetadata.IndexingStatus.
	IndexingStatus string `json:"indexing_status,omitempty"`

	// ── Transcript & Embeddings ────────────────────────────────────────

	// TranscriptPath is the local path to the transcript file.
	// YouTube: from metadata builder; Stock: empty.
	TranscriptPath string `json:"transcript_path,omitempty"`

	// EmbeddingText is the structured text block fed to the embedding model.
	// YouTube: from metadata builder; Stock: empty.
	EmbeddingText string `json:"embedding_text,omitempty"`

	// ── Extra metadata ─────────────────────────────────────────────────

	// Slug is the explicit operator-supplied Drive folder slug.
	// YouTube: empty; Stock: ChunkState.Slug.
	Slug string `json:"slug,omitempty"`

	// Subfolder is the run-level subfolder override.
	// YouTube: empty; Stock: RunInput.Subfolder.
	Subfolder string `json:"subfolder,omitempty"`
}

// AsSearchTextInput converts this ClipSemanticMetadata into a
// SearchTextInput suitable for the SearchTextComposer registry.
//
// The `source` parameter selects the strategy (e.g. "youtube", "stock").
// Stock-specific fields (Event, Round, Subject, StartSec, EndSec) are
// placed in the Additional map so the stockSearchTextStrategy can read
// them. YouTube-specific fields (Hook, Speakers, MentionedPeople, Topics,
// Summary) are placed in the typed SearchTextInput fields.
//
// godlike/06 SSOT: this method is the SOLE canonical bridge from
// ClipSemanticMetadata to SearchTextInput. Callers MUST NOT manually
// construct SearchTextInput from ClipSemanticMetadata fields.
func (m *ClipSemanticMetadata) AsSearchTextInput(source string) SearchTextInput {
	if source == "" {
		// Provenance is supplied by the registry-backed caller. An opaque
		// AssetID is never parsed as a source discriminator.
		source = ""
	}

	sti := SearchTextInput{
		AssetID:         m.AssetID,
		Source:          source,
		Title:           m.Title,
		Description:     m.Description,
		Summary:         m.Summary,
		Tags:            append([]string(nil), m.Tags...), // defensive copy
		Category:        m.Category,
		SourceURL:       m.SourceURL,
		Hook:            m.Hook,
		Speakers:        append([]string(nil), m.Speakers...),
		MentionedPeople: append([]string(nil), m.MentionedPeople...),
		Topics:          append([]string(nil), m.Topics...),
		// Channel: intentionally NOT set here. NormalizedGroup (folder routing)
		// is semantically different from Channel (YouTube channel name).
		// Adapters that populate AsSearchTextInput should set Channel separately.
		Transcript:       "", // not available at this layer; callers wire it
		DetectedEntities: append([]string(nil), m.Entities...),
	}

	// Stock-specific fields go into Additional for the
	// stockSearchTextStrategy to read.
	additional := make(map[string]string)
	if m.Event != "" {
		additional["event"] = m.Event
	}
	if m.Round > 0 {
		additional["round"] = strconv.Itoa(m.Round)
	}
	if m.Subject != "" {
		additional["subject"] = m.Subject
	}
	if m.StartSec > 0 {
		additional["start_sec"] = strconv.FormatFloat(m.StartSec, 'f', -1, 64)
	}
	if m.EndSec > 0 {
		additional["end_sec"] = strconv.FormatFloat(m.EndSec, 'f', -1, 64)
	}
	// Note: source_url is NOT placed in Additional for YouTube because
	// it's already set in the typed SourceURL field above. Stock strategy
	// reads source_url from Additional when needed.
	if source == "stock" && m.SourceURL != "" {
		additional["source_url"] = m.SourceURL
	}
	if len(additional) > 0 {
		sti.Additional = additional
	}

	return sti
}

// Clone returns a deep copy of this ClipSemanticMetadata. Slice fields
// (Tags, Entities, Speakers, MentionedPeople, Topics) are defensively
// copied so mutations on the clone don't affect the original.
//
// godlike/07 minimum-blast-radius: callers that pass ClipSemanticMetadata
// across goroutine boundaries or store it in long-lived caches should
// use Clone() to prevent data races.
func (m *ClipSemanticMetadata) Clone() ClipSemanticMetadata {
	cp := *m // shallow copy (strings are immutable in Go)
	cp.Tags = append([]string(nil), m.Tags...)
	cp.Entities = append([]string(nil), m.Entities...)
	cp.Speakers = append([]string(nil), m.Speakers...)
	cp.MentionedPeople = append([]string(nil), m.MentionedPeople...)
	cp.Topics = append([]string(nil), m.Topics...)
	return cp
}

// IsEmpty returns true when the struct carries no meaningful data.
// Useful for guard checks before persisting or indexing.
func (m *ClipSemanticMetadata) IsEmpty() bool {
	return m.AssetID == "" &&
		m.Title == "" &&
		m.Description == "" &&
		m.ContentHash == ""
}

// ComputeDurationSec populates DurationSec from StartSec and EndSec
// if DurationSec is currently zero. Returns the computed value.
func (m *ClipSemanticMetadata) ComputeDurationSec() float64 {
	if m.DurationSec == 0 && m.EndSec > m.StartSec {
		m.DurationSec = m.EndSec - m.StartSec
	}
	return m.DurationSec
}

// MergedTags returns the union of all tag-like fields, deduplicated
// and trimmed. This is the canonical tag set for search indexing.
// YouTube: Tags (already merged by adapter) + Topics + Speakers +
// MentionedPeople + Entities.
// Stock: Tags (already set by adapter).
func (m *ClipSemanticMetadata) MergedTags() []string {
	seen := make(map[string]bool)
	var out []string
	addAll := func(ss []string) {
		for _, s := range ss {
			s = strings.TrimSpace(s)
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	addAll(m.Tags)
	addAll(m.Topics)
	addAll(m.Speakers)
	addAll(m.MentionedPeople)
	addAll(m.Entities)
	return out
}
