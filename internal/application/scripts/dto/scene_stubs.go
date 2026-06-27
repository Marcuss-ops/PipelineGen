// Package scripts — scene_stubs.go provides minimal stub definitions
// for types that lived in the now-deleted scenes_service.go and types.go.
// These stubs satisfy the compiler; the real implementations were deleted
// in origin/main and will be re-introduced when the scene pipeline is
// re-constituted.
package dto

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ScenesService handles scene image/voiceover generation.
// Real implementation was in scenes_service.go (deleted).
type ScenesService struct{}

// NewScenesService creates a new ScenesService (stub).
func NewScenesService(imgSvc, voSvc, log, cfg, resolveFolder, groupsRes interface{}, albumCapacity int) *ScenesService {
	return &ScenesService{}
}

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
