// Package delivery defines the canonical Drive publish contract.
//
// Every endpoint and every job that uploads a file to Google Drive MUST
// go through the Publisher interface (defined in a follow-up file in this
// package). The types below describe WHAT a caller wants to publish —
// never WHERE on Drive it should land. The DestinationRegistry (also in
// this package) maps a DestinationKey to a root folder and path-builder;
// the concrete Publisher in internal/infrastructure/drive/publisher.go
// resolves the full Drive folder hierarchy and performs the upload.
//
// Architecture rule (June 2026):
//
//	An endpoint or job never chooses a folder ID and never builds a
//	Drive folder hierarchy. It declares only a DestinationKey and the
//	asset's logical metadata. The DestinationRegistry is the sole
//	authority for root and structure; the Publisher is the sole
//	authority for folder creation and file upload.
package delivery

// DestinationKey identifies a canonical Drive destination. Each key maps
// to exactly one DestinationPolicy in the DestinationRegistry. Adding a
// new capability means adding one constant here and one policy entry —
// no endpoint-level Drive logic is permitted.
type DestinationKey string

const (
	DestinationYouTubeClip  DestinationKey = "youtube_clip"
	DestinationArtlist      DestinationKey = "artlist"
	DestinationStock        DestinationKey = "stock"
	DestinationImage        DestinationKey = "image"
	DestinationVoiceover    DestinationKey = "voiceover"
	DestinationBook         DestinationKey = "book"
	DestinationScript       DestinationKey = "script"
	DestinationSoundEffect  DestinationKey = "sound_effect"
)

// ConflictPolicy controls what happens when a file with the same name
// already exists in the target Drive folder.
type ConflictPolicy int

const (
	// ConflictOverwrite updates the existing file in place (default).
	// This matches the legacy behaviour of drive.Uploader.UploadFile.
	ConflictOverwrite ConflictPolicy = iota

	// ConflictSkip returns the existing file's metadata without uploading.
	ConflictSkip

	// ConflictRename appends a timestamp or suffix to avoid collision.
	ConflictRename
)

// PublishRequest describes WHAT to publish, not WHERE it lands on Drive.
// The caller provides only the destination kind and the asset's logical
// metadata. The Publisher resolves the actual folder path.
type PublishRequest struct {
	// Destination is the canonical Drive destination key.
	Destination DestinationKey `json:"destination"`

	// LocalPath is the absolute path to the file on the local filesystem.
	LocalPath string `json:"local_path"`

	// Filename is the desired name on Drive (e.g. "clip_abc123.mp4").
	Filename string `json:"filename"`

	// Description is an optional description visible in the Drive UI.
	Description string `json:"description,omitempty"`

	// AssetID is the canonical asset identifier (e.g. media_assets.id).
	AssetID string `json:"asset_id,omitempty"`

	// ProjectID groups related assets (e.g. a book processing run).
	ProjectID string `json:"project_id,omitempty"`

	// Group is a logical grouping key (e.g. YouTube channel name,
	// artlist search term). Used by PathBuilder to construct the
	// folder hierarchy.
	Group string `json:"group,omitempty"`

	// Subject identifies the subject within the group (e.g. YouTube
	// video ID, artlist asset ID). Used by PathBuilder.
	Subject string `json:"subject,omitempty"`

	// Style is an optional style tag (e.g. image generation style).
	Style string `json:"style,omitempty"`

	// Language is an optional BCP-47 language tag.
	Language string `json:"language,omitempty"`

	// ConflictPolicy controls duplicate-file behaviour. Zero-value
	// (ConflictOverwrite) matches legacy behaviour.
	ConflictPolicy ConflictPolicy `json:"conflict_policy,omitempty"`
}

// PublishResult is returned after a successful publish.
type PublishResult struct {
	// FileID is the Google Drive file ID of the uploaded file.
	FileID string `json:"file_id"`

	// WebViewLink is the human-readable Drive URL.
	WebViewLink string `json:"web_view_link,omitempty"`

	// FolderID is the resolved Drive folder the file was uploaded into.
	FolderID string `json:"folder_id"`

	// Destination echoes back the requested DestinationKey.
	Destination DestinationKey `json:"destination"`

	// PathSegments are the resolved folder path segments (e.g.
	// ["clips", "NBA News", "abc123"]). Useful for auditing and
	// asset-tree upsert.
	PathSegments []string `json:"path_segments,omitempty"`
}
