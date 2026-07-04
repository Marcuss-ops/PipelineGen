// Package scripts — scene_stubs.go: per-godlike/07-zero-legacy cleanup
// (2026-07-25), the `ScenesService` struct + `NewScenesService` ctor
// were REMOVED (0 callers, "PR G placeholder, canonical impl deleted"
// per the pre-cleanup file doc). The remaining types are ACTIVE
// surface and must NOT be moved:
//
//   - FolderResolver: drive-folder ID resolution with fallback; used by
//     voiceover/document pipelines (see `voiceover/usecase.go`,
//     `documents_usecase.go`).
//   - SceneVoiceover / SceneImage: per-scene postprocessor output rows.
//   - PipelineResult: the canonical postprocessor-output aggregator
//     returned by `Run` for both voiceover + image sub-pipelines.
//
// A future re-introduction of the per-scene service MUST be a fresh
// canonical implementation in `internal/application/scripts/usecase/`
// (NOT a stub re-added to this file) per godlike/07 minimum-blast-radius.
package dto

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// FolderResolver resolves drive folder IDs with a fallback.
type FolderResolver = func(ctx context.Context, folderID, defaultFolderID string) (string, error)

// SceneVoiceover represents a generated voiceover result for a scene.
type SceneVoiceover struct {
	SceneIndex int    `json:"scene_index"`
	Status     string `json:"status"`
	Link       string `json:"link"`
	LocalPath  string `json:"local_path"`
}

// SceneImage represents a generated image result for a scene.
type SceneImage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// PipelineResult aggregates results from running postprocessors.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	Scenes           []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string
}
