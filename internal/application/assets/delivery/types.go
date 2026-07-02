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
	DestinationDocument     DestinationKey = "document"
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

	// RootFolderOverride, when non-empty, overrides the root folder ID
	// that the DestinationRegistry would normally resolve for this
	// destination. This is a backward-compat escape hatch for callers
	// that historically passed an explicit FolderID (e.g. script
	// generation jobs that target a specific Drive folder). New code
	// SHOULD NOT set this field — let the registry decide.
	//
	// Deprecated: will be removed once all callers migrate to
	// DestinationKey-only routing (FASE 9 cleanup).
	RootFolderOverride string `json:"root_folder_override,omitempty"`
}

// PublishAction describes what the publisher actually did on Drive.
// It mirrors delivery.ConflictPolicy (declared further up in this
// file) by exposing the concrete outcome of the publish so callers
// can branch on it: route a "skipped" asset to the dedupe ledger,
// convert a "renamed" asset into a sibling row, treat "updated" as
// a no-op for downstream events, and surface "created" as the
// canonical fresh-asset path.
//
// Zero value is "" to keep zero-value PublishResults (e.g. stub
// fakes in tests, or legacy sites that still construct the value
// manually before downstream consumers move to the canonical
// Publisher) indistinguishable from an actual unknown action — any
// consumer that branches on Action MUST default the empty branch
// to a conservative no-op rather than treat it as "created".
type PublishAction string

const (
	// PublishActionUnknown is the typed zero value. It signals that
	// the publisher could not determine the outcome — usually because
	// a pre-P0-#1 adapter (no PutFile on the FileUploaderPort)
	// produced a PublishResult. Post-P0-#9 callers that branch on
	// Action MUST default the empty branch to a conservative no-op
	// rather than treat it as PublishActionCreated.
	PublishActionUnknown PublishAction = ""

	PublishActionCreated PublishAction = "created"
	PublishActionUpdated PublishAction = "updated"
	PublishActionSkipped PublishAction = "skipped"
	PublishActionRenamed PublishAction = "renamed"
)

// PublishResult is returned after a successful publish.
//
// P0 #9 (June 2026): the publisher used to drop DownloadLink,
// MD5Checksum, Action, and FolderPath from the PutFileResult. Callers
// were forced to reconstruct the download URL via string interpolation
// ("https://drive.google.com/uc?id=...") and to re-issue FindFileByName
// to recover the hash — both fragile (link drift, network race on
// the lookup). The struct now carries all four fields so no caller
// ever has to reconstruct them.
type PublishResult struct {
	// FileID is the Google Drive file ID of the uploaded file.
	FileID string `json:"file_id"`

	// WebViewLink is the human-readable Drive URL.
	WebViewLink string `json:"web_view_link,omitempty"`

	// DownloadLink is the direct download URL surfaced by Drive.
	//
	// This field is the canonical source for the download URL. Any
	// caller that needs the download link MUST read it from here —
	// never reconstruct via "https://drive.google.com/uc?id=" + FileID.
	// Reconstructing risks formatting drift (e.g. the "?export=download"
	// variant used by jobs/assets/service.go) and produces different URLs
	// for the same underlying FileID depending on call site.
	//
	// Empty when the publish was a no-op (e.g. ConflictSkip on a
	// not-yet-existing file) AND Drive did not return a transactional
	// download URL — callers should treat empty as "no link available"
	// and skip Drive-side cleanup or projection in that branch.
	DownloadLink string `json:"download_link,omitempty"`

	// MD5Checksum is the Drive-returned md5Checksum for the uploaded
	// file. Present on every successful PutFile (PutActionCreated,
	// PutActionUpdated, PutActionSkipped, PutActionRenamed) because
	// Drive returns the check-sum on both Create and Update responses
	// AND on the existing-match metadata returned by the skip branch.
	//
	// Empty when Drive did not surface the checksum (rare; the
	// Publisher logs but does not fail when this happens). Callers
	// should treat empty as "checksum not yet known" and either
	// compute the local MD5 separately or skip a checksum-pin step.
	MD5Checksum string `json:"md5_checksum,omitempty"`

	// FolderID is the resolved Drive folder the file was uploaded into.
	FolderID string `json:"folder_id"`

	// FolderPath is the slash-joined human-readable form of
	// PathSegments (e.g. "NBA News/abc123"). Useful for audit logs,
	// display layers, and asset-tree unfurls that need a single string.
	//
	// Empty when PathSegments is empty (the same condition under which
	// ResolveFolder returns the root FolderID unchanged). Coexists
	// with PathSegments: callers that need the structured form use
	// PathSegments; callers that need a single string use FolderPath.
	// PathSegments is authoritative (zero or one ordering; no
	// round-trip normalization); FolderPath is a derived view.
	FolderPath string `json:"folder_path,omitempty"`

	// Destination echoes back the requested DestinationKey.
	Destination DestinationKey `json:"destination"`

	// PathSegments are the resolved folder path segments (e.g.
	// ["clips", "NBA News", "abc123"]). Useful for auditing and
	// asset-tree upsert.
	PathSegments []string `json:"path_segments,omitempty"`

	// Action is what the publisher actually did on Drive. See
	// PublishAction constants for the canonical outcomes.
	//
	// Empty when the publisher was a pre-P0-#1 adapter that could
	// not determine the outcome. New code (post-P0-#9) MUST populate
	// Action via TranslatePutAction so consumers can branch deterministically.
	Action PublishAction `json:"action,omitempty"`
}
