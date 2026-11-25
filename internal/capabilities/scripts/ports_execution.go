package scriptgeneration

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	portexec "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/exec"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Compatibility facade: execution ports moved to scripts/ports/exec.
type (
	OverlayPrepareEnqueuer          = portexec.OverlayPrepareEnqueuer
	OverlayRenderEnqueuer           = portexec.OverlayRenderEnqueuer
	LocalizedRenderInput            = portexec.LocalizedRenderInput
	LocalizedOverlayLineage         = portexec.LocalizedOverlayLineage
	LocalizedRenderEnqueuer         = portexec.LocalizedRenderEnqueuer
	LocalizedRenderRecoveryEnqueuer = portexec.LocalizedRenderRecoveryEnqueuer
	CombinedAudioRenderer           = portexec.CombinedAudioRenderer
	MediaPreflight                  = portexec.MediaPreflight
	FinalAudioPublishResult         = portexec.FinalAudioPublishResult
	FinalAudioPublisher             = portexec.FinalAudioPublisher
	ExecutionContext                = portexec.ExecutionContext
	ExecutionStep                   = portexec.ExecutionStep
	ArtifactOperation               = portexec.ArtifactOperation
	ExecutionRecorder               = portexec.ExecutionRecorder
)

var NewExecutionContext = portexec.NewExecutionContext

const (
	OperationTranslation = portexec.OperationTranslation
	OperationTTS         = portexec.OperationTTS
	OperationSubtitles   = portexec.OperationSubtitles
	OperationRender      = portexec.OperationRender
	OperationValidation  = portexec.OperationValidation
	OperationDriveUpload = portexec.OperationDriveUpload
)

type AudioAssetResolver = scriptaudio.AssetResolver

var NewAudioAssetResolver = scriptaudio.NewAssetResolver

// Compatibility facade for the pure audio resolvers in scripts/audio.
type (
	AudioAutomationCompiler = scriptaudio.AudioAutomationCompiler
	AudioIntentResolver     = scriptaudio.AudioIntentResolver
	BackgroundMusicResolver = scriptaudio.BackgroundMusicResolver
)

const (
	DefaultBGMDuckGainDB    = scriptaudio.DefaultBGMDuckGainDB
	DefaultBGMDuckAttackUS  = scriptaudio.DefaultBGMDuckAttackUS
	DefaultBGMDuckReleaseUS = scriptaudio.DefaultBGMDuckReleaseUS
	CanonicalBGMDuckFloorDB = scriptaudio.CanonicalBGMDuckFloorDB
)

var (
	NewAudioAutomationCompiler = scriptaudio.NewAudioAutomationCompiler
	NewAudioIntentResolver     = scriptaudio.NewAudioIntentResolver
	NewBackgroundMusicResolver = scriptaudio.NewBackgroundMusicResolver
)

// AudioIntentCompileResult and CompileAudioWithIntents remain on the scripts
// facade; the pure plan construction is owned by scripts/audio.
type AudioIntentCompileResult = scriptaudio.IntentCompileResult

func CompileAudioWithIntents(
	ctx context.Context,
	timeline audio.CanonicalTimeline,
	profile audio.CanonicalAudioProfile,
	policy audio.AudioMixPolicy,
	bgmIntents []scriptpkg.BackgroundMusicIntent,
	sfxIntents []scriptpkg.SoundEffectIntent,
	source contract.AudioAssetSource,
) (AudioIntentCompileResult, error) {
	return scriptaudio.CompileWithIntents(ctx, timeline, profile, policy, bgmIntents, sfxIntents, source)
}

// noopExecutionRecorder keeps local/unit runtimes safe when durable lineage
// is intentionally not wired. Production composition injects the Job Registry
// adapter; this is not a fake success path for registry writes.
type noopExecutionRecorder struct{}

func (noopExecutionRecorder) StartStep(context.Context, ExecutionContext, ExecutionStep) error {
	return nil
}
func (noopExecutionRecorder) CompleteStep(context.Context, ExecutionContext, ExecutionStep) error {
	return nil
}
func (noopExecutionRecorder) FailStep(context.Context, ExecutionContext, ExecutionStep, error) error {
	return nil
}
func (noopExecutionRecorder) AttachInputAsset(context.Context, ExecutionContext, string, string, int) error {
	return nil
}
func (noopExecutionRecorder) AttachOutputAsset(context.Context, ExecutionContext, string, string, int) error {
	return nil
}
func (noopExecutionRecorder) RecordMetric(context.Context, ExecutionContext, string, string, float64, string) error {
	return nil
}
func (noopExecutionRecorder) RecordOperation(context.Context, ExecutionContext, ArtifactOperation) error {
	return nil
}

