// Package qdrant — index_document.go defines the canonical wire shape
// that crosses the Writer↔Qdrant boundary.
//
// PR 6 (June 2026, refactor/qdrant-index-document) — verdict section
// #11 (embedding provenance) + partial footer of #7 (the Qdrant
// writer boundary must not carry server-internal locators).
//
// Invariants enforced by this file:
//
//  1. IndexDocument is the ONLY struct the Mapper emits to the Writer
//     AND the ONLY struct the Writer turns into a Qdrant payload. The
//     SQL-fetch shape (AssetData, internal/infrastructure/qdrant/
//     index_writer.go) keeps its diagnostic fields (Status,
//     DriveLink, LocalPath, 4 raw []float32 slices) but the Mapper
//     airlock EXCLUDES them when it builds the IndexDocument.
//
//  2. EmbeddingArtifact carries the OBSERVED provenance. The payload
//     writes `embedding_version_<channel>` from the artifact's
//     ModelVersion, NOT from `schema.DenseVectors[].ModelVersion`.
//     The schema declares the EXPECTED version; the artifact records
//     what the writer actually emitted. Mismatches are surfaced via
//     the per-channel counters in schema.SwitchReport.VersionMismatchPerChannel
//     (PR 12) — and now, going forward, the on-disk payload matches
//     the OBSERVED version, not the schema's hypothetical one.
//
//  3. Sparse channels (e.g. bm25_text) carry Values=nil — Qdrant
//     infers the sparse vector server-side via `qm25_text: {text:
//     <doc.SearchText>, model: <schema.DefaultSparseModel>}` on the wire.
//     The Mapper emits that wire shape in IndexDocumentToPoint;
//     EmbeddingArtifact just records the model name + observed
//     version so the per-channel provenance counter is uniform.
//
//  4. LifecycleState carries the canonical domain asset.LifecycleState
//     value; the Mapper converts from media_assets.lifecycle_state
//     (TEXT) and falls back to domain.AssetLifecycleActive when the
//     column is empty (defence-in-depth against the legacy `status`
//     column that migration 101 retired).
//
// FORBIDDEN at this boundary (enforced by the freeze test in
// internal/app/composition_test.go::TestComposition_Frozen
// QdrantIndexDocumentForbiddenFields):
//
//   - Status
//   - DriveLink
//   - LocalPath
//
// These stay on the SQL-fetch `asset.AssetData` DTO for diagnostic
// paths (cmd/admin/*, asset ingest); they are NOT promoted to the
// Qdrant payload. A future reader discovering these fields on
// IndexDocument is looking at the wrong struct.
package indexing

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// VectorChannel names a per-channel vector handle in the canonical
// pipeline. Used as both the EmbeddingArtifact.Channel discriminator
// AND the payload-side `embedding_version_<channel>` key suffix.
//
// Pre-existing constants from payload_mapper.go (text / transcript /
// visual / audio / bm25_text) map 1:1 to this type. The constants
// below are the canonical SSOT; payload_mapper.go compares against
// the strings at the boundary so the test suite can pin the channel
// names without a deep import dance.
type VectorChannel string

const (
	// ChannelText is the default dense channel for textual semantic.
	ChannelText VectorChannel = "text"

	// ChannelTranscript is the dense channel for Whisper-derived
	// transcripts. Distinct from text so a YouTube clip can ship
	// both. Falls back to "drop" (not text) when TranscriptVector
	// is absent — PR 2 (verdetto §10) eliminated the synthetic
	// fallback to TextVector.
	ChannelTranscript VectorChannel = "transcript"

	// ChannelVisual is the dense channel for image embeddings
	// (SigLIP so400m patch14-384 in v3).
	ChannelVisual VectorChannel = "visual"

	// ChannelAudio is the dense channel for audio embeddings
	// (CLAP-HTSAT in v3). Optional — dropped when AudioVector is
	// absent.
	ChannelAudio VectorChannel = "audio"

	// ChannelBM25Text is the sparse channel for lexical BM25. The
	// wire shape is `vectors.bm25_text = {text: <doc.SearchText>,
	// model: "qdrant/bm25"}`; Qdrant projects the text on-server.
	ChannelBM25Text VectorChannel = "bm25_text"
)

