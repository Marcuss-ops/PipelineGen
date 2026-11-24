// clips_aliases.go — facade over the clip-identity and clip-audio leaf.
//
// The identity gate, the audio-prep seam and the single-clip background are
// pure functions over the generate contract and the audio capability: they
// live in scripts/clips and the root keeps the historical names.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/clips"

type (
	ClipIdentityMismatch = clips.ClipIdentityMismatch
	clipIdentityMismatch = clips.ClipIdentityMismatch
)

const (
	ClipIdentityReasonMismatch = clips.ClipIdentityReasonMismatch
	ClipIdentityReasonMissing  = clips.ClipIdentityReasonMissing
	OverlayBackgroundPlateFit  = clips.OverlayBackgroundPlateFit
	clipIdentityReasonMismatch = clips.ClipIdentityReasonMismatch
	clipIdentityReasonMissing  = clips.ClipIdentityReasonMissing
	overlayBackgroundPlateFit  = clips.OverlayBackgroundPlateFit
)

var (
	ApplyFixedPlaybackWindow     = clips.ApplyFixedPlaybackWindow
	AuditSceneClipIdentity       = clips.AuditSceneClipIdentity
	ClampClipAudioRanges         = clips.ClampClipAudioRanges
	ClipIdentityItems            = clips.ClipIdentityItems
	ClipIdentityMismatchSceneIDs = clips.ClipIdentityMismatchSceneIDs
	IdentityMatchesPerson        = clips.IdentityMatchesPerson
	IdentityNamesOverlap         = clips.IdentityNamesOverlap
	MergeAudioCompileTimings     = clips.MergeAudioCompileTimings
	NormalizeIdentityName        = clips.NormalizeIdentityName
	OverlaySceneClip             = clips.OverlaySceneClip
	PrepareClipAudioAssets       = clips.PrepareClipAudioAssets
	RebuildFixedMediaTiming      = clips.RebuildFixedMediaTiming
	SceneClipsForIdentity        = clips.SceneClipsForIdentity
	ScenePersonEntities          = clips.ScenePersonEntities
	SceneUsesClipAudio           = clips.SceneUsesClipAudio
	SingleClipOverlayBackground  = clips.SingleClipOverlayBackground
	ValidateSceneClipIdentity    = clips.ValidateSceneClipIdentity
	applyFixedPlaybackWindow     = clips.ApplyFixedPlaybackWindow
	auditSceneClipIdentity       = clips.AuditSceneClipIdentity
	clampClipAudioRanges         = clips.ClampClipAudioRanges
	clipIdentityItems            = clips.ClipIdentityItems
	clipIdentityMismatchSceneIDs = clips.ClipIdentityMismatchSceneIDs
	identityMatchesPerson        = clips.IdentityMatchesPerson
	identityNamesOverlap         = clips.IdentityNamesOverlap
	mergeAudioCompileTimings     = clips.MergeAudioCompileTimings
	normalizeIdentityName        = clips.NormalizeIdentityName
	overlaySceneClip             = clips.OverlaySceneClip
	prepareClipAudioAssets       = clips.PrepareClipAudioAssets
	rebuildFixedMediaTiming      = clips.RebuildFixedMediaTiming
	sceneClipsForIdentity        = clips.SceneClipsForIdentity
	scenePersonEntities          = clips.ScenePersonEntities
	sceneUsesClipAudio           = clips.SceneUsesClipAudio
	singleClipOverlayBackground  = clips.SingleClipOverlayBackground
	validateSceneClipIdentity    = clips.ValidateSceneClipIdentity
)
