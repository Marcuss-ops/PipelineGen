// Package scene owns the sealed scene projection shared by generation,
// audio, overlay and final-job. It is a contract package: it may depend on
// kernel/platform/audio value types, never on the scriptgeneration root.
package scene

import kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

// Language is an ISO 639-1 two-letter code (e.g. "en", "es").
type Language = kernelaudio.Language

// ResolvedScene is the sealed technical projection of an editorial Scene.
// Once created, downstream timeline/render/audio code must not read legacy
// millisecond fields or re-derive duration from a clip.
type ResolvedScene struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	// FixedMedia is propagated from Scene.ExecutionMode so the canonical
	// timeline and renderer cannot lose the protected fixed-section boundary.
	FixedMedia bool `json:"fixed_media,omitempty"`
	// TimelineStartUS is the absolute timeline placement of this scene: the
	// cumulative sum of all preceding scene durations. The scene end is always
	// derived as TimelineStartUS + DurationUS and is never stored.
	TimelineStartUS int64                `json:"timeline_start_us"`
	Text            map[Language]string  `json:"text"`
	DurationUS      int64                `json:"duration_us"`
	Video           audio.VideoSegment   `json:"video"`
	VideoSegments   []audio.VideoSegment `json:"video_segments,omitempty"`
	ClipAudioPaths  map[string]string    `json:"clip_audio_paths,omitempty"`
	Voiceover       *ResolvedVoiceover   `json:"voiceover,omitempty"`
	AudioIntents    []audio.AudioIntent  `json:"audio_intents"`
}

// ResolvedVoiceover is the sealed voiceover reference for one scene.
type ResolvedVoiceover struct {
	AssetID    string `json:"asset_id"`
	Path       string `json:"path"`
	DurationUS int64  `json:"duration_us"`
}
