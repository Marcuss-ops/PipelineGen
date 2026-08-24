package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/commit"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/publication"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	ytacquisition "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/hashutil"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	ytplatform "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
	"go.uber.org/zap"
)

// buildDomainMediaServices constructs the YouTube clip pipeline service
// and populates the wiring.DomainBundle with it. Returns intermediate deps
// consumed by other domain sections.
//
// godlike/06 SSOT: the YouTube Service and its adapters are the SOLE
// canonical owners of the clip extraction pipeline.
func buildDomainMediaServices(
	ctx context.Context,
	cfg *config.Config,
	dbs *wiring.Databases,
	log *zap.Logger,
	drive *wiring.DriveBundle,
	repos *wiring.RepoBundle,
	search *wiring.SearchBundle,
	process *wiring.ProcessBundle,
	ai *wiring.AIBundle,
	outbox *wiring.OutboxBundle,
	committer persistence.AssetCommitter,
	bundle *wiring.DomainBundle,
	mediaConfig mediaexec.ExecutionConfig,
) (
	voMetaWriter semantic.MetadataWriterPort,
	clipWriter *assets.ClipAtomicWriterAdapter,
	err error,
) {
	// P0-#2 (July 2026): the composition root no longer constructs
	// semantic.NewMetadataWriter(...) (the retired fake concrete). The
	// metadata writer capability is now wired ONLY via the
	// semantic.MetadataWriterPort port — the production composition
	// passes nil when the real semantic tagger is absent. Consumers
	// nil-check the port at the call site (e.g., buildVoiceoverService
	// returns an error on nil, SemanticEnricher.Enrich returns an
	// error on nil, MetadataService.tagImageMetadata returns
	// (nil, nil) on nil). godlike/07 NO-FAKE-AVAILABILITY: nil is the
	// correct signal for 'this capability is not available' — callers
	// cannot accidentally observe a synthetic Payload from a disabled
	// stub. The real implementation (P0.18 follow-up) will replace
	// this nil with a real *ollama.TaggerAdapter that structurally
	// satisfies semantic.MetadataWriterPort.
	voMetaWriter = nil

	clipProcessor := rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)
	videoPipelineAdapter := ytinfra.NewVideoPipelineAdapter(videoPipeline)

	folderMemSvc := foldermemory.NewService(log, repos.ClipsRepo)
	_ = voMetaWriter // P0-#2: voMetaWriter is nil in production (no real semantic tagger); retained on the return tuple for forward-compat with future real-implementation wiring
	metaFetcher := ytinfra.NewMetadataFetcherAdapter(cfg, nil)
	youtubePubAdapter := ytadapters.NewYouTubePublisherDriveAdapter(drive.Publisher, log)
	youtubeCache := ytcache.NewService(ytcache.Deps{DB: repos.ClipsRepo.DB(), Log: log})

	var clipIndexerAdapterValue youtubeports.ClipIndexerPort
	if process.ClipIndexerService != nil {
		clipIndexerAdapterValue = ytadapters.NewClipIndexerAdapter(process.ClipIndexerService)
	}

	searchRunnerAdapter := ytinfra.NewSearchRunnerAdapter(cfg, log)
	if searchRunnerAdapter == nil {
		return nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort nil (cfg or log missing — fail-closed per PR2)")
	}
	if portutil.IsNilPort(searchRunnerAdapter) {
		return nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort typed-nil (portutil.IsNilPort true — fail-closed per PR2)")
	}

	hashAdapter := hashutil.NewHashAdapter()

	// Use the canonical resolved cookie path for subtitle acquisition; the
	// shared BaseArgs builder still gates --cookies to YouTube URLs.
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// SubtitleFetcherAdapter now consumes the canonical language
	// registry derived from cfg.Media.Multilingual.Languages. The
	// acquisition chain consumes this list as the PreferredLanguages
	// fan-out order and as the SubtitleFetcherAdapter --sub-langs CSV
	// (yt-dlp probes them top-to-bottom).
	mlCfg := wiring.ActiveMultilingualConfig(cfg)
	subtitleLanguagesCSV, err := wiring.BuildMultilingualLanguageCSV(mlCfg, func(spec asset.LanguageSpec) bool {
		return spec.TranslateClips
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compose domains: subtitle languages: %w", err)
	}
	subtitleFetcherAdapter := ytinfra.NewSubtitleFetcherAdapter(
		ytinfra.SubtitleCacheConfig{
			YTDLPPath:    cfg.External.ResolvedYtdlpPath(),
			DefaultLangs: subtitleLanguagesCSV,
			CacheDir:     cfg.Storage.SubtitlesPath(),
		},
		nil,
		ytdlp.NewCommandBuilder(cfg),
		cfg.External.ResolveYouTubeCookiesPath() != "",
	)
	clipCache := assets.NewClipCacheAdapter(repos.ClipsRepo, log)
	clipWriter = assets.NewClipAtomicWriterAdapterWithCommitter(
		dbs.DualPool.Writer,
		outbox.EventsRepo,
		newCanonicalAssetCommitter(dbs.DualPool.Writer, outbox.EventsRepo, log),
		log,
	)
	clipMetadataWriter := assets.NewClipMetadataWriterAdapter(dbs.DualPool.Writer, outbox.EventsRepo, log)
	ollamaBuilder := ytinfra.NewOllamaClipMetadataBuilder(
		ai.OllamaClient,
		buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel,
		0,
		log,
	)
	clipMetadataService, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder:  ollamaBuilder,
		Writer:   clipMetadataWriter,
		Logger:   log,
		JobID:    "",
		JobGroup: "general",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compose domains: clip metadata service: %w", err)
	}

	// Compile-time pin: Step10MetricsRecorder port ↔ Step10MetricsAdapter.
	var _ youtubeports.Step10MetricsRecorder = (*observability.Step10MetricsAdapter)(nil)
	// TextTrackRepository + TextTrackResolver: priority-chain lookup
	// for localized text tracks. Reduces redundant Whisper invocations
	// by checking the API payload and the DB before falling through.
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// TextTrackRepository is now sourced from the canonical
	// wiring.RepoBundle.TextTrackRepo (wired in BuildRepoBundle). The
	// pre-PR local construction is removed so every consumer
	// (wiring.BuildTextTrackBundle, BuildRepoBundle, the Qdrant
	// PayloadMapper, the TextTrackResolver here) shares the SAME
	// instance — a future refactor that read from a stray local
	// copy would silently corrupt text-track state.
	// Compile-time pin: TextTrackRepositorySQLite satisfies asset.TextTrackRepository.
	var _ asset.TextTrackRepository = (*texttracks.TextTrackRepositorySQLite)(nil)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// resolver now consumes cfg.Media.Multilingual.RequireLanguageCertainty
	// so the policy gate (asset.ErrLanguageUndeterminable pre-Step-9
	// on full-miss chain) is plumbed end-to-end.
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// SubtitleFetcherAdapter is ALSO exposed on the
	// wiring.DomainBundle so composition.go can wire it into the
	// AcquireService (backfill CLI 5-priority chain —
	// priorities 3+4: YouTube subtitles). The narrow
	// texttracks.SubtitlesPort interface is a structural
	// subset of the full youtubeports.SubtitleFetcherPort.
	bundle.SubtitleFetcher = subtitleFetcherAdapter
	textTrackResolver := &youtube.TextTrackResolver{
		Repo:        repos.TextTrackRepo,
		Subtitles:   subtitleFetcherAdapter, // satisfies youtubeports.SubtitleFetcherPort at wire-time (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a)
		Transcriber: ai.WhisperTranscriber,
		Log:         log,
		// The certainty gate stays config-driven; Whisper is the
		// fallback when subtitles are unavailable or unusable.
		RequireLanguageCertainty: mlCfg.RequireLanguageCertainty,
	}

	// PR-GRUPOC-2 (July 2026): youtube.ProcessSegmentDeps (17 fields) is
	// RETIRED. The 17 fields are now split into 4 capability-area
	// sub-bundles (Core/Media/Metadata/Observability) — each ≤7 fields
	// — to clear percheck_struct_deps ≤8 enforcement. The
	// composition-root wiring below sources the 17 fields from the
	// same canonical production dep set the previous ProcessSegmentDeps
	// literal used; no port is added, dropped, or renamed.
	// Subtitles / Stager are intentionally left zero (matches the
	// previous literal's behaviour; those optional ports are not
	// exercised by the YouTube orchestrator at composition time).
	//
	// FFProbe is WIRED (Aug 2026 stub-recovery): Step 5a is the
	// fail-closed gate that rejects a 262-byte empty MP4 stub (no video
	// stream / zero duration) produced when a bot-checked yt-dlp section
	// download exits zero but writes no media data. Before wiring, the
	// Step 5 gate (size > 0) accepted the stub and the job "succeeded"
	// with a dead artifact on Drive + Qdrant. The gate reuses the same
	// shared media probe (rustexec) that cut_and_normalize uses, so the
	// validation and the encode agree on the same execution plane.
	// SegmentPolicy is config-driven: the canonical default is 4-60s
	// (DefaultSegmentPolicy); production raises the max via
	// cfg.Jobs.YoutubeMaxSegmentDurationSeconds (e.g. 120 for
	// 2-minute clips) without mutating the shared default.
	segmentPolicy := youtubetypes.DefaultSegmentPolicy()
	if maxDur := cfg.Jobs.YoutubeMaxSegmentDurationSeconds; maxDur > 0 {
		segmentPolicy.MaxDuration = maxDur
	}
	processSegCore := youtube.ProcessSegmentCoreDeps{
		Cache:         clipCache,
		VideoPipeline: videoPipelineAdapter,
		Hash:          hashAdapter,
		Writer:        clipWriter, // legacy writer for non-localized callers
		SegmentsSvc:   youtube.NewSegmentsService(),
		SegmentPolicy: segmentPolicy,
		Log:           log,
	}
	processSegMedia := youtube.ProcessSegmentMediaDeps{
		DriveFolderMgr:    youtubePubAdapter,
		TextTrackResolver: textTrackResolver,
		FFProbe:           ytplatform.NewFFProbeAdapter(clipProcessor),
	}
	processSegMetadata := youtube.ProcessSegmentMetadataDeps{
		// Phase 2.b atomic super-tx (clipWriter satisfies both ports —
		// see clip_atomic_writer.go compile-time assertion).
		LocalizedWriter:    clipWriter,
		ClipMetadataWriter: clipMetadataWriter,
		MetadataService:    clipMetadataService,
	}
	var preferredLangs []string
	for _, spec := range mlCfg.Languages {
		if spec.Enabled && spec.TranslateClips {
			preferredLangs = append(preferredLangs, spec.Code)
		}
	}

	processSegObservability := youtube.ProcessSegmentObservabilityDeps{
		Step10Metrics:                  observability.NewStep10MetricsAdapter(),
		RequireTranscriptReady:         mlCfg.RequireTranscriptReady,
		RequireAllLanguagesBeforeVideo: mlCfg.RequireAllLanguagesBeforeVideo,
		PreferredLanguages:             preferredLangs,
	}
	processSeg := youtube.NewProcessYouTubeSegmentFromSubBundles(
		processSegCore,
		processSegMedia,
		processSegMetadata,
		processSegObservability,
	)

	// PR-GRUPOC-1 (July 2026): youtube.ServiceDeps (20 fields) is RETIRED.
	// The 20 fields are now split into 5 capability-area sub-bundles
	// (Core/Asset/Video/Storage/Adapter) — each ≤7 fields — to clear
	// percheck_struct_deps ≤8 enforcement. The composition-root wiring
	// below sources the 20 fields from the same canonical production
	// dep set the previous ServiceDeps literal used; no port is added,
	// dropped, or renamed. ClipFiles + Whisper are intentionally left
	// nil (matches the previous literal's behaviour; those ports are
	// not exercised by the YouTube orchestrator at composition time).
	youtubeCore := youtube.ServiceCoreDeps{
		Cfg: buildYouTubeRuntimeConfig(cfg),
		Log: log,
	}
	youtubeAsset := youtube.ServiceAssetDeps{
		AssetRepo:         repos.Assets.Repository(),
		AssetDestResolver: drive.DestResolver,
		LifecycleService: NewLifecycleFromDeps(&AssetLifecycleDeps{
			Registry: artifacts.NewClipsRegistry(
				dbs.DualPool.Writer,
				repos.Assets.Repository(),
				repos.Assets,
				repos.Assets.LocationRepository(),
				repos.Assets.ProcessingRepository(),
				newCanonicalAssetCommitter(dbs.DualPool.Writer, outbox.EventsRepo, log),
			),
			Publisher:   drive.Publisher,
			DriveReader: drive.DriveUploader,
			AssetIndex:  search.AssetIndexService,
		}, log),
		MediaProcessor: process.MediaProcessor,
	}
	youtubeVideo := youtube.ServiceVideoDeps{
		VideoPipeline: videoPipelineAdapter,
		ProcessSeg:    processSeg,
	}
	youtubeStorage := youtube.ServiceStorageDeps{
		Clips:            ytadapters.NewClipStoreAdapter(repos.ClipsRepo),
		Cache:            youtubeCache,
		Monitors:         ytadapters.NewMonitorsStoreAdapter(repos.MonitorsRepo),
		Indexer:          clipIndexerAdapterValue,
		Ollama:           ai.OllamaClient,
		FolderMemory:     ytadapters.NewFolderMemoryAdapter(folderMemSvc),
		TranscriptReader: &youtube.OSTranscriptReader{},
	}
	youtubeAdapter := youtube.ServiceAdapterDeps{
		SearchRunner:    searchRunnerAdapter,
		HashSvc:         hashAdapter,
		SubtitleFetcher: subtitleFetcherAdapter,
		MetaFetcher:     metaFetcher,
		DriveFolderMgr:  youtubePubAdapter,
	}
	if err := youtube.ValidateServiceDepsFromSubBundles(youtubeCore, youtubeAsset, youtubeVideo, youtubeStorage, youtubeAdapter); err != nil {
		return nil, nil, fmt.Errorf("compose youtube: %w", err)
	}
	bundle.YoutubeClipService = youtube.NewServiceFromSubBundles(youtubeCore, youtubeAsset, youtubeVideo, youtubeStorage, youtubeAdapter)

	// PR-YOUTUBE-SERVICE-SPLIT (July 2026): composition-root wiring
	// for the 6 typed-narrow packages (godlike/06 SSOT). Phase 1
	// validates the wiring at compile time via function-value
	// references — each entry resolves the package + constructor
	// symbol at build time; any signature drift surfaces as a
	// compile failure rather than a runtime nil-port panic. Phase 2
	// (separate commit) consumes the typed-narrow ports directly
	// here and promotes the function-value references to actual
	// construction calls (with godlike/07 fail-closed nil-port
	// guards + typed sentinels). godlike/06 SSOT one-canonical-
	// owner-per-fact: each new package owns exactly one canonical
	// contract (Acquirer, Metadata, Transcriber, Publisher,
	// Committer, Recommender); the legacy canonical impl
	// (youtube.Service, WhisperTranscriberAdapter, PublishClipToDrive,
	// AssetTxFinalizer) stays untouched in this commit.
	var (
		_ = ytacquisition.NewServiceAdapter
		_ = transcripts.NewWhisperAdapter
		_ = publication.NewDriveAdapter
		_ = commit.NewTxAdapter
	)

	return voMetaWriter, clipWriter, nil
}

// buildBcp47CSV was removed: unused after the SubtitleFetcherAdapter wiring
// collapsed to the config-driven path.
