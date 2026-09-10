// Package adapters — postprocessor_voiceover.go: voiceover-related types.
// Owns: SceneVoiceover.
package adapters

import capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// SceneVoiceover is a single scene-voiceover outcome from
// VoiceoverProcessor. PR 9: voices map to model-defined scenes
// 1:1 with stable indexes (matches engineResult.Output.SpecScene.Scenes).
type SceneVoiceover struct {
	SceneIndex int
	Language   string
	Status     string // "completed" | "failed" | "empty_result"
	Link       string // DriveLink for the produced audio
	LocalPath  string // local on-disk path
	DurationMs int64  // synthesized audio duration

	// Timing carries the per-language timing bundle references (nil when
	// timing is disabled). Written into VoiceoverBinding.Timing by the
	// merge so timing links survive downstream processors.
	Timing *scriptpkg.VoiceoverTimingBinding

	// TimingArtifact is the in-memory word-level timing SSOT returned by the
	// same synthesis call that produced the audio. It is deliberately not
	// serialized: the public binding exposes only verified bundle links, while
	// the batch overlay compiler needs the exact boundaries to place Chronon
	// items without estimating from text length.
	TimingArtifact *capabilityaudio.SpeechTimingArtifact `json:"-"`
}
