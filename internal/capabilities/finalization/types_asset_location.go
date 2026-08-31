// types/types_asset_location.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// AssetLocation is the canonical descriptor for where a published
// artifact physically lives. Every PublishedArtifact carries exactly
// one AssetLocation.
type AssetLocation struct {
	// Provider identifies the storage backend (e.g. "drive", "s3").
	Provider string `json:"provider"`

	// FileID is the provider-specific file identifier.
	// For Drive: the Google Drive file ID.
	FileID string `json:"file_id"`

	// WebViewLink is the human-readable URL to view the file.
	WebViewLink string `json:"web_view_link,omitempty"`

	// DownloadLink is the direct download URL for the file.
	// Consumers MUST read this from the canonical location — never
	// reconstruct via string interpolation.
	DownloadLink string `json:"download_link,omitempty"`

	// Checksum is the provider-returned checksum (typically MD5 for
	// Drive). Distinct from the artifact's content SHA-256.
	Checksum string `json:"checksum,omitempty"`

	// FolderID is the resolved folder identifier on the provider.
	FolderID string `json:"folder_id,omitempty"`

	// FolderPath is the human-readable folder path.
	FolderPath string `json:"folder_path,omitempty"`

	// Action is what the publisher actually did.
	Action PublishAction `json:"action,omitempty"`
}
