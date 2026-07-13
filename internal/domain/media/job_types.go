package media

// Canonical media job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeBulkUploadYouTubeClips is the canonical job type for
	// bulk uploading a list of YouTube clips to the operator's
	// Google Drive + upserting the corresponding media_assets rows.
	//
	// Wire-string: "media.bulk_upload_youtube_clips". The owner-side
	// identifier (clips.JobBulkUpload) is a typed alias to this
	// canonical; renaming the identifier does NOT change the wire
	// value so in-flight jobs / orchestration records continue to
	// dispatch post cutover.
	TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"
)
