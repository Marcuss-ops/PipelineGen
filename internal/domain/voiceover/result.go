// Package voiceover — result.go defines the canonical voiceover result type.
//
// PR 1 (June 2026): replaces the legacy VoiceoverResult (application/voiceover/types.go)
// which embedded a string Error field and bool OK flag. The canonical result is
// always returned alongside a Go error — the caller checks `err != nil` instead
// of inspecting an OK bool.
package voiceover

// VoiceoverResult is the canonical output of a single voiceover generation.
//
// Every field that the caller may need is present; no map[string]any,
// no interface{}, no ambiguous "status" string. The caller checks the
// returned error to determine success — a non-nil error means the generation
// failed and Result is nil.
type VoiceoverResult struct {
	// ID is the deterministic asset identifier (SHA256 of the command).
	ID string `json:"id"`

	// Voice is the resolved voice code used for TTS (e.g. "en-US-RogerNeural").
	Voice string `json:"voice"`

	// Locale is the normalised BCP-47 language tag.
	Locale string `json:"locale"`

	// Filename is the server-generated filename (e.g. "vo_abc123_en-us.mp3").
	// The client never supplies this; it is computed deterministically.
	Filename string `json:"filename"`

	// LocalPath is the absolute path to the generated .mp3 file on disk.
	LocalPath string `json:"local_path,omitempty"`

	// DriveLink is the Google Drive web-view link (populated when Destination is set).
	DriveLink string `json:"drive_link,omitempty"`

	// DriveFileID is the Google Drive file ID.
	DriveFileID string `json:"drive_file_id,omitempty"`

	// FileHash is the SHA-256 hash of the generated .mp3 content.
	FileHash string `json:"file_hash,omitempty"`

	// TextHash is the SHA-256 of the input text (used for dedup).
	TextHash string `json:"text_hash,omitempty"`

	// Cached reports whether this result was served from cache (no TTS call).
	Cached bool `json:"cached,omitempty"`

	// Reference echoes back the Reference from the command for caller
	// correlation (script_id, scene_id).
	ScriptID string `json:"script_id,omitempty"`
	SceneID  string `json:"scene_id,omitempty"`
}
