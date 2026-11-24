package contract

import (
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type RenderArtifact = renderqueue.RenderArtifact

// FinalAudioReference is the canonical delivery-audio contract.
type FinalAudioReference = audiocontract.FinalAudioReference

// AudioConfig is the request-side audio phase contract. GenerateRequest embeds
// it so the phase owns the fields while the durable JSON keeps its flat keys.
type AudioConfig = audiocontract.Config

// AudioOutput is the result-side audio phase contract. GenerateResult embeds
// it for the same reason: one owner, unchanged persisted wire shape.
type AudioOutput = audiocontract.Output

// AudioPipelineMetrics and TTSSSceneMetric are the audio timing projection
// owned by contracts/audio. The aliases keep existing consumers source
// compatible without giving the root a second declaration.
type AudioPipelineMetrics = audiocontract.AudioPipelineMetrics

type TTSSSceneMetric = audiocontract.TTSSSceneMetric
