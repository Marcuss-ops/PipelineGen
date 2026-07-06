// types/types_artifact_kind.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// ArtifactKind classifies the high-level category of a produced artifact.
type ArtifactKind string

const (
	KindVideo       ArtifactKind = "video"
	KindImage       ArtifactKind = "image"
	KindAudio       ArtifactKind = "audio"
	KindDocument    ArtifactKind = "document"
	KindScript      ArtifactKind = "script"
	KindVoiceover   ArtifactKind = "voiceover"
	KindSoundEffect ArtifactKind = "sound_effect"
	KindMetadata    ArtifactKind = "metadata"
	KindArchive     ArtifactKind = "archive"
)
