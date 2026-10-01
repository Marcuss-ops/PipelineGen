// Package usecase — usecase_builders_test.go
//
// Shared GenerateOneUseCase constructor for test engines that exercise the
// full Execute path without external systems. The normalization config always
// carries a resolvable script docs folder: applyPublicationDefaults enables
// Docs and validation fails closed without a folder (routing_context.go), so
// an empty NormalizationConfig turns every well-formed fixture into a
// validation failure. The audio processor is a passing stub because the
// normalized default audio mode is COMBINED_TIMELINE and the real renderer
// shells out to ffmpeg.
package usecase

import (
	"context"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
)

// testDocsNormalizationConfig is the canonical NormalizationConfig for usecase
// tests: production-shaped (docs folder resolvable) without external systems.
func testDocsNormalizationConfig() processor.NormalizationConfig {
	return processor.NormalizationConfig{ScriptDocsFolderID: "TEST_DOCS_FOLDER"}
}

// noopAudioProcessor satisfies mediaexec.AudioProcessor for tests that do not
// assert on audio bytes: the compiled plan is accepted and a minimal certified
// final-audio envelope is returned.
type noopAudioProcessor struct{}

func (noopAudioProcessor) MergeInputs(context.Context, []string, string) error { return nil }
func (noopAudioProcessor) RemoveSilence(context.Context, string, string) error { return nil }
func (noopAudioProcessor) Probe(context.Context, string) (*mediaexec.MediaInfo, error) {
	return &mediaexec.MediaInfo{}, nil
}
func (noopAudioProcessor) RenderAudioPlan(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets, _ string) (capabilityaudio.FinalAudioAsset, error) {
	return capabilityaudio.FinalAudioAsset{
		AssetID:              "final",
		AudioContractVersion: capabilityaudio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		AudioPlanSHA256:      plan.PlanSHA256,
		FinalAudioSHA256:     "hash",
		Codec:                plan.Output.Codec,
		Profile:              plan.Output.Profile,
		SampleRate:           plan.Output.SampleRate,
		Channels:             plan.Output.Channels,
		ChannelLayout:        plan.Output.ChannelLayout,
		Bitrate:              128000,
		DurationMS:           plan.DurationUS / 1000,
		SizeBytes:            10,
		FinalMix:             true,
		CopyEligible:         true,
		StartPTS:             0,
	}, nil
}

// buildTestUseCase wires the full GenerateOneUseCase against the shared test
// engine, the shared source registry, the postprocessor registry, and the
// no-op audio processor stub.
func buildTestUseCase(
	engine *gencore.Engine,
	registry *processor.SourceRegistry,
	ppReg *adapters.PostProcessorRegistry,
) *gencore.GenerateOneUseCase {
	uc := gencore.NewGenerateOneUseCase(testDocsNormalizationConfig(), registry, engine, ppReg, zap.NewNop())
	uc.SetAudioProcessor(noopAudioProcessor{})
	return uc
}
