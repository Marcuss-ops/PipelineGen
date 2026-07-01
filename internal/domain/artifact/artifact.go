// Package artifact defines the canonical domain types for artifacts
// and their published locations (Fase 0 della Spina Dorsale, July 2026).
//
// An artifact is the abstract product of a pipeline step: a file, a
// document, an audio clip, an image, a rendered video. Artifacts are
// registered in the asset catalog and then published to one or more
// locations (Drive, local disk, object storage) by the delivery service.
//
// Separation of concerns:
//   - Domain services (voiceover, images, books) produce an Artifact.
//   - AssetCatalog registers the Artifact.
//   - DeliveryService publishes the Artifact to a PublishedLocation.
//   - AssetCatalog links the PublishedLocation back to the Artifact.
//
// No domain service should call Drive directly — they go through
// delivery.Publisher. The Artifact type is the contract between
// production and delivery.
//
// Canonical reference: Piano d'Azione § Fase 3.
package artifact

import "time"

// ── LocationKind ─────────────────────────────────────────────────────

// LocationKind categorises where a published artifact physically lives.
type LocationKind string

const (
	// LocationKindLocal is a file on the worker's local filesystem.
	LocationKindLocal LocationKind = "local"

	// LocationKindDrive is a file on Google Drive.
	LocationKindDrive LocationKind = "drive"

	// LocationKindObjectStorage is a file on S3-compatible object storage.
	LocationKindObjectStorage LocationKind = "object_storage"
)

// ── Artifact ─────────────────────────────────────────────────────────

// Artifact is the canonical descriptor of a produced asset, before
// it is published to any specific location. Every pipeline step that
// produces a file or document emits an Artifact.
//
// Artifacts are registered in the asset catalog once (RegisterArtifact),
// then linked to PublishedLocations as they are delivered.
type Artifact struct {
	// ID is the unique canonical identifier for this artifact.
	// Typically derived from a content hash (SHA-256) or a
	// deterministic ID (e.g. voiceover command ID).
	ID string `json:"id"`

	// Kind is the high-level category of the artifact.
	// Examples: "voiceover", "image", "document", "video", "script".
	Kind string `json:"kind"`

	// Checksum is the SHA-256 hex digest of the artifact content.
	Checksum string `json:"checksum"`

	// Size is the artifact size in bytes.
	Size int64 `json:"size"`

	// MimeType is the IANA media type (e.g. "audio/mpeg", "image/png").
	MimeType string `json:"mime_type"`

	// CreatedAt is the UTC timestamp of artifact creation.
	CreatedAt time.Time `json:"created_at"`
}

// ── PublishedLocation ────────────────────────────────────────────────

// PublishedLocation is the record of where an artifact has been
// delivered. One Artifact can have multiple PublishedLocations
// (e.g. a voiceover file on local disk AND on Drive).
type PublishedLocation struct {
	// ArtifactID is the canonical Artifact ID this location belongs to.
	ArtifactID string `json:"artifact_id"`

	// Kind is the storage backend category.
	Kind LocationKind `json:"kind"`

	// URI is the canonical address for this location.
	//   - local: absolute filesystem path
	//   - drive: Drive file ID
	//   - object_storage: s3://bucket/key
	URI string `json:"uri"`

	// ExternalID is the provider-specific identifier.
	//   - drive: Google Drive file ID
	//   - object_storage: object key
	ExternalID string `json:"external_id,omitempty"`

	// PublishedAt is the UTC timestamp of successful publication.
	PublishedAt time.Time `json:"published_at"`
}
