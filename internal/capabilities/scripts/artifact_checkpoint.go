package scriptgeneration

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The artifact-checkpoint contract is owned by internal/kernel/script so the
// SQLite adapter can implement it without importing the business package.
type ArtifactCheckpointKind = scriptpkg.ArtifactCheckpointKind
type ArtifactCheckpoint = scriptpkg.ArtifactCheckpoint
type ArtifactCheckpointRepository = scriptpkg.ArtifactCheckpointRepository

const (
	ArtifactCheckpointSchemaVersion   = scriptpkg.ArtifactCheckpointSchemaVersion
	MaxArtifactCheckpointPayloadBytes = scriptpkg.MaxArtifactCheckpointPayloadBytes

	ArtifactCheckpointSceneText       = scriptpkg.ArtifactCheckpointSceneText
	ArtifactCheckpointTranslation     = scriptpkg.ArtifactCheckpointTranslation
	ArtifactCheckpointVoiceover       = scriptpkg.ArtifactCheckpointVoiceover
	ArtifactCheckpointOverlayPlan     = scriptpkg.ArtifactCheckpointOverlayPlan
	ArtifactCheckpointOverlayRender   = scriptpkg.ArtifactCheckpointOverlayRender
	ArtifactCheckpointLocalizedRender = scriptpkg.ArtifactCheckpointLocalizedRender
	ArtifactCheckpointFinalAudio      = scriptpkg.ArtifactCheckpointFinalAudio
	ArtifactCheckpointFinalJobPlan    = scriptpkg.ArtifactCheckpointFinalJobPlan
	ArtifactCheckpointFinalJobReceipt = scriptpkg.ArtifactCheckpointFinalJobReceipt
)

var (
	ErrInvalidArtifactCheckpoint = scriptpkg.ErrInvalidArtifactCheckpoint
	ErrArtifactCheckpointCorrupt = scriptpkg.ErrArtifactCheckpointCorrupt
)

func ArtifactCheckpointKey(parts ...string) (string, error) {
	return scriptpkg.ArtifactCheckpointKey(parts...)
}
