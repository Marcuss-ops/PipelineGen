package app

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/hashutil"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// buildDomainMediaServices constructs the YouTube clip pipeline service
// and populates the DomainBundle with it. Returns intermediate deps
// consumed by other domain sections.
//
// godlike/06 SSOT: the YouTube Service and its adapters are the SOLE
// canonical owners of the clip extraction pipeline.
func buildDomainMediaServices(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	drive *DriveBundle,
	repos *RepoBundle,
	search *SearchBundle,
	process *ProcessBundle,
	ai *AIBundle,
	outbox *OutboxBundle,
	mutationsDisp mutations.AssetMutationDispatcher,
	bundle *DomainBundle,
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

	clipProcessor := pkgffmpeg.NewFromConfig(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)
	videoPipelineAdapter := ytinfra.NewVideoPipelineAdapter(videoPipeline)

	folderMemSvc := foldermemory.NewService(log, repos.ClipsRepo)
	_ = voMetaWriter // P0-#2: voMetaWriter is nil in production (no real semantic tagger); retained on the return tuple for forward-compat with future real-implementation wiring
	metaFetcher := ytinfra.NewMetadataFetcherAdapter(cfg, nil)
	youtubePubAdapter := NewYouTubePublisherDriveAdapter(drive.Publisher, log)
	youtubeCache := ytcache.NewService(ytcache.Deps{DB: repos.ClipsRepo.DB(), Log: log})

	var clipIndexerAdapterValue youtubeports.ClipIndexerPort
	if process.ClipIndexerService != nil {
		clipIndexerAdapterValue = &clipIndexerAdapter{inner: process.ClipIndexerService}
	}

	searchRunnerAdapter := ytinfra.NewSearchRunnerAdapter(cfg, log)
	if searchRunnerAdapter == nil {
		return nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort nil (cfg or log missing — fail-closed per PR2)")
	}
	if portutil.IsNilPort(searchRunnerAdapter) {
		return nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort typed-nil (portutil.IsNilPort true — fail-closed per PR2)")
	}

	hashAdapter := hashutil.NewHashAdapter()

	// UseCookies=false: public video segmentation (n-challenge path via monitor).
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// SubtitleFetcherAdapter no longer silently defaults to "en,en-US".
	// The default-languages list is the config-driven
	// cfg.Media.Multilingual.MaterializeLanguages (BCP-47 list,
	// comma-separated), normalized via the canonical asset.Normalize
	// helper. Empty config collapses to "" (NOT "en") so the
	// acquisition chain surfaces "und" (BCP-47 undetermined) per
	// godlike/07 no-fake-availability. The acquisition chain
	// (TextTrackResolver.AcquireSegmentText) consumes this list as
	// the PreferredLanguages fan-out order and as the
	// SubtitleFetcherAdapter --sub-langs CSV (yt-dlp probes them
	// top-to-bottom).
	subtitleLanguagesCSV := buildBcp47CSV(cfg.Multilingual.MaterializeLanguages)
	subtitleFetcherAdapter := ytinfra.NewSubtitleFetcherAdapter(
		ytinfra.SubtitleCacheConfig{
			YTDLPPath:    cfg.External.ResolvedYtdlpPath(),
			DefaultLangs: subtitleLanguagesCSV,
			CacheDir:     cfg.Storage.SubtitlesPath(),
		},
		nil,
		ytdlp.NewCommandBuilder(cfg),
		false,
	)
	clipCache := assets.NewClipCacheAdapter(repos.ClipsRepo, log)
	clipWriter = assets.NewClipAtomicWriterAdapter(dbs.main.DB, outbox.EventsRepo, log)
	clipMetadataWriter := assets.NewClipMetadataWriterAdapter(dbs.main.DB, outbox.EventsRepo, log)
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
	textTrackRepo, err := assets.NewTextTrackRepository(dbs.main.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("compose domains: text track repository: %w", err)
	}
	// Compile-time pin: TextTrackRepositorySQLite satisfies asset.TextTrackRepository.
	var _ asset.TextTrackRepository = (*assets.TextTrackRepositorySQLite)(nil)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// resolver now consumes cfg.Media.Multilingual.RequireLanguageCertainty
	// so the policy gate (asset.ErrLanguageUndeterminable pre-Step-9
	// on full-miss chain) is plumbed end-to-end.
	textTrackResolver := &youtube.TextTrackResolver{
		Repo:                     textTrackRepo,
		Subtitles:                subtitleFetcherAdapter, // satisfies youtubeports.SubtitleFetcherPort at wire-time (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a)
		Transcriber:              nil,                   // no concrete whisper wired in production composition; resolver.AcquireSegmentText skips priority 5 when nil (Fase 1.b will add the adapter)
		Log:                      log,
		RequireLanguageCertainty: cfg.Multilingual.RequireLanguageCertainty,
	}

	processSeg := youtube.NewProcessYouTubeSegmentUseCase(youtube.ProcessSegmentDeps{
		Cache:              clipCache,
		VideoPipeline:      videoPipelineAdapter,
		Hash:               hashAdapter,
		DriveFolderMgr:     youtubePubAdapter,
		Writer:             clipWriter, // legacy Stripe for non-localized callers
		LocalizedWriter:    clipWriter, // Phase 2.b atomic super-tx (clipWriter satisfies both ports — see clip_atomic_writer.go compile-time assertion)
		ClipMetadataWriter: clipMetadataWriter,
		MetadataService:    clipMetadataService,
		SegmentsSvc:        youtube.NewSegmentsService(),
		SegmentPolicy:      youtubetypes.DefaultSegmentPolicy(),
		Step10Metrics:      observability.NewStep10MetricsAdapter(),
		TextTrackResolver:  textTrackResolver,
		Log:                log,
	})

	youtubeDeps := youtube.ServiceDeps{
		Cfg:            buildYouTubeRuntimeConfig(cfg),
		Log:            log,
		MediaProcessor: process.MediaProcessor,
		VideoPipeline:  videoPipelineAdapter,
		LifecycleService: NewLifecycleFromDeps(&LifecycleDeps{
			Registry: artifacts.NewClipsRegistry(
				dbs.main.DB,
				repos.Assets.Repository(),
				repos.Assets,
				repos.Assets.LocationRepository(),
				repos.Assets.ProcessingRepository(),
				mutationsDisp,
			),
			Publisher:   drive.Publisher,
			DriveReader: drive.driveUploader,
			AssetIndex:  search.AssetIndexService,
		}, log),
		AssetDestResolver: drive.DestResolver,
		AssetRepo:         repos.Assets.Repository(),
		Clips:             newClipStoreAdapter(repos.ClipsRepo),
		Cache:             youtubeCache,
		Monitors:          newMonitorsStoreAdapter(repos.MonitorsRepo),
		Indexer:           clipIndexerAdapterValue,
		Ollama:            ai.OllamaClient,
		MetaFetcher:       metaFetcher,
		DriveFolderMgr:    youtubePubAdapter,
		FolderMemory:      newFolderMemoryAdapter(folderMemSvc),
		SearchRunner:      searchRunnerAdapter,
		HashSvc:           hashAdapter,
		ProcessSeg:        processSeg,
		TranscriptReader:  &youtube.OSTranscriptReader{},
		SubtitleFetcher:   subtitleFetcherAdapter,
	}
	if err := youtube.ValidateServiceDeps(youtubeDeps); err != nil {
		return nil, nil, fmt.Errorf("compose youtube: %w", err)
	}
	bundle.YoutubeClipService = youtube.NewService(youtubeDeps)

	return voMetaWriter, clipWriter, nil
}

// buildBcp47CSV normalizes a config-driven BCP-47 list into the
// comma-separated string yt-dlp's --sub-langs expects (e.g.
// "it,en,es,pt-BR,fr,de"). godlike/07 NO-FAKE-AVAILABILITY: empty
// or invalid entries are SILENTLY SKIPPED — the helper MUST NOT
// default to "en" or any other language. Empty input collapses to
// an empty string (which is the canonical "no preference" signal
// at the SubtitleFetcherAdapter layer; FetchSegmentSubtitles
// surfaces "und" when no langs are configured).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026). The helper
// is the single canonical conversion point; callers MUST NOT
// re-implement CSV joining inline.
func buildBcp47CSV(codes []string) string {
	var out []string
	for _, raw := range codes {
		normalized, err := asset.Normalize(raw)
		if err != nil || normalized == "und" {
			continue
		}
		out = append(out, normalized)
	}
	return strings.Join(out, ",")
}
