// Package scripts — scene_stubs.go: per-godlike/07-zero-legacy cleanup
// (2026-07-25), the `ScenesService` struct + `NewScenesService` ctor
// were REMOVED (0 callers, "PR G placeholder, canonical impl deleted"
// per the pre-cleanup file doc). The remaining types are ACTIVE
// surface and must NOT be moved:
//
//   - FolderResolver: drive-folder ID resolution with fallback; used by
//     voiceover/document pipelines (see `voiceover/usecase.go`,
//     (was: `documents_usecase.go`; Sprint 1.0 retired that file).
//   - SceneVoiceover / SceneImage: per-scene postprocessor output rows.
//
// `dto.PipelineResult` was REMOVED (PR-DTO-PIPELINERESULT-DEDUPE, 2026-07-09;
// follows PR-5/PR-6 SCRIPT-DOWNSTREAM-CUTOVER at canonical ship_sha
// `4bf91e52c`): the canonical postprocessor-output aggregator lives
// at `internal/capabilities/scripts/adapters.PipelineResult`
// (declared in `adapters/postprocessor_document.go`). The dto-package
// duplicate was a residual from a previous consolidation wave
// (zero callers verified via `rg 'dto\.PipelineResult' internal/`).
//
// A future re-introduction of any type retired from this file MUST be
// a fresh canonical implementation in `internal/capabilities/scripts/usecase/`
// or `internal/capabilities/scripts/adapters/` (NOT a stub re-added here)
// per godlike/07 minimum-blast-radius.
package dto

import (
	"context"
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
