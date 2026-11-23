package scriptgeneration

import (
	"context"
	"os"
	"strings"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	runnerpolicy "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// SetNLPConcurrency sets the NLP/entity-extraction fan-out width used by the
// SceneReadyCoordinator and the translated-NLP pass. Values <= 0 fall back to
// the certified default (DefaultNLPConcurrency).
func (r *Runner) SetNLPConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultNLPConcurrency
	}
	r.nlpConcurrency = concurrency
}

// observePhraseImpactTimings reports the extractive-summary worker stages.
func observePhraseImpactTimings(timings PhraseImpactTimings) {
	stages := [...]struct {
		name string
		ms   float64
	}{
		{"split", timings.SplitMS},
		{"embedding", timings.EmbeddingMS},
		{"similarity", timings.SimilarityMS},
		{"ranking", timings.RankingMS},
		{"summary", timings.SummaryMS},
		{"bullet", timings.BulletMS},
		{"total", timings.TotalMS},
	}
	for _, stage := range stages {
		if stage.ms > 0 {
			observability.ScriptPhraseImpactStageSeconds.WithLabelValues(stage.name).Observe(stage.ms / 1000)
		}
	}
}

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

// SetOverlayCanvas wires the target render canvas for the derived
// OverlayPlan. Nil-safe; a zero spec falls back to the golden canary
// default (1280×720 @ 30 FPS).
func (r *Runner) SetOverlayCanvas(canvas OverlayCanvasSpec) {
	if r != nil {
		r.overlayCanvas = canvas
	}
}

// SetOverlayRegistry wires the canonical entity type→template registry used
// by the EntityOverlayPlanner. Nil-safe; nil means overlay intent planning
// is skipped.
func (r *Runner) SetOverlayRegistry(registry *capmodel.ChrononOverlayRegistry) {
	if r != nil {
		r.overlayRegistry = registry
	}
}

// SetOverlayPrepareEnqueuer wires the overlay.prepare job enqueuer. Nil-safe;
// nil means prepare is not registered (no prepare job is enqueued).
func (r *Runner) SetOverlayPrepareEnqueuer(enqueuer OverlayPrepareEnqueuer) {
	if r != nil {
		r.overlayPrepareEnqueuer = enqueuer
	}
}

// SetOverlayRenderEnqueuer wires the timing-frozen overlay.render path.
func (r *Runner) SetOverlayRenderEnqueuer(enqueuer OverlayRenderEnqueuer) {
	if r != nil {
		r.overlayRenderEnqueuer = enqueuer
	}
}

// OverlayRenderEnqueuer returns the configured Chronon queue boundary so the
// legacy batch child path can share the exact same publisher, fresh-render
// policy and queue client as the durable runner. It is read-only wiring; the
// runner remains the owner of the field.
func (r *Runner) OverlayRenderEnqueuer() OverlayRenderEnqueuer {
	if r == nil {
		return nil
	}
	return r.overlayRenderEnqueuer
}

// SetLocalizedRenderEnqueuer wires the per-(scene, language) localized render
// fan-out. A nil enqueuer disables the fan-out (render not registered); a
// non-nil enqueuer is fail-closed (an enqueue error fails the run).
func (r *Runner) SetLocalizedRenderEnqueuer(enqueuer LocalizedRenderEnqueuer) {
	if r != nil {
		r.localizedRenderEnqueuer = enqueuer
	}
}

// SetGeocoder wires the provider-neutral geocoder used to enrich grounded
// place annotations when the request enables media_plan.provider_policy.geocoding. A nil
// adapter with an enabled policy fails closed at enrichment time.
func (r *Runner) SetGeocoder(geocoder capabilitygeocoding.Geocoder) {
	if r != nil {
		r.geocoder = geocoder
	}
}

// SetMapPlateResolver wires the run's ONLY map source: the certified basemap
// plate resolver loaded from the operator manifest. A nil resolver (or an
// uncovered place) emits no map overlay — never a fabricated one.
func (r *Runner) SetMapPlateResolver(resolver capabilityoverlay.PlateResolver) {
	if r != nil {
		r.mapPlates = resolver
	}
}

// SetOverlayPlanCompiler wires the single-call BrainRust editorial planner.
func (r *Runner) SetOverlayPlanCompiler(compiler OverlayPlanCompiler) {
	if r != nil {
		r.overlayPlanCompiler = compiler
	}
}

// OverlayPlanCompiler returns the configured Rust planner for composition
// paths that share the durable runner's semantic worker.
func (r *Runner) OverlayPlanCompiler() OverlayPlanCompiler {
	if r == nil {
		return nil
	}
	return r.overlayPlanCompiler
}

