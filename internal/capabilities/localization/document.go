package localization

// document.go owns LocalizedDocumentEntry — the canonical per-language row
// assembled into the localization Google Doc. It answers "which Drive link
// belongs to which language/scene" with certified facts instead of ad-hoc
// strings.
//
// godlike/06 SSOT (one canonical owner per fact): the Docs writer receives
// []LocalizedDocumentEntry and applies ONE ordering — by Priority — then
// renders. It never learns about Whisper, Rust, FFmpeg, or the translation
// provider; those layers produce the artifact facts that land here.

import "sort"

// LocalizedDocumentEntry is one ordered row in the localization manifest
// Google Doc. Every field is a fact the upstream layers already certified:
// identity (scene/clip), the language + its requested priority, the text
// track that supplied the subtitle, the video asset + its Drive location,
// and the rendered bytes' duration + content hash.
type LocalizedDocumentEntry struct {
	// SceneID is the editorial scene the clip belongs to.
	SceneID string `json:"scene_id"`
	// ClipID is the canonical clip identity that was localized.
	ClipID string `json:"clip_id"`
	// Language is the BCP-47 language this row describes (e.g. "es").
	Language string `json:"language"`
	// Priority is the requested editorial/queue order (source=0, targets
	// 1..N). It is the sole sort key for the doc.
	Priority int `json:"priority"`

	// TextTrackID is the text track (translated subtitle text) that fed the
	// .ass for this language.
	TextTrackID int64 `json:"text_track_id"`

	// VideoAssetID is the canonical derived-asset id of the rendered clip.
	VideoAssetID string `json:"video_asset_id"`
	// DriveFileID is the uploaded Drive file id.
	DriveFileID string `json:"drive_file_id"`
	// DriveLink is the uploaded Drive web-view link.
	DriveLink string `json:"drive_link"`

	// DurationMS is the rendered clip duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// SHA256 is the content hash of the rendered bytes.
	SHA256 string `json:"sha256"`
}

// SortLocalizedDocumentEntries orders entries by Priority ascending — the
// requested editorial order (source=0, targets=1..N), never render completion
// order. The sort is stable so equal priorities keep their input order. This
// is the ONE ordering the Docs writer applies; it is a pure, tested function
// so no worker/queue reordering can silently flip the manifest.
func SortLocalizedDocumentEntries(entries []LocalizedDocumentEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Priority < entries[j].Priority
	})
}
