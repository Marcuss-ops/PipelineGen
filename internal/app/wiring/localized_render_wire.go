package wiring

import (
	clipswiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring/localizedrender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// wireLocalizedRenderEnqueuer wires the runner's localized-render fan-out to
// the canonical LocalizationService when the full localization stack is
// available. It is best-effort: a missing asset registry, Drive publisher/doc
// client, text-track store, or cue writer leaves the fan-out unwired (a
// legitimate no-op) rather than failing composition — the runner only treats a
// nil enqueuer as "render not registered".
func wireLocalizedRenderEnqueuer(cfg *config.Config, root *ComposeRoot, log *zap.Logger, runner *scriptgeneration.Runner) {
	if runner == nil {
		return
	}
	if cfg == nil || root == nil || root.Repos == nil || root.Repos.TextTrackRepo == nil || (root.Domains == nil || root.Domains.CueWriter == nil) || root.CanonicalAssetWriter == nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (text-track store or cue writer missing)")
		return
	}
	svc, err := BuildLocalizationService(cfg, root, log)
	if err != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (localization service unavailable)", zap.Error(err))
		return
	}
	resolver, resolverErr := clipswiring.NewClipRenderMediaResolver(root.MediaPostgres, log)
	if resolverErr != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (asset resolver unavailable)", zap.Error(resolverErr))
		return
	}
	materializer, materializerErr := clipadapters.NewClipRenderMaterializer(root.Drive.Reader, clipswiring.AssetMaterializationRoot(cfg), log)
	if materializerErr != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (asset materializer unavailable)", zap.Error(materializerErr))
		return
	}
	// Reuse the canonical clip-render transcript resolver. It first reads the
	// READY timed transcript from SQLite; only a cache miss may invoke Whisper,
	// and the generated result is persisted by that resolver.
	transcriptResolver := clipadapters.NewClipRenderTranscriptResolver(log)
	transcriptResolver.SetRepo(root.Repos.TextTrackRepo)
	if root.TextTracks != nil {
		transcriptResolver.SetAcquire(root.TextTracks.AcquireService)
	}
	if root.Domains != nil {
		transcriptResolver.SetCueWriter(root.Domains.CueWriter)
	}
	if streaming, streamErr := clipadapters.NewClipRenderStreamingTranscriber(cfg, log); streamErr == nil {
		streaming.SetArtifactCache(clipRenderArtifactCache(cfg, root, log), whisperBridgeVersion("scripts/bridges/whisper_transcriber.py"))
		transcriptResolver.SetStreaming(streaming)
	} else {
		log.Warn("wireScriptFlow: streaming subtitle generation unavailable; using acquisition fallback", zap.Error(streamErr))
	}
	adapter := localizedrender.NewLocalizedRenderEnqueuerAdapter(svc, root.Repos.TextTrackRepo, root.Domains.CueWriter, localizedrender.LocalizedRenderEnqueuerConfig{
		SourceLanguage:    localizedrender.LocalizationConfigFromConfig(cfg).SourceLanguage,
		FolderID:          cfg.Drive.ClipsFolder(),
		FolderAdmin:       root.Drive.Admin,
		JobBroker:         root.Jobs.Repo,
		SubtitleFolderID:  "1noSFMK_UeF_Xo-RRZWvH10U7tiL1jPP1",
		Concurrency:       cfg.Scripts.LocalizedRenderConcurrency,
		GlobalConcurrency: cfg.Scripts.LocalizedRenderGlobalConcurrency,
	}, log, resolver, materializer, transcriptResolver, root.Repos.SubtitleArtifactRepo, root.CanonicalAssetWriter)
	// A requested language whose track the fan-out has not created yet is
	// materialized on demand through the SAME canonical pipeline as
	// asset.text.materialize and the operator backfill. Without this seam a
	// first render could fail closed even though the source transcript is
	// already READY and the translation is one call away.
	if root.TextTracks != nil && root.TextTracks.JobHandler != nil {
		if backfill := root.TextTracks.JobHandler.Backfill(); backfill != nil {
			adapter.SetSubtitleMaterializer(backfill)
		} else {
			log.Warn("wireScriptFlow: on-demand localized subtitle materialization not wired (text-track backfill unavailable)")
		}
	}
	runner.SetLocalizedRenderEnqueuer(adapter)
	log.Info("wireScriptFlow: localized render fan-out wired to LocalizationService (RenderingGen/Chronon)",
		zap.String("source_language", localizedrender.LocalizationConfigFromConfig(cfg).SourceLanguage),
		zap.String("clips_folder", cfg.Drive.ClipsFolder()))
}