// SetVoiceoverPublishDrainer wires the async voiceover publish pool
// (P0.4: separate TTS pool from publish pool). After the voiceover
// phase, the runner drains the pool so Drive links are hydrated before
// downstream stages (audio compile, docs) consume them. A nil drainer
// means synchronous publish (backward compat).
func (r *Runner) SetVoiceoverPublishDrainer(drainer interface{ Wait() }) {
	if r != nil {
		r.voiceoverPublishDrainer = drainer
	}
}

// SetOverlayPublicationDrainer wires the bounded post-render publication pool.
// The runner joins it immediately before completion so Drive failures remain
// terminal even though they no longer occupy a RenderingGen render slot.
// The drainer is handed the run's context, because the pool is process-wide
// while the join must be scoped to the run that queued the work.
func (r *Runner) SetOverlayPublicationDrainer(drainer interface{ Wait(context.Context) error }) {
	if r != nil {
		r.overlayPublicationDrainer = drainer
	}
}

// SetMediaPreflight wires the fail-fast media requirement verification
// (P0.5). When wired, the runner executes it synchronously after normalize
// and before Gemma; any failure aborts the run before LLM, translation, or
// TTS. Fixed-media requests fail closed when it is not wired.
func (r *Runner) SetMediaPreflight(preflight MediaPreflight) {
	if r != nil {
		r.mediaPreflight = preflight
	}
}

// Compatibility exports for the runner-owned concurrency policy. New code
// should import scripts/runner directly.
const (
	DefaultGenerationConcurrency    = runnerpolicy.DefaultGenerationConcurrency
	DefaultNLPConcurrency           = runnerpolicy.DefaultNLPConcurrency
	MaxVidRushImageConcurrency      = runnerpolicy.MaxVidRushImageConcurrency
	DefaultVidRushImageConcurrency  = runnerpolicy.DefaultVidRushImageConcurrency
	DefaultTTSConcurrency           = runnerpolicy.DefaultTTSConcurrency
	MaxTranslationConcurrency       = runnerpolicy.MaxTranslationConcurrency
	DefaultTranslationConcurrency   = runnerpolicy.DefaultTranslationConcurrency
	DefaultOverlayRenderConcurrency = runnerpolicy.DefaultOverlayRenderConcurrency
)

type GenerationGate = runnerpolicy.GenerationGate

func NewGenerationGate() *GenerationGate { return runnerpolicy.NewGenerationGate() }

func NewGenerationGateWithCapacity(capacity int) *GenerationGate {
	return runnerpolicy.NewGenerationGateWithCapacity(capacity)
}

func (r *Runner) SetCombinedAudioRenderer(renderer CombinedAudioRenderer) {
	r.combinedAudioRenderer = renderer
}

// SetCheckpointResolver wires the durable per-unit checkpoint resolver that
// gates unit reuse (resume) and records unit completions. Nil-safe; nil
// keeps the legacy best-effort idempotency path (restored partial result
// only, no artifact verification).
func (r *Runner) SetCheckpointResolver(resolver *capcheckpoint.Resolver) {
	if r != nil {
		r.checkpoints = resolver
	}
}

// SetAudioAssetSource wires the BGM/SFX asset_id → local path + certified
// duration resolver port. Nil-safe; nil means the audio intent block cannot
// be resolved and a run carrying BGM/SFX intents fails closed in the
// audio-compile phase.
func (r *Runner) SetAudioAssetSource(source AudioAssetSource) {
	if r != nil {
		r.audioAssetSource = source
	}
}

// SetOverlayBackgroundSource wires the catalog/cache resolver used for
// visual background asset_ids. Nil keeps color backgrounds and callers that
// already provide a complete content-addressed ref compatible.
func (r *Runner) SetOverlayBackgroundSource(source OverlayBackgroundSource) {
	if r != nil {
		r.overlayBackgroundSource = source
	}
}

// OverlayBackgroundSource returns the canonical background resolver so the
// batch child path can use the same catalog/cache boundary as the durable
// runner. The returned port is read-only and nil when not wired.
func (r *Runner) OverlayBackgroundSource() OverlayBackgroundSource {
	if r == nil {
		return nil
	}
	return r.overlayBackgroundSource
}

// SetFinalAudioPublisher wires the canonical delivery publisher used to make
// the certified full-audio Drive link available to the document phase.
func (r *Runner) SetFinalAudioPublisher(publisher FinalAudioPublisher) {
	if r != nil {
		r.finalAudioPublisher = publisher
	}
}

