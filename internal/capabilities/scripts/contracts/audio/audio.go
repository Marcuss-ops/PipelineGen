// Package audio owns the delivery-audio DTOs shared by the audio compiler,
// the combined renderer and final-job assembly. It has no root-package
// dependency; implementations consume these types through ports.
package audio

import kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

// FinalAudioReference is the certified delivery-audio reference. It is the
// contract between the audio compiler, the renderer and final-job assembly.
type FinalAudioReference = kernelaudio.FinalAudioReference

// CompileTimings captures the per-stage wall-clock of combined-audio
// compilation: asset resolution, timeline build, clip preparation and final
// audio-plan compile.
type CompileTimings struct {
	AudioAssetResolveMS int64
	TimelineCompileMS   int64
	ClipAudioPrepareMS  int64
	AudioPlanCompileMS  int64
}
