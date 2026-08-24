package assets

import "context"

// Transcriber extracts the audio transcript from a downloaded clip.
// It is the canonical port satisfied by the shared Whisper adapter
// (PR-ARTLIST-MANDATORY-TRANSCRIPTION, July 2026).
type Transcriber interface {
	// Transcribe returns the transcript text, the detected language
	// code (BCP-47), and an error if transcription fails. The audio
	// path is the local filesystem path to the media file.
	Transcribe(ctx context.Context, audioPath string) (transcript string, languageCode string, err error)
}