// SetFinalAudioCacheDir enables cross-run reuse of a certified master keyed by
// the compiled audio plan. An empty directory (or an explicit "off") disables
// the cache, which is how an A/B run measures it. Nil-safe.
func (r *Runner) SetFinalAudioCacheDir(dir string) {
	if r == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "", "off", "disabled", "none":
		r.finalAudioCache = nil
	default:
		r.finalAudioCache = scriptaudio.NewFinalAudioCache(dir)
	}
	r.finalAudioCacheAllRuns = finalAudioCacheAllRunsFromEnv()
}

// finalAudioCacheAllRunsEnv is the rollback switch for the non-final gate. The
// compiled plan hash — not the run class — is the work identity, so the default
// is every run; "=0" restores the historical final_job-only behaviour without
// a rebuild.
const finalAudioCacheAllRunsEnv = "PIPELINEGEN_FINAL_AUDIO_CACHE_ALL_RUNS"

func finalAudioCacheAllRunsFromEnv() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(finalAudioCacheAllRunsEnv)))
	switch raw {
	case "0", "false", "off", "no", "disabled":
		return false
	}
	return true
}

// finalAudioCacheApplies reports whether this run may read and write the
// plan-keyed master cache. A hit still passes ValidateFinalAudioReference, so
// widening the gate changes cost, not correctness.
func (r *Runner) finalAudioCacheApplies(req GenerateRequest) bool {
	if r == nil || r.finalAudioCache == nil || !r.finalAudioCache.Enabled() {
		return false
	}
	return req.FinalJob || r.finalAudioCacheAllRuns
}

func finalAudioReferenceFromCache(ref scriptaudio.CachedFinalAudioReference) FinalAudioReference {
	return FinalAudioReference{
		AssetID: ref.AssetID, Path: ref.Path, Container: ref.Container,
		AudioContractVersion: ref.AudioContractVersion, AudioPlanVersion: ref.AudioPlanVersion,
		PlanSHA256: ref.PlanSHA256, FinalAudioSHA256: ref.FinalAudioSHA256,
		Codec: ref.Codec, Profile: ref.Profile, SampleRate: ref.SampleRate,
		Channels: ref.Channels, ChannelLayout: ref.ChannelLayout, Bitrate: ref.Bitrate,
		DurationUS: ref.DurationUS, DurationMS: ref.DurationMS, StartPTS: ref.StartPTS,
		SizeBytes: ref.SizeBytes, FinalMix: ref.FinalMix, CopyEligible: ref.CopyEligible,
	}
}

func finalAudioCacheReference(ref FinalAudioReference) scriptaudio.CachedFinalAudioReference {
	return scriptaudio.CachedFinalAudioReference{
		AssetID: ref.AssetID, Path: ref.Path, Container: ref.Container,
		AudioContractVersion: ref.AudioContractVersion, AudioPlanVersion: ref.AudioPlanVersion,
		PlanSHA256: ref.PlanSHA256, FinalAudioSHA256: ref.FinalAudioSHA256,
		Codec: ref.Codec, Profile: ref.Profile, SampleRate: ref.SampleRate,
		Channels: ref.Channels, ChannelLayout: ref.ChannelLayout, Bitrate: ref.Bitrate,
		DurationUS: ref.DurationUS, DurationMS: ref.DurationMS, StartPTS: ref.StartPTS,
		SizeBytes: ref.SizeBytes, FinalMix: ref.FinalMix, CopyEligible: ref.CopyEligible,
	}
}

// SetFinalJobSubmitter wires the optional two-stage remote render handoff.
func (r *Runner) SetFinalJobSubmitter(submitter FinalJobSubmitter) {
	if r != nil {
		r.finalJobSubmitter = submitter
	}
}

// SetScriptPersistence wires the canonical SQLite script-row writer. The
// runner invokes it only when GenerateRequest.SaveToDB is true.
func (r *Runner) SetScriptPersistence(persistence ScriptPersistence) {
	if r != nil {
		r.scriptPersistence = persistence
	}
}

// SetExecutionRecorder injects the durable execution/lineage port. A nil
// recorder restores the safe no-op implementation used by unit runtimes.
func (r *Runner) SetExecutionRecorder(recorder ExecutionRecorder) {
	if r != nil {
		r.setRecorder(recorder)
	}
}

// SetSceneCommitObserver wires the observer notified of every SceneCommitted
// event. A nil observer is safe and disables emission; a non-nil observer is
// fail-closed (a commit error fails the scene-text stage). This is the
// injection seam used by tests; per-run wiring registered by beginVidRush
// takes precedence, so concurrent runs never share a coordinator.
func (r *Runner) SetSceneCommitObserver(observer SceneCommitObserver) {
	if r != nil {
		r.sceneCommitObserver = observer
	}
}

