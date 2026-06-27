// Package scripts — scene_types.go defines the per-stage result types
// used by PostProcessResult and PipelineResult in postprocessor_registry.go.
//
// These types were originally defined in adapters/postprocessor_registry.go
// (package adapters, removed as duplicate during merge resolution).
package scripts

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// SceneVoiceover is a single scene-voiceover outcome from
// VoiceoverProcessor. PR 9: voices map to model-defined scenes
// 1:1 with stable indexes (matches engineResult.Output.SpecScene.Scenes).
type SceneVoiceover struct {
	SceneIndex int
	Status     string // "completed" | "failed" | "empty_result"
	Link       string // DriveLink for the produced audio
	LocalPath  string // local on-disk path (debugging)
}

// SceneImage is a single scene-image outcome from ImageProcessor.
// PR 9: images map to model-defined scenes 1:1.
type SceneImage struct {
	Index int
	Text  string // scene text used as the generation prompt
	URL   string // public URL of the generated image
}

// PipelineResult aggregates the postprocessor outputs across the
// full Run sequence. PR 5: it's the typed merged view that
// generation_job.go writes to script/section rows via the
// canonical artifacts contract.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	SceneImages      []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string
}