// IndexedMetadata is the structured metadata block that ships in the
// Qdrant payload alongside the vectors. Drives payload-index creation
// (schema.DefaultV3Schema().PayloadIndexes) and hydration on read-back.
//
// Server-internal locators (DriveLink, LocalPath) are NOT in this
// struct. See the package-level doctrine above.
type IndexedMetadata struct {
	// Summary is the clip summary / run summary extracted from the
	// metadata_json bag (canonical key: summary / clip_summary).
	Summary string

	// Name is the human-readable payload name required by the
	// verifier and search adapters. Title + Description are extracted
	// from AssetData.MetadataJSON
	// via BuildPayload's parseMetadataJSON helper (the legacy
	// popper, unchanged in shape).
	Name        string
	Title       string
	Description string

	// Tags is the canonical tag list. Currently sourced from
	// media_assets.tags (TEXT JSON array); see payload_mapper.go for
	// the decode path. Empty slice → no payload tag key (so the
	// payload-index flag isn't created on a no-tag asset).
	Tags []string

	// Source / MediaType / Language / Category / Style / License /
	// IndexVersion are direct columns from media_assets. Each is
	// gated by an `if != ""` check in BuildPayload so legacy rows
	// with NULL columns don't emit empty payload keys.
	Source         string
	MediaType      string
	Language       string
	Category       string
	Style          string
	License        string
	IndexVersion   string
	SourceProvider string
	Origin         string
	Destination    string

	// DurationMs is the int64 ms duration for video/audio assets.
	// Zero → no payload key (the int zero is not a legitimate
	// duration for indexed assets anyway).
	DurationMs  int64
	DurationSec int

	// SourceURL is the canonical source URL for the asset. For the
	// stock/timestamp workflow this is the original video URL.
	SourceURL string

	// YouTubeID + YouTubeURL are the canonical YT clip references.
	// Non-YouTube assets leave these empty; the payload-key guards
	// keep the keys absent.
	YouTubeID     string
	YouTubeURL    string
	SourceVideoID string

	// SemanticTitle and EmbeddingText are the search-facing text
	// fields. SemanticTitle is a compact human label; EmbeddingText is
	// the richer text block used to build the dense embedding input.
	SemanticTitle string
	EmbeddingText string

	// Event / Round / Scene / Subject make the workflow and content
	// semantics filterable in Qdrant. All are optional.
	Event   string
	Round   int
	Scene   string
	Subject string

	// ContextSubject is the canonical LLM-derived secondary
	// subject (e.g. "Manny Pacquiao" when Subject is "Adrien
	// Broner"). Distinct from Subject — Subject is the
	// primary-actor descriptor, ContextSubject is the
	// secondary-actor / counter-party. Empty until RLM pass;
	// omitempty guard in payload_builder.go keeps the payload
	// key absent.
	ContextSubject string

	// Tags / entity bags used by the embedding text builder and
	// payload filters.
	Topics           []string
	Speakers         []string
	MentionedPeople  []string
	People           []string
	SourceTags       []string
	ClipTags         []string
	SearchKeywords   []string
	Entities         []string
	Hook             string
	SearchVisibility string

	// Workflow/projection identifiers used to reconstruct the publish
	// run and to filter chunks by their source job.
	JobID          string
	WorkflowID     string
	RunFingerprint string
	ChunkIndex     int
	TotalChunks    int
	PolicyVersion  string
	DrivePath      string
	IndexingStatus string

	// TimestampDriveFolderLink / TimestampFolderID carry the parent
	// timestamp Drive folder metadata. Per-run scalar (all chunks in
	// the same timestamp block share the same parent folder). Propagated
	// from ChunkState → ChunkMetadataEntry → metadata.json → Qdrant
	// payload for "open in Drive" navigation from search results.
	TimestampDriveFolderLink string
	TimestampFolderID        string

	// StartTime / EndTime are the HH:MM:SS.ms timeline anchors
	// (YouTube clip excerpts). Empty when the asset is not a clip.
	StartTime string
	EndTime   string

	// YouTubeChan is the channel the source video came from.
	// Stored under payload key `channel_id` (verbatim from the
	// legacy discriminator — see schema.DefaultV3Schema.PayloadIndexes).
	YouTubeChan string

	// CreatedAt / UpdatedAt / DeletedAt are ISO-8601 strings sourced
	// from media_assets.{ts} columns. Qdrant stores them as
	// `datetime`-indexed payload fields.
	CreatedAt string
	UpdatedAt string
	DeletedAt string
}