// SetVidRushBarrier wires the final barrier awaited after scene generation
// completes. A nil barrier is safe and skips the wait; a non-nil barrier is
// fail-closed (a barrier error fails the run) and blocks only for enrichments
// still running, never re-running whole-document extraction. This
// is the injection seam used by tests; per-run wiring registered by
// beginVidRush takes precedence.
func (r *Runner) SetVidRushBarrier(barrier VidRushBarrier) {
	if r != nil {
		r.vidRushBarrier = barrier
	}
}

// SetTTSConcurrency sets the TTS voiceover worker-pool size. Values <= 0
// fall back to the certified default (DefaultTTSConcurrency).
func (r *Runner) SetTTSConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultTTSConcurrency
	}
	r.ttsConcurrency = concurrency
}

// SetOverlayRenderConcurrency sets the multilingual overlay render fan-out
// width: how many per-language OverlayPlans the overlay_render phase may have
// in flight against RenderingGen at once. Values <= 0 fall back to the
// certified default (DefaultOverlayRenderConcurrency). The effective width is
// additionally clamped to the number of plans that still need rendering, so a
// single-language run never pays for an idle slot.
//
// It is a PIPELINING bound, not a GPU bound: RenderingGen's worker owns
// worker.gpu_lanes and stays the only authority on concurrent GPU work.
func (r *Runner) SetOverlayRenderConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultOverlayRenderConcurrency
	}
	r.overlayRenderConcurrency = concurrency
}

// overlayRenderWorkers resolves the effective overlay render fan-out width,
// never returning less than 1 so at least one plan is always submitted.
func (r *Runner) overlayRenderWorkers() int {
	if r == nil || r.overlayRenderConcurrency <= 0 {
		return DefaultOverlayRenderConcurrency
	}
	return r.overlayRenderConcurrency
}

// OverlayRenderConcurrency reports the effective overlay render fan-out width
// (the certified default when unset). It is read-only wiring: the runner remains
// the owner of the field, and this exists so the composition root can log the
// width it actually wired instead of re-deriving it.
func (r *Runner) OverlayRenderConcurrency() int {
	return r.overlayRenderWorkers()
}

// SetTranslationConcurrency sets the bounded translation worker-pool size.
func (r *Runner) SetTranslationConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultTranslationConcurrency
	}
	if concurrency > MaxTranslationConcurrency {
		concurrency = MaxTranslationConcurrency
	}
	r.translationConcurrency = concurrency
}

// SetVidRushTimingRecorder wires the recorder that receives the scene-
// generation wall-clock window, enabling the generation↔VidRush overlap
// metric. A nil recorder is safe and disables timing. This is the injection
// seam used by tests; per-run wiring registered by beginVidRush takes
// precedence.
func (r *Runner) SetVidRushTimingRecorder(recorder VidRushTimingRecorder) {
	if r != nil {
		r.vidRushTiming = recorder
	}
}

// SetPhraseAligner wires the Rust phrase/word alignment boundary. Nil means a
// run that carries certified timing fails closed instead of falling back to a
// Go matcher.
func (r *Runner) SetPhraseAligner(aligner PhraseAligner) {
	if r != nil {
		r.phraseAligner = aligner
	}
}

// SetLocalizedIdentityMatcher wires the Rust localized-identity boundary.
// Nil means a translated run fails closed instead of falling back to a Go
// matcher or inflection.
func (r *Runner) SetLocalizedIdentityMatcher(matcher LocalizedIdentityMatcher) {
	if r != nil {
		r.localizedIdentity = matcher
	}
}

// LocalizedIdentityMatcher returns the run-scoped Rust identity seam.
func (r *Runner) LocalizedIdentityMatcher() LocalizedIdentityMatcher {
	if r == nil {
		return nil
	}
	return r.localizedIdentity
}

// PhraseAligner returns the run-scoped Rust alignment seam so the batch path
// can share the exact worker the durable runner uses.
func (r *Runner) PhraseAligner() PhraseAligner {
	if r == nil {
		return nil
	}
	return r.phraseAligner
}

// SetVidRushPipeline wires the composition-time incremental VidRush
// dependencies. A nil pipeline disables incremental VidRush (batch workflows
// keep enriching the whole document later). The Runner builds a fresh,
// run-scoped coordinator from these dependencies for each run.
func (r *Runner) SetVidRushPipeline(pipeline *VidRushPipeline) {
	if r != nil {
		r.vidRushPipeline = pipeline
	}
}

// SetSemanticSearchBatchPlanner wires the real-provider executor for the
// semantic production plan. When set, every run with a consolidated plan
// executes its exact-source ladder once, records hit/miss and latency, and
// publishes the measured report on the result. Nil keeps planning offline.
func (r *Runner) SetSemanticSearchBatchPlanner(planner *SemanticSearchBatchPlanner) {
	if r != nil {
		r.semanticSearchPlanner = planner
	}
}
