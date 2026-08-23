package voiceover

import "errors"

// Canonical sentinel errors for the voiceover use case. Every call site
// uses errors.Is to branch on these — no string matching, no any
// casting.
var (
	// ErrTextRequired is returned when GenerateVoiceoverCommand.Text is empty.
	ErrTextRequired = errors.New("voiceover: text is required")

	// ErrLocaleRequired is returned when GenerateVoiceoverCommand.Locale is empty.
	ErrLocaleRequired = errors.New("voiceover: locale is required")

	// ErrLocaleUnsupported is returned when the locale is not in the voice
	// registry (no default voice mapped).
	ErrLocaleUnsupported = errors.New("voiceover: locale not supported by voice registry")

	// ErrTTSFailed is returned when the TTS provider fails to generate audio.
	// The wrapped error carries provider-specific diagnostics.
	ErrTTSFailed = errors.New("voiceover: TTS generation failed")

	// ErrAudioInvalid is returned when the generated audio file is empty,
	// too short, or corrupt.
	ErrAudioInvalid = errors.New("voiceover: generated audio is invalid")

	// ErrDriveUploadFailed is returned when the optional Drive upload fails
	// after TTS succeeded. The local file is still valid.
	ErrDriveUploadFailed = errors.New("voiceover: Drive upload failed (local file is valid)")

	// ErrDestinationRequired is returned when the caller explicitly requests
	// Drive upload but provides no destination (empty FolderID).
	ErrDestinationRequired = errors.New("voiceover: destination folder ID is required for Drive upload")

	// ErrDispatcherUnavailable is returned when the use case's persistence
	// port (voiceover repository) is nil (composition bug or partial deploy).
	ErrDispatcherUnavailable = errors.New("voiceover: persistence port not wired")

	// ErrDeduplication indicates that an existing artifact with the same
	// deterministic ID was found and force_regenerate was false. This is
	// not an error — it's a signal that the cached result is available.
	// Callers check with errors.Is and read the existing Result from the
	// use case's return value.
	ErrDeduplication = errors.New("voiceover: artifact exists (deduplication hit)")
)
