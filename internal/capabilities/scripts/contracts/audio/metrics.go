package audio

import kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

// AudioPipelineMetrics is the audio-phase timing projection of the certified
// render and mix operations. It is owned by the kernel so the scripts
// capability and the platform adapters share one definition.
type AudioPipelineMetrics = kernelaudio.AudioPipelineMetrics

// TTSSSceneMetric is one per-scene TTS measurement row.
type TTSSSceneMetric = kernelaudio.TTSSSceneMetric
