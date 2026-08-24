// Package search — document.go holds the canonical typed envelope that
// bridges the three search territories (SemanticEnrichment,
// IndexProjection, MediaSearch) and the hydration shape (MediaAsset)
// (PR-SEARCH-PORTS-SPLIT, 2026-07-04).
//
// Pre-split, SearchDocument + AsPayloadMap + MediaAsset lived in the
// historical 674-LoC ports.go god file alongside the SearchBackend
// interface, BackendRegistry struct, 8 sentinels, 2 ports, and the
// Logger type. The split surfaces it as a single-purpose capability
// file per AGENTS.md Pattern 5.
//
// What lives here:
//   - SearchDocument + AsPayloadMap (Qdrant IndexSchema payload envelope)
//   - MediaAsset (SQLite hydration shape, declared in Commit 3-A)
//
// Shape rationale (godlike/06 "one owner per fact" + the QDRANT-001
// locator-leak rule): the struct is the SSOT for the Qdrant IndexSchema
// payload — every field except QdrantPointID is mirrored 1:1 to a
// payload key by internal/platform/qdrant/payload_mapper.go.
// No server-internal locator (LocalPath, DriveLink, DriveFileID,
// InternalRootURL, FileSystemPath, raw collection/vector names) is
// allowed in this struct; a locator leak here would flow through every
// downstream surface and break the QDRANT-004 acceptance criterion
// ("Nessun path locale o secret esposto").
//
// Producers (SemanticEnrichment territory) MUST populate all payload
// fields they expect to be filterable on; consumers (IndexProjection +
// MediaSearch) MUST treat missing fields as zero-value gracefully.
// SchemaVersion tracks forward-compatible evolution of the payload —
// readers reject unknown versions.
package assets

// SearchDocument is the canonical typed envelope that bridges the three
// search territories (SemanticEnrichment, IndexProjection, MediaSearch).
//
// See package doc for the shape rationale (godlike/06 + QDRANT-001
// locator-leak rule).
type SearchDocument struct {
	// SchemaVersion is the version of this document contract. Currently
	// always 1. Bumped if a structural field is added.
	SchemaVersion int

	// AssetID is the canonical asset identifier (UUID). Mirrors the
	// media_assets.id column.
	AssetID string

	// QdrantPointID is the per-asset Qdrant point identifier (set by
	// the IndexProjection territory after a successful Upsert). It is
	// the read-side correlation key for retrievals + cleanups; absent
	// from freshly-produced SearchDocuments (no Qdrant write yet).
	QdrantPointID string

	// Payload fields (every key mirrors the Qdrant IndexSchema's
	// payload) — see infra/qdrant/schema/schema.go for the canonical
	// names. All fields are json-stable strings / string-lists so the
	// payload_mapper conversion is lossless.
	Source         string   // "youtube" | "artlist" | "local" | ...
	Name           string   // human-readable asset name
	Category       string   // taxonomy category slug
	MediaType      string   // "video" | "image" | "audio"
	Style          string   // visual style (optional)
	Language       string   // BCP-47 (optional, drive by content)
	YouTubeVideoID string   // canonical YouTube video identifier (optional)
	YouTubeURL     string   // canonical YouTube web URL (optional)
	StartTime      string   // HH:MM:SS(.mmm) — for clip-style assets
	EndTime        string   // HH:MM:SS(.mmm) — for clip-style assets
	Tags           []string // free-form tags (lowercased, deduped)
	SearchText     string   // semantic-search text (title+summary+topics)
}

// AsPayloadMap flattens a SearchDocument into the canonical Qdrant
// payload map. Mirrors the field-to-key contract — same string for
// each name as in infra/qdrant/schema/schema.go (canonical truth) and
// infra/qdrant/payload_mapper.go (read-side). The SchemaVersion is
// NOT included (Qdrant does not version its payload; version is
// tracked separately in infra indexer logs).
//
// Use this from IndexProjection producers (artlist/semantic_enricher,
// images/metadata_service) so the write surface and read surface
// agree byte-for-byte. The MediaSearch side reads payload via the
// infra qdrant/payload_mapper.go's read path — never via this
// function (the direction is wrong for retrieval).
func (d SearchDocument) AsPayloadMap() map[string]any {
	out := map[string]any{
		"asset_id": d.AssetID,
	}
	if d.Source != "" {
		out["source"] = d.Source
	}
	if d.Name != "" {
		out["name"] = d.Name
	}
	if d.Category != "" {
		out["category"] = d.Category
	}
	if d.MediaType != "" {
		out["media_type"] = d.MediaType
	}
	if d.Style != "" {
		out["style"] = d.Style
	}
	if d.Language != "" {
		out["language"] = d.Language
	}
	if d.YouTubeVideoID != "" {
		out["youtube_video_id"] = d.YouTubeVideoID
	}
	if d.YouTubeURL != "" {
		out["youtube_url"] = d.YouTubeURL
	}
	if d.StartTime != "" {
		out["start_time"] = d.StartTime
	}
	if d.EndTime != "" {
		out["end_time"] = d.EndTime
	}
	if len(d.Tags) > 0 {
		out["tags"] = d.Tags
	}
	if d.SearchText != "" {
		out["search_text"] = d.SearchText
	}
	return out
}

// MediaAsset is the canonical typed envelope for SQLite hydration.
// Shape mirrors the legacy `mediasearch.MediaAsset` 1:1
// (gofmt-stable, JSON-tag-stable). Server-internal locators
// (DriveLink, LocalPath) are guarded by `json:"-"` tags so they
// never leak through default serialisation; the search result
// mapper explicitly copies DriveLink to searchResultItem.DriveLink
// per PR-SEARCH-DRIVELINK (July 2026).
//
// LifecycleState is `json:"-"` because clients have no business
// knowing internal lifecycle semantics; if a row reaches a Candidate
// it is by definition searchable. The post-query guard layers
// defence-in-depth on top of the SQL allowStates filter.
type MediaAsset struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Source         string   `json:"source"`
	MediaType      string   `json:"media_type"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags,omitempty"`
	Language       string   `json:"language,omitempty"`
	DurationMs     int      `json:"duration_ms,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	DriveLink      string   `json:"-"` // PR-SEARCH-DRIVELINK: in-memory enrichment only
	LifecycleState string   `json:"-"`
}
