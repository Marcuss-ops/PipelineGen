// Package adapters — postprocessor_voiceover.go: voiceover-related types.
// Owns: SceneVoiceover.
package adapters

// SceneVoiceover is a single scene-voiceover outcome from
// VoiceoverProcessor. PR 9: voices map to model-defined scenes
// 1:1 with stable indexes (matches engineResult.Output.SpecScene.Scenes).
type SceneVoiceover struct {
	SceneIndex int
	Status     string // "completed" | "failed" | "empty_result"
	Link       string // DriveLink for the produced audio
	LocalPath  string // local on-disk path (debugging)
}
