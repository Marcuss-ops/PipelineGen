package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/foldermemory"
	localized "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	texttracksport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/portutil"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/texttracks"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
	ytplatform "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube/cache"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
	"go.uber.org/zap"
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
	dbs *Databases,
	log *zap.Logger,
	drive *DriveBundle,
	repos *RepoBundle,
	search *SearchBundle,
	process *ProcessBundle,
	ai *AIBundle,
	outbox *OutboxBundle,
	committer persistence.AssetCommitter,
	bundle *DomainBundle,
	mediaConfig mediaexec.ExecutionConfig,
) (
	voMetaWriter semantic.MetadataWriterPort,
	clipWriter interface {
		youtubeports.ClipAtomicWriter
		localized.LocalizedClipWriter
		texttracksport.TimedCueWriter
	},
	folderPathWriter texttracksport.FolderPathWriter,
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
		return nil, nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort nil (cfg or log missing — fail-closed per PR2)")
	}
	if portutil.IsNilPort(searchRunnerAdapter) {
		return nil, nil, nil, fmt.Errorf("compose domains: youtube SearchRunnerPort typed-nil (portutil.IsNilPort true — fail-closed per PR2)")
	}

	hashAdapter := ytinfra.NewHashAdapter()

	// Use the canonical resolved cookie path for subtitle acquisition; the
	// shared BaseArgs builder still gates --cookies to YouTube URLs.
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// SubtitleFetcherAdapter now consumes the canonical language
	// registry derived from cfg.Media.Multilingual.Languages. The
	// acquisition chain consumes this list as the PreferredLanguages
	// fan-out order and as the SubtitleFetcherAdapter --sub-langs CSV
	// (yt-dlp probes them top-to-bottom).
	mlCfg := ActiveMultilingualConfig(cfg)
	subtitleLanguagesCSV, err := BuildMultilingualLanguageCSV(mlCfg, func(spec asset.LanguageSpec) bool {
		return spec.TranslateClips
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compose domains: subtitle languages: %w", err)
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
	clipCache := imagesregistry.NewClipCacheAdapter(repos.ClipsRepo, log)
	if committer == nil {
		return nil, nil, nil, fmt.Errorf("compose domains: canonical asset committer is required")
	}
	// PR-SINGLE-WRITER (August 2026) + MEDIA DEMOLITION (September 2026):
	// the canonical media writer satisfies youtubeports.ClipAtomicWriter +
	// localized.LocalizedClipWriter + texttracks.TimedCueWriter +
	// youtubeports.ClipMetadataWriter DIRECTLY (port-based assertion —
	// the concrete engine is an implementation detail of the composition
	// root; since the demolition it is always the PostgreSQL committer).
	type canonicalClipWriter interface {
		youtubeports.ClipAtomicWriter
		localized.LocalizedClipWriter
		texttracksport.TimedCueWriter
	}
	canonicalMediaCommitter, ok := committer.(canonicalClipWriter)
	if !ok || canonicalMediaCommitter == nil {
		return nil, nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter must implement ClipAtomicWriter + LocalizedClipWriter (got %T) — single-writer invariant", committer)
	}
	clipWriter = canonicalMediaCommitter
	canonicalMutator, ok := committer.(persistence.AssetMutationCommitter)
	if !ok || canonicalMutator == nil {
		return nil, nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter does not implement AssetMutationCommitter")
	}
	// MEDIA DEMOLITION (September 2026): the canonical media writer
	// implements youtubeports.ClipMetadataWriter directly over the media
	// SSOT — the SQLite-bound ClipMetadataWriterAdapter is retired.
	clipMetadataWriter, ok := committer.(youtubeports.ClipMetadataWriter)
	if !ok || clipMetadataWriter == nil {
		return nil, nil, nil, fmt.Errorf("compose domains: canonical AssetCommitter must implement youtubeports.ClipMetadataWriter (got %T) — single-writer invariant", committer)
	}
	_ = canonicalMutator
	folderPathWriter = &folderPathWriterAdapter{committer: clipWriterFolderPathSource(committer), log: log}
	ollamaBuilderInner := ytinfra.NewOllamaClipMetadataBuilder(
		ai.OllamaClient,
		buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel,
		0,
		log,
	)
	var ollamaBuilder ytmetadata.ClipMetadataBuilder = ollamaBuilderInner
	if dbs.Cache != nil && dbs.Cache.DB != nil {
		if cache, cacheErr := NewArtifactCache(cfg, dbs.Cache.DB, log); cacheErr == nil {
			model := buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel
			if model == "" && ai.OllamaClient != nil {
				model = ai.OllamaClient.Model()
			}
			if model == "" {
				model = "ollama/unknown"
			}
			version := "ollama/" + model
			if decorated, dErr := ytplatform.NewCachedOllamaBuilder(ollamaBuilderInner, cache, version, log); dErr == nil {
				ollamaBuilder = decorated
				log.Info("ollama artifact cache wired for youtube metadata", zap.String("processor_version", version))
			} else {
				log.Warn("ollama artifact cache decorator unavailable", zap.Error(dErr))
			}
		} else {
			log.Warn("ollama artifact cache unavailable; using uncached builder", zap.Error(cacheErr))
		}
	}
	clipMetadataService, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder:  ollamaBuilder,
		Writer:   clipMetadataWriter,
		Logger:   log,
		JobID:    "",
		JobGroup: "general",
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compose domains: clip metadata service: %w", err)
	}

	// Async metadata enrichment (Sept 2026): register the durable consumer
	// for metadata.enrich.requested on the PostgreSQL media outbox. The
	// per-segment pipeline emits that event atomically with the clip commit
	// when the async gate is on, so the LLM analyzer runs OUTSIDE the
	// extraction critical path. godlike/07 fail-closed: when the operator
	// explicitly enables the gate but the consumer cannot be wired, boot
	// aborts instead of silently dropping every enrichment intent.
	if outbox != nil && outbox.MediaIndexWorker != nil {
		enrichHandler, enrichErr := newClipMetadataEnrichHandler(clipMetadataService, log)
		if enrichErr != nil {
			return nil, nil, nil, fmt.Errorf("compose domains: metadata enrichment handler: %w", enrichErr)
		}
		if regErr := outbox.MediaIndexWorker.RegisterHandler(pgmedia.EventMetadataEnrichRequested, enrichHandler); regErr != nil {
			return nil, nil, nil, fmt.Errorf("compose domains: register metadata enrichment handler: %w", regErr)
		}
		log.Info("youtube async metadata enrichment handler registered: metadata.enrich.requested -> AnalyzeClip + atomic metadata/index commit")
	} else if youtube.AsyncEnrichmentEnabled() {
		return nil, nil, nil, fmt.Errorf("compose domains: VELOX_YOUTUBE_ASYNC_ENRICHMENT is enabled but the PostgreSQL media outbox worker is unavailable; refusing to boot with a silent enrichment drop")
	}

	// TextTrackRepository + TextTrackResolver: priority-chain lookup
	// for localized text tracks. Reduces redundant Whisper invocations
	// by checking the API payload and the DB before falling through.
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// TextTrackRepository is now sourced from the canonical
	// RepoBundle.TextTrackRepo (wired in BuildRepoBundle). The
	// pre-PR local construction is removed so every consumer
	// (BuildTextTrackBundle, BuildRepoBundle, the Qdrant
	// PayloadMapper, the TextTrackResolver here) shares the SAME
	// instance — a future refactor that read from a stray local
	// copy would silently corrupt text-track state.
	// MEDIA-SSOT P0-3 (September 2026): TextTrackRepository is PG-owned.
	// The SQLite implementation remains only as the degraded / migration
	// backfill adapter; in PG mode repos.TextTrackRepo is the
	// TextTrackRepositoryPG (composition.go override).
	var _ detail.TextTrackRepository = (*texttracks.TextTrackRepositorySQLite)(nil)
	var _ detail.TextTrackRepository = (*pgmedia.TextTrackRepositoryPG)(nil)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
	// resolver now consumes cfg.Media.Multilingual.RequireLanguageCertainty
	// so the policy gate (asset.ErrLanguageUndeterminable pre-Step-9
	// on full-miss chain) is plumbed end-to-end.
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// SubtitleFetcherAdapter is ALSO exposed on the
	// DomainBundle so composition.go can wire it into the
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
		SegmentsSvc:   youtube.NewSegmentsService(),
		SegmentPolicy: segmentPolicy,
		Log:           log,
	}
	// P0.1 download-once: wire acquisition SourceStager; the stager is always wired but
	// fanout only stages the full source when VELOX_YOUTUBE_DOWNLOAD_ONCE=true|1 or when
	// the batch has >=2 segments and a valid URL (otherwise stageFullSourceOnce no-ops) and each segment cuts locally via
	// PreDownloadedPath (ffmpeg -c copy). Fail-soft: if wiring fails the fanout
	// falls back to per-segment yt-dlp (backwards compatible).
	var youtubeSourceStager acquisition.SourceStager
	{
		ytdlpDL := downloader.NewYTDLP(cfg)
		fetch := func(ctx context.Context, req acquisition.PrepareRequest, dstPath string, _ func(string)) error {
			dlReq := &downloader.DownloadRequest{
				URL:        req.Source.URL,
				OutputPath: dstPath + ".%(ext)s",
				Timeout:    req.Timeout,
				UseCookies: true,
			}
			if req.Source.MergeFormat != "" {
				dlReq.MergeFormat = req.Source.MergeFormat
			} else {
				dlReq.MergeFormat = "mp4"
			}
			if req.Source.DownloadSection != "" {
				dlReq.DownloadSections = []string{req.Source.DownloadSection}
				dlReq.ForceKeyframes = req.Source.ForceKeyframes
			}
			if err := ytdlpDL.Download(ctx, dlReq); err != nil {
				return err
			}
			tmpl := dstPath + ".%(ext)s"
			resolved, rErr := downloader.ResolveDownloadedSegmentPath(tmpl)
			if rErr != nil {
				return rErr
			}
			if resolved != dstPath {
				if err := os.Rename(resolved, dstPath); err != nil {
					return err
				}
			}
			return nil
		}
		if s, sErr := WireAcquisitionStager(cfg, log, fetch); sErr != nil {
			log.Warn("youtube download-once stager unavailable; fanout will use per-segment yt-dlp", zap.Error(sErr))
		} else {
			youtubeSourceStager = s
			log.Info("youtube download-once SourceStager wired", zap.String("staging_root", filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging")))
		}
	}

	processSegMedia := youtube.ProcessSegmentMediaDeps{
		DriveFolderMgr:    youtubePubAdapter,
		TextTrackResolver: textTrackResolver,
		FFProbe:           ytplatform.NewFFProbeAdapter(clipProcessor),
		Stager:            youtubeSourceStager,
		// Sept 2026 single-pass cut contract: the CutModeResolver compares
		// probed source facts against this canonical profile to decide
		// copy-vs-render exactly once per segment.
		CutProfile: mediaConfig.Profile,
	}
	processSegMetadata := youtube.ProcessSegmentMetadataDeps{
		// Phase 2.b atomic super-tx — LocalizedWriter is the SOLE commit
		// contract of the per-segment pipeline (Sept 2026: the legacy
		// ClipAtomicWriter dependency is removed).
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

	// Compile-time pin: MetadataEnrichmentMetrics port ↔
	// observability.MetadataEnrichmentRecorder (Sept 2026 replacement
	// for the retired Step10MetricsAdapter pin). The same recorder family
	// is used by the async metadata.enrich.requested consumer, so the
	// synchronous and asynchronous enrichment surfaces share one
	// collector family (godlike/06 SSOT).
	var _ youtubeports.MetadataEnrichmentMetrics = (*observability.MetadataEnrichmentRecorder)(nil)

	processSegObservability := youtube.ProcessSegmentObservabilityDeps{
		RequireTranscriptReady:         mlCfg.RequireTranscriptReady,
		RequireAllLanguagesBeforeVideo: mlCfg.RequireAllLanguagesBeforeVideo,
		PreferredLanguages:             preferredLangs,
		EnrichmentMetrics:              observability.NewMetadataEnrichmentRecorder(),
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
	// Single-owner concurrency contract (Sept 2026): the youtube concurrency
	// values are resolved EXACTLY ONCE at config-resolution time
	// (buildYouTubeRuntimeConfig); the composition root only passes them
	// through. An explicit operator value (e.g. VELOX_CONCURRENT_VIDEO_EXTRACTS=2)
	// must be honored verbatim — no silent re-normalization here.
	ytRuntimeCfg := buildYouTubeRuntimeConfig(cfg)
	youtubeCore := youtube.ServiceCoreDeps{
		Cfg: ytRuntimeCfg,
		Log: log,
	}
	youtubeAsset := youtube.ServiceAssetDeps{
		AssetRepo:         repos.Assets.Repository(),
		AssetDestResolver: drive.DestResolver,
		LifecycleService: NewLifecycleFromDeps(&AssetLifecycleDeps{
			Registry: artifacts.NewClipsRegistryWithLogger(
				dbs.DualPool.Writer,
				repos.Assets.Repository(),
				repos.Assets,
				repos.Assets.ProcessingRepository(),
				committer,
				log,
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
		return nil, nil, nil, fmt.Errorf("compose youtube: %w", err)
	}
	bundle.YoutubeClipService = youtube.NewServiceFromSubBundles(youtubeCore, youtubeAsset, youtubeVideo, youtubeStorage, youtubeAdapter)

	// PR-YOUTUBE-SERVICE-SPLIT phase-1 typed-narrow skeletons were
	// removed here (September 2026): the `_ =` function-value pins for
	// ytacquisition.Acquirer, transcripts.Transcriber,
	// publication.Publisher and commit.Committer referenced constructors
	// whose Commit/Transcribe/Acquire delegation was never implemented
	// (each returned a phase-2 NotImplemented sentinel or a silent
	// no-op). Dead fake-availability surfaces are a godlike/07
	// violation, so the stubs and their pins are deleted rather than
	// kept as compile-time decoration.
	return voMetaWriter, clipWriter, folderPathWriter, nil
}

// folderPathWriterAdapter bridges the texttracks.FolderPathWriter port
// (3-arg UpdateFolderPath) to the canonical media writer's 5-arg
// UpdateFolderPathTx. MEDIA DEMOLITION (September 2026): the adapter is
// port-based — the concrete engine (PostgreSQL since the demolition) is
// supplied by the composition root.
type folderPathWriterAdapter struct {
	committer interface {
		DB() *sql.DB
		UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error
		CommitIndexEventTx(ctx context.Context, tx *sql.Tx, assetID, source, contentHash, mediaType string) error
	}
	log *zap.Logger
}

// clipWriterFolderPathSource asserts the narrow tx-mutation surface the
// adapter needs from the canonical writer.
func clipWriterFolderPathSource(committer persistence.AssetCommitter) interface {
	DB() *sql.DB
	UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error
	CommitIndexEventTx(ctx context.Context, tx *sql.Tx, assetID, source, contentHash, mediaType string) error
} {
	return committer.(interface {
		DB() *sql.DB
		UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error
		CommitIndexEventTx(ctx context.Context, tx *sql.Tx, assetID, source, contentHash, mediaType string) error
	})
}

func (a *folderPathWriterAdapter) UpdateFolderPath(ctx context.Context, assetID, folderPath string) error {
	if a == nil || a.committer == nil {
		return fmt.Errorf("folderPathWriterAdapter: committer not wired")
	}
	tx, err := a.committer.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var sourceVersion string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_version,'') FROM media_assets WHERE id=$1`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("folderPathWriterAdapter: asset: %w", err)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if err := a.committer.UpdateFolderPathTx(ctx, tx, assetID, "", folderPath, updatedAt); err != nil {
		return err
	}
	if err := a.committer.CommitIndexEventTx(ctx, tx, assetID, "youtube", sourceVersion, "video"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// buildBcp47CSV was removed: unused after the SubtitleFetcherAdapter wiring
// collapsed to the config-driven path.