// CompileCanonicalAudioPlanAudioOnlyWithIntents is the audio-compile entry
// point when a run carries a BGM/SFX intent block. It builds the same
// VO-governed canonical timeline + primary (voiceover/original-clip) assets
// as CompileCanonicalAudioPlanAudioOnly, then funnels the intents through
// the canonical pipeline — AudioAssetResolver → BackgroundMusicResolver →
// AudioLoopExpander → AudioIntentResolver → AudioAutomationCompiler — into
// audio.CompileWithLayers (via CompileAudioWithIntents). The primary and
// BGM/SFX asset tables are merged into one renderer input.
//
// Fail-closed: a nil source, an unresolved asset, a BGM without a certified
// duration, or an SFX that overruns its source fails the whole compile — the
// run never renders a partial or guessed layered plan.
func CompileCanonicalAudioPlanAudioOnlyWithIntents(
	ctx context.Context,
	result contract.GenerateResult,
	language contract.Language,
	profile audio.CanonicalAudioProfile,
	source contract.AudioAssetSource,
	policy audio.AudioMixPolicy,
	bgm []scriptpkg.BackgroundMusicIntent,
	sfx []scriptpkg.SoundEffectIntent,
	clipStartSFX []scriptpkg.ClipStartSoundEffectIntent,
	randomSFXOnClipStart bool,
) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, audiocontract.CompileTimings, error) {
	return CompileCanonicalAudioPlanAudioOnlyWithIntentsPool(ctx, result, language, profile, source, policy, bgm, sfx, clipStartSFX, randomSFXOnClipStart, nil)
}

// CompileCanonicalAudioPlanAudioOnlyWithIntentsPool is the same compile with
// an explicit clip-start SFX selection pool (the indexed SFX library). An
// empty pool keeps the built-in whoosh vocabulary.
func CompileCanonicalAudioPlanAudioOnlyWithIntentsPool(
	ctx context.Context,
	result contract.GenerateResult,
	language contract.Language,
	profile audio.CanonicalAudioProfile,
	source contract.AudioAssetSource,
	policy audio.AudioMixPolicy,
	bgm []scriptpkg.BackgroundMusicIntent,
	sfx []scriptpkg.SoundEffectIntent,
	clipStartSFX []scriptpkg.ClipStartSoundEffectIntent,
	randomSFXOnClipStart bool,
	sfxPool []string,
) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, audiocontract.CompileTimings, error) {
	// Audio-only narration runs may be compiled before clip materialization.
	// With VOICEOVER_ONLY there is deliberately no dependency on a local clip
	// audio path: the master is narration + optional BGM, while the rendered
	// MP4 clips keep their own source audio contract independently.
	if policy == audio.MixVoiceoverOnly {
		result = resultWithoutClipAudioIntents(result)
	}
	timeline, primaryAssets, timings, err := buildCanonicalTimelineAndPrimaryAssets(result, language, false)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	if len(clipStartSFX) > 0 {
		explicit, err := scriptaudio.ClipStartSFXIntents(timeline, clipStartSFX)
		if err != nil {
			return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
		}
		sfx = append(append([]scriptpkg.SoundEffectIntent(nil), sfx...), explicit...)
	}
	if randomSFXOnClipStart {
		excluded := make([]string, 0, len(clipStartSFX))
		for _, cue := range clipStartSFX {
			excluded = append(excluded, cue.ClipID)
		}
		sfx = append(append([]scriptpkg.SoundEffectIntent(nil), sfx...), scriptaudio.RandomClipStartSFXIntentsWithPoolExcluding(timeline, sfxPool, excluded)...)
	}
	planStarted := time.Now()
	compiled, err := CompileAudioWithIntents(ctx, timeline, profile, policy, bgm, sfx, source)
	timings.AudioPlanCompileMS = time.Since(planStarted).Milliseconds()
	timings.AudioAssetResolveMS = compiled.AudioAssetResolveMS
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	return timeline, compiled.Plan, scriptaudio.MergeResolvedAssets(primaryAssets, compiled.Assets), timings, nil
}
