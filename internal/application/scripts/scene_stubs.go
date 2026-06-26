// Package scripts — scene_stubs.go provides minimal stub definitions
// for types that lived in the now-deleted scenes_service.go and types.go.
// These stubs satisfy the compiler; the real implementations were deleted
// in origin/main and will be re-introduced when the scene pipeline is
// re-constituted.
package scripts

// ScenesService handles scene image/voiceover generation.
// Real implementation was in scenes_service.go (deleted).
type ScenesService struct{}

// NewScenesService creates a new ScenesService (stub).
func NewScenesService(imgSvc, voSvc, log, cfg, resolveFolder, groupsRes interface{}, albumCapacity int) *ScenesService {
	return &ScenesService{}
}

// BuildScenesWithMarkers builds scenes with narration/clip markers (stub).
// Real implementation was in scenes_service.go (deleted).
func BuildScenesWithMarkers(script string, pack interface{}) []ClipScene {
	return nil
}
