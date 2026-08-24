// Package outbox — asset_published_envelope.go carries the canonical
// wire-shape contract for asset.published events
// (SEMANTIC-LOCATION-API-2026-07-06 Wave 5, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - AssetPublishedSchemaVersion lives ONLY in this file. The
//     PARALLEL canonical constant outboxevents.SchemaVersionAssetPublished
//     at internal/platform/sqlite/outboxevents/registry.go
//     is the registry's sole owner of the wire-shape string; this
//     local re-export lets the handler's body reference the constant
//     by an ergonomic short name without importing the registry's
//     full public surface. Both constants MUST resolve to the same
//     string literal — any drift surfaces as a build failure during
//     the Compilation contract check.
//   - AssetPublishedRequestV1 lives ONLY in this file. The producer
//     (EnrichmentHandler at
//     internal/capabilities/assets/providers/stock/enrichment/handler.go)
//     and the consumer (AssetPublishedHandler at
//     asset_published_handler.go) MUST both reference this struct
//     for the wire-shape.
//
// Required fields (handler fails-fast with TerminalError on
// missing-or-malformed):
//
//   - schema_version   (literal AssetPublishedSchemaVersion)
//   - event_id         (RFC4122 UUID or producer-chosen opaque token)
//   - asset_id         (canonical media_assets.id)
//   - destination      (delivery.DestinationKey canonical string;
//     "stock", "image", "voiceover", etc.)
//   - idempotency_key  (mirrors event_key for audit)
//
// Optional (used by ComposeSearchText for rich embedding text):
//
//   - origin           ("generated", "retrieved", or "live" —
//     distinguishes where the asset came from)
//   - category         (Boxe / Personaggi / etc.)
//   - subject          (Mike Tyson / abc123 / etc. — same field
//     as PublishRequest.Subject)
//   - provider         (pexels / pixabay / wikipedia / dall-e)
//   - drive_file_id    (canonical Drive file id)
//   - drive_path       (slash-joined human form, e.g.
//     "stock/Boxe/pexels/Mike-Tyson")
//   - content_type     (DoD #9, July 2026: video / image / audio /
//     document — discriminates embedding vectors by media kind)
//   - tags             (canonical tag list)
//   - requested_at     (RFC3339 UTC; logged for audit only)
//
// Producers MUST NOT include embeddings, raw search vectors, or any
// payload that would make the event bloom to MBs. The handler
// composes SearchText locally for audit/debug logging only; it does
// not invoke a publisher or mutate an index. The composition root
// registers asset.index.requested separately as the sole operational
// indexing seam.
package outbox

// AssetPublishedSchemaVersion is the canonical, EXACT string the
// AssetPublishedHandler accepts. Producers MUST send the literal
// schema-version value. Mismatch is TERMINAL.
const AssetPublishedSchemaVersion = "asset.published.v1"

// AssetPublishedRequestV1 is the canonical v1 envelope for
// asset.published events.
type AssetPublishedRequestV1 struct {
	SchemaVersion  string   `json:"schema_version"`
	EventID        string   `json:"event_id"`
	AssetID        string   `json:"asset_id"`
	Destination    string   `json:"destination"`
	Origin         string   `json:"origin,omitempty"`
	Category       string   `json:"category,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	DriveFileID    string   `json:"drive_file_id,omitempty"`
	DrivePath      string   `json:"drive_path,omitempty"`
	ContentType    string   `json:"content_type,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
	SourceVersion  string   `json:"source_version"`
	RequestedAt    string   `json:"requested_at,omitempty"`
}
