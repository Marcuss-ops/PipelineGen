package render

import "time"

// PublishedLocation records where an artifact has been delivered.
// An artifact can have multiple published locations, such as local disk
// and Google Drive.
type PublishedLocation struct {
	// ArtifactID is the canonical artifact ID this location belongs to.
	ArtifactID string `json:"artifact_id"`

	// Kind identifies the storage backend category.
	Kind LocationKind `json:"kind"`

	// URI is the canonical address for this location.
	URI string `json:"uri"`

	// ExternalID is the provider-specific identifier.
	ExternalID string `json:"external_id,omitempty"`

	// PublishedAt is the UTC timestamp of successful publication.
	PublishedAt time.Time `json:"published_at"`
}
