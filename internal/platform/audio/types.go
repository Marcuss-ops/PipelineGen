package audioasset

import "time"

// AudioInput is the input to Processor.Generate. PR-VO-B1 (June 2026):
// the previous `Destination *asset.ResolveRequest` field is gone.
// Processor writes ONLY to local FS; the resolved Drive destination
// flows from voiceover.processLanguage to Lifecycle.ProcessAsset
// directly (which performs Step 2 upload). AudioInput keeps the
// fields Processor actually consumes.
type AudioInput struct {
	Text          string
	Language      string
	Voice         string // the explicit voice identifier (required in production; passed as --voice)
	Filename      string
	OutputDir     string
	Strategy      string // "replace", "skip", "fail"
	RemoveSilence bool
	UseStdin      bool // pipe text via stdin instead of --text (avoids OS arg limits)
	// AllowVoiceFallback enables the Python sidecar's automatic voice
	// selection when Voice is empty. Default false (production fail-closed).
	// Set to true only in debug / smoke test contexts.
	AllowVoiceFallback bool
}

// AudioResult carries the outcome of a single TTS generation.
//
// PR-VO-A1 (June 2026, Lost Voice): the canonical TTS voice identifier
// returned by scripts/bridges/tts_edge.py lives in `Voice` only
// (captured in processor.go from the bridge's stdout JSON).
//
// PR-VO-B1 (June 2026, Drive upload split): DriveLink and DriveFileID
// are zero-valued when Processor returns. Lifecycle.ProcessAsset (Step
// 2 in internal/capabilities/assets/lifecycle/service.go) fills them
// after Generate returns. AudioResult keeps the fields for back-compat
// with callers that read both, but consumers should rely on
// lifecycleResult for the Drive surface.
type AudioResult struct {
	LocalPath     string
	CleanedPath   string
	LegacyFileMD5 string
	Duration      time.Duration
	// DriveLink / DriveFileID: always zero from audioasset.Processor;
	// Lifecycle fills. Deprecated for direct read on Processor output.
	DriveLink   string
	DriveFileID string
	Status      string
	Error       string
	// Voice is the canonical TTS voice returned by scripts/bridges/tts_edge.py
	// (e.g. "en-US-RogerNeural"). Empty when the bridge returned no voice.
	Voice string

	// MetadataPath is the path to the bridge's word-boundary metadata
	// (<out>.metadata.jsonl) captured in the SAME synthesis stream that
	// produced the audio. Empty when zero boundaries were captured (the
	// bridge removes the file in that case) or the bridge did not report a
	// path. The application layer is responsible for parsing it into the
	// canonical SpeechTimingArtifact — the provider only hands it over.
	MetadataPath string

	// BoundaryCount is the number of word boundaries captured by the bridge
	// in one synthesis pass. Zero means no timing was captured.
	BoundaryCount int

	// Metrics describes one persistent-worker synthesis attempt. Durations
	// are wall-clock milliseconds and are intentionally kept with the asset
	// result so callers can diagnose queueing versus Edge service time.
	Metrics TTSMetrics
}

type TTSMetrics struct {
	QueueMS         int64   `json:"tts_queue_ms,omitempty"`
	LockWaitMS      int64   `json:"tts_lock_wait_ms,omitempty"`
	VoiceResolveMS  int64   `json:"tts_voice_resolve_ms,omitempty"`
	StreamMS        int64   `json:"tts_stream_ms,omitempty"`
	PostprocessMS   int64   `json:"tts_postprocess_ms,omitempty"`
	AudioDurationMS int64   `json:"tts_audio_duration_ms,omitempty"`
	RTF             float64 `json:"tts_rtf,omitempty"`
}