// EmbeddingArtifact is the OBSERVED provenance record for a single
// vector channel on a single write. This is what the writer actually
// emitted to Qdrant; the schema's schema.EmbeddingSpec.ModelVersion is what
// was EXPECTED. The two may differ when an asset was indexed against
// a stale schema (PR 9 / 12 reindex-verifier gate).
//
// EmbeddingArtifact is constructed in the Mapper boundary; today the
// mapper defaults Model/ModelVersion/PreprocessVer/Dimensions to the
// schema values for legacy rows (write-only provenance). A future PR
// can swap the source to media_assets.metadata_json.$.embeddings
// once that column-shape lands in a migration.
type EmbeddingArtifact struct {
	// Channel discriminates the artifact by VectorChannel. The
	// payload key `embedding_version_<channel>` is derived from
	// this string; the writer MUST populate one artifact per
	// schema.EmbeddingSpec in the schema.
	Channel VectorChannel

	// Values is the raw dense vector. nil for sparse channels — the
	// wire shape for sparse is `{text, model}` and the values are
	// inferred by Qdrant on read.
	Values []float32

	// Model is the embedding model name embedded into Values
	// (e.g. "multilingual-e5-base", "siglip-so400m-patch14-384",
	// "clap-htsat-fused", "qdrant/bm25"). Defaults to the schema
	// spec's Model for write-only provenance.
	Model string

	// ModelVersion is the OBSERVED model version (write-only today;
	// defaults to the schema's spec.ModelVersion for legacy rows).
	// PR 6 (verdetto §11): THIS is the value written to
	// payload.embedding_version_<channel>, not the schema's.
	ModelVersion string

	// PreprocessVer identifies the text preprocessing pipeline that
	// produced Values. Empty for visual/audio channels (no text
	// preprocessing). Defaults to the schema's spec.PreprocessVer.
	PreprocessVer string

	// Dimensions is the vector length. Matches len(Values) for dense
	// channels. -1 for sparse (Qdrant-managed). Defaults to
	// spec.Dimensions.
	Dimensions int

	// ContentHash is a short hex digest over the Values bytes
	// (truncated SHA-256, 16 hex chars). Empty today; reserved for a
	// future PR that wires SHA-256 over the canonical
	// canonicalise-and-normalise pipeline. The freeze test does NOT
	// assert non-empty (the field is optional).
	ContentHash string

	// GeneratedAt is when the artifact was recorded. The Mapper
	// stamps this at construction time (today) — a future PR can
	// read it back from media_assets.metadata_json and surface
	// provenance drift via the per-channel verifier counter.
	GeneratedAt time.Time
}

// IndexDocument is the canonical Writer↔Qdrant wire shape. The
// Mapper constructs this from the SQL-fetch AssetData and strips
// every forbidden field at the airlock. The Writer turns it into a
// schema.Point via IndexDocumentToPoint (see payload_mapper.go).
//
// FORBIDDEN fields (Status, DriveLink, LocalPath) are kept off this
// struct by design. See package-level doctrine. A future PR adding
// them here without updating the freeze test is a regression —
// grep `IndexDocument struct` and verify no forbidden field name
// appears between the braces.
type IndexDocument struct {
	// AssetID is the canonical media_assets.id. The Writer calls
	// schema.AssetIDToQdrantPointID at the very last step before the
	// wire json — the IndexDocument carries the raw string so
	// downstream observability logs read the canonical identifier.
	AssetID string

	// WorkspaceID is the multi-tenancy discriminator (PR 5,
	// fix/qdrant-tenant-scope). Empty when the Qdrant-enabled
	// deployment opts out of tenant isolation (single-tenant dev
	// mode). The Mapper's BuildPayloadFromDocument emits
	// payload.workspace_id only when non-empty.
	WorkspaceID string

	// LifecycleState is the canonical allow-list-keyed lifecycle
	// (ACTIVE | DELETED | RETIRED | ...). Source:
	// media_assets.lifecycle_state (UPPERCASE), fallback to
	// domain.AssetLifecycleActive on empty.
	LifecycleState asset.LifecycleState

	// SourceVersion is the ingest-time version string used by the
	// outbox supersede gate (port: jobsoutbox.SourceVersionQuerier).
	// Empty for legacy rows.
	SourceVersion string

	// ContentHash is the content-addressable digest (currently
	// media_assets.content_hash via JSON projection). Used by the
	// verifier to detect drift on reindex.
	ContentHash string

	// SearchText is the canonical search-text source for the bm25
	// sparse channel. Populated by the script-generation pipeline
	// (`scenes[*].description` joined); empty for assets that have
	// no script.
	SearchText string

	// Metadata is the structured payload metadata (see IndexedMetadata
	// doc above).
	Metadata IndexedMetadata

	// Embeddings is the per-channel provenance map. The Writer
	// validates dense channel Values == artifact.Dimensions at the
	// boundary (existing PR 3 fail-closed invariant). Sparse
	// channels: Values == nil.
	Embeddings map[VectorChannel]EmbeddingArtifact
}

// ForbiddenIndexDocumentFields is the SSOT for the freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocument
// ForbiddenFields. A future addition to IndexDocument must NOT add
// any of these field names without updating the freeze test.
//
// Hidden (unexported) because it's a test-only fixture; callers
// outside this package should never reason about which fields are
// forbidden — they should reason about the wire shape (no
// drive_link / no local_path / no status payload keys).
var ForbiddenIndexDocumentFields = []string{
	"Status",
	"DriveLink",
	"LocalPath",
}
