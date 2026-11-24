// Package scriptgeneration — audio_aliases.go: facade for the audio stage
// extracted into scripts/audio and scripts/timeline.
//
// The root is the public surface of script generation: consumers
// (adapters, platform/media/rustexec, app wiring) name these symbols as
// scriptgen.X. The aliases keep that surface stable while the code that
// owns the behaviour lives in the subpackages, so the dependency runs
// root → audio, never audio → root.
//
// Extracted 2026-11-23: audio_prefetch.go and audio_timeline.go moved to
// scripts/audio (prefetch.go, canonical_timeline.go), while the pure
// scene→timeline projection (resolved_scene.go) and the freeze-tail policy
// moved to scripts/timeline. The subpackages import only contract and the
// audio value package, so a consumer of the timeline no longer needs the
// whole root.
package scriptgeneration

import (
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/timeline"
)

// Types owned by the timeline projection.
type (
	ResolvedVoiceover     = timeline.ResolvedVoiceover
	SceneDurationResolver = timeline.SceneDurationResolver
)

// AudioCompileTimings is the per-stage timing envelope of the audio compile.
type AudioCompileTimings = audio.AudioCompileTimings

// sceneEditorialSilenceAllowanceUS is the silence allowance of the
// editorial scene duration, owned by timeline.
const sceneEditorialSilenceAllowanceUS = timeline.SceneEditorialSilenceAllowanceUS

// Timeline projection: scene → ResolvedScene and the freeze tail.
var (
	ResolveScenes             = timeline.ResolveScenes
	freezeTailedVideoSegments = timeline.FreezeTailedVideoSegments
)

// Audio compile and validation.
var (
	ValidateFinalAudioReference           = audio.ValidateFinalAudioReference
	ValidateMasterAudioInvariants         = audio.ValidateMasterAudioInvariants
	ValidateVoiceoverSourceDurations      = audio.ValidateVoiceoverSourceDurations
	ValidateChunkedVoiceovers             = audio.ValidateChunkedVoiceovers
	ValidateChunkedVoiceoversForLanguages = audio.ValidateChunkedVoiceoversForLanguages
	CompileCanonicalAudioPlan             = audio.CompileCanonicalAudioPlan
	CompileCanonicalAudioPlanWithTimings  = audio.CompileCanonicalAudioPlanWithTimings
	CompileCanonicalAudioPlanAudioOnly    = audio.CompileCanonicalAudioPlanAudioOnly
	CompileCanonicalTimeline              = audio.CompileCanonicalTimeline
)

// Package-internal seams whose callers stayed in the root: the audio
// subpackage exports them because it owns the behaviour, while the root
// keeps the original unexported names so no call site moved.
var (
	compileResolvedSceneTimeline           = audio.CompileResolvedSceneTimeline
	resolvedScenesFor                      = audio.ResolvedScenesFor
	overlayResolvedScenesFor               = audio.OverlayResolvedScenesFor
	buildCanonicalTimelineAndPrimaryAssets = audio.BuildCanonicalTimelineAndPrimaryAssets
)

// Prefetch bounds the root reports in its durable result.
const (
	audioPrefetchBudget          = audio.AudioPrefetchBudget
	maxAudioPrefetchAssetReports = audio.MaxAudioPrefetchAssetReports
	maxAudioPrefetchFailures     = audio.MaxAudioPrefetchFailures
)

// Audio asset prefetch.
var (
	PrefetchAudioAssets = audio.PrefetchAudioAssets
	canonicalAudioIDs   = audio.CanonicalAudioIDs
)
