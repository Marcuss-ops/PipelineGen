package wiring

import (
	"context"
	"fmt"
	"strings"

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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
// The construction is a sequence of named, cohesive steps (see the Step
// markers below), each owning exactly one concern:
//
//	Step 1  acquisition + execution ports
//	Step 2  boot gate: YouTube cookie-jar format
//	Step 3  canonical single-writer contract (explicit committer dependency)
//	Step 4  subtitle acquisition path
//	Step 5  clip metadata service + async enrichment registration
//	Step 6  text-track resolver
//	Step 7  sections-only source staging
//	Step 8  per-segment pipeline assembly
//	Step 9  clip service assembly
//
// The steps are ordinary function calls, NOT new business rules and NOT a
// dependency-injection framework: every dependency is still resolved here, in
// the composition root, and threaded explicitly — including the canonical
// persistence.AssetCommitter, which is asserted once in Step 3 and then handed
// to the writers that need the single-writer surface.
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

	// ── Step 1: acquisition + execution ports ───────────────────────────
	clipProcessor := rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)
	videoPipelineAdapter := ytinfra.NewVideoPipelineAdapter(videoPipeline)

	folderMemSvc := foldermemory.NewService(log, repos.ClipsRepo)
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

	// ── Step 2: boot gate — cookie-jar format (fail loud) ───────────────
	if err := validateYouTubeCookieBootGate(cfg, log); err != nil {
		return nil, nil, nil, err
	}

	hashAdapter := ytinfra.NewHashAdapter()

	// ── Step 3: canonical single-writer contract ────────────────────────
	// The canonical committer is the ONE media writer; assert every surface
	// the pipeline needs from it here, at boot, instead of failing later on
	// the first write. PR-SINGLE-WRITER (August 2026) + MEDIA DEMOLITION
	// (September 2026): the canonical media writer satisfies
	// youtubeports.ClipAtomicWriter + localized.LocalizedClipWriter +
	// texttracks.TimedCueWriter + youtubeports.ClipMetadataWriter DIRECTLY
	// (port-based assertion — the concrete engine is an implementation
	// detail of the composition root; since the demolition it is always the
	// PostgreSQL committer).
	canonicalWriter, clipMetadataWriter, writerErr := assertCanonicalMediaWriter(committer)
	if writerErr != nil {
		return nil, nil, nil, writerErr
	}
	clipWriter = canonicalWriter
	folderPathWriter = &folderPathWriterAdapter{committer: clipWriterFolderPathSource(committer), log: log}

	// ── Step 4: subtitle acquisition path ───────────────────────────────
	mlCfg := ActiveMultilingualConfig(cfg)
	// ACQUISITION vs MATERIALIZATION (Sept 2026): the subtitle fetcher resolves
	// ONE source-language track — never the configured translation set. Wiring
	// every target language here (it,en,pl,ru,de,es,pt-BR,fr,tr,id) made yt-dlp
	// request all ten subtitle tracks in a single call, hit HTTP 429 on the
	// first one and abort the entire fetch, so the clip ended up with NO
	// transcript; the nine translations are produced later in the materializer
	// by Argos/Ollama and must not be requested from YouTube.
	subtitleFetcherAdapter := buildYouTubeSubtitleFetcher(cfg, mlCfg)
	clipCache := imagesregistry.NewClipCacheAdapter(repos.ClipsRepo, log)

	// ── Step 5: clip metadata service + async enrichment ────────────────
	clipMetadataService, metaErr := buildClipMetadataService(cfg, dbs, ai, clipMetadataWriter, log)
	if metaErr != nil {
		return nil, nil, nil, metaErr
	}

	// Async metadata enrichment (Sept 2026): register the durable consumer
	// for metadata.enrich.requested on the PostgreSQL media outbox. The
	// per-segment pipeline emits that event atomically with the clip commit
	// when the async gate is on, so the LLM analyzer runs OUTSIDE the
	// extraction critical path. godlike/07 fail-closed: when the operator
	// explicitly enables the gate but the consumer cannot be wired, boot
	// aborts instead of silently dropping every enrichment intent.
	if enrichErr := registerClipMetadataEnrichment(outbox, clipMetadataService, log); enrichErr != nil {
		return nil, nil, nil, enrichErr
	}

	// ── Step 6: text-track resolver ─────────────────────────────────────
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
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// SubtitleFetcherAdapter is ALSO exposed on the
	// DomainBundle so composition.go can wire it into the
	// AcquireService (backfill CLI 5-priority chain —
	// priorities 3+4: YouTube subtitles). The narrow
	// texttracks.SubtitlesPort interface is a structural
	// subset of the full youtubeports.SubtitleFetcherPort.
	bundle.SubtitleFetcher = subtitleFetcherAdapter
	textTrackResolver := buildTextTrackResolver(mlCfg, repos, ai, subtitleFetcherAdapter, log)

	// ── Step 7: sections-only source staging ────────────────────────────
	// The fanout merges the requested segment windows into contiguous blocks
	// and stages each block with ONE yt-dlp --download-sections call, so an
	// extraction downloads only the seconds it publishes instead of the whole
	// source; every segment then cuts locally from its block
	// (PreDownloadedPath + PreDownloadedOffsetSec). Fail-soft: a block that
	// fails to stage leaves its segments on the per-segment yt-dlp path
	// (backwards compatible).
	youtubeSourceStager := buildYouTubeSourceStager(cfg, log)

	// ── Step 8: per-segment pipeline assembly ───────────────────────────
	processSeg := buildProcessYouTubeSegment(
		cfg, mlCfg, mediaConfig,
		clipProcessor, videoPipelineAdapter, hashAdapter, clipCache,
		youtubePubAdapter, textTrackResolver, youtubeSourceStager,
		clipWriter, clipMetadataWriter, clipMetadataService,
		log,
	)

	// ── Step 9: clip service assembly ───────────────────────────────────
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
		// Media SSOT persistence port: the canonical dispatcher (media_assets +
		// index request in one media-SSOT transaction) whenever the canonical
		// writer is present; the SQLite repository is the media-disabled
		// degrade fallback only.
		AssetRepo:         newYouTubeAssetWriter(outbox, repos, committer),
		AssetDestResolver: drive.DestResolver,
		LifecycleService: NewLifecycleFromDeps(&AssetLifecycleDeps{
			// MEDIA-SSOT P2-9 step 2: the registry hydrates from the canonical
			// committer's engine rather than the operational SQLite mirror.
			// MEDIA-SSOT write-bridge: asset_processing is media-authoritative,
			// so the registry's step-progress port resolves from the canonical
			// committer's engine rather than the operational SQLite store.
			Registry: artifacts.NewClipsRegistryWithLogger(
				mediaDetailsReaderFromCommitter(committer),
				persistence.CanonicalAssetProcessingWriter(committer),
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
		ClipLister:       mediaYouTubeClipListerFromCommitter(committer),
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

// validateYouTubeCookieBootGate raises the cookie-jar FORMAT gate (fail loud,
// at boot). Every YouTube leg hands the resolved jar to yt-dlp through
// BaseArgs; a jar yt-dlp cannot parse aborts each invocation with "does not
// look like a Netscape format cookies file". That verdict used to reach the
// operator only as an unrelated 5xx on whichever endpoint happened to touch
// YouTube first (observed: 503 on GET /api/clips/search and on every stock
// acquisition), so it is raised here instead — the jar is the one YouTube
// input this process cannot repair at runtime.
//
// Scoped to a YouTube-ENABLED deployment: a jar left behind by a disabled
// feature must not block an unrelated boot, but it is reported rather than
// silently ignored.
func validateYouTubeCookieBootGate(cfg *config.Config, log *zap.Logger) error {
	cookieJar := cfg.External.ResolveYouTubeCookiesPath()
	cookieJarErr := ytdlp.ValidateCookiesFile(cookieJar)
	switch {
	case cookieJarErr != nil && cfg.Features.YouTubeEnabled:
		return fmt.Errorf("compose domains: %w", cookieJarErr)
	case cookieJarErr != nil:
		log.Warn("youtube cookies file is unusable but the YouTube feature is disabled; continuing",
			zap.String("path", cookieJar), zap.Error(cookieJarErr))
	}
	return nil
}

// buildYouTubeSubtitleFetcher resolves the acquisition-side subtitle fetcher:
// the canonical language registry derived from cfg.Media.Multilingual.Languages
// becomes the SubtitleFetcherAdapter --sub-langs CSV (yt-dlp probes them
// top-to-bottom) and the PreferredLanguages fan-out order.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026). Use the canonical
// resolved cookie path for subtitle acquisition; the shared BaseArgs builder
// still gates --cookies to YouTube URLs.
func buildYouTubeSubtitleFetcher(cfg *config.Config, mlCfg config.MultilingualConfig) *ytinfra.SubtitleFetcherAdapter {
	subtitleLanguagesCSV := SubtitleAcquisitionLanguages(mlCfg)
	return ytinfra.NewSubtitleFetcherAdapter(
		ytinfra.SubtitleCacheConfig{
			YTDLPPath:    cfg.External.ResolvedYtdlpPath(),
			DefaultLangs: subtitleLanguagesCSV,
			CacheDir:     cfg.Storage.SubtitlesPath(),
		},
		nil,
		ytdlp.NewCommandBuilder(cfg),
		cfg.External.ResolveYouTubeCookiesPath() != "",
	)
}

// buildClipMetadataService assembles the clip metadata analyzer: the Ollama
// builder, optionally decorated with the artifact cache when the cache and
// main SQLite planes are both available (best-effort — a missing cache only
// downgrades to the uncached builder), wired to the canonical
// ClipMetadataWriter.
func buildClipMetadataService(
	cfg *config.Config,
	dbs *Databases,
	ai *AIBundle,
	clipMetadataWriter youtubeports.ClipMetadataWriter,
	log *zap.Logger,
) (*ytmetadata.MetadataService, error) {
	ollamaBuilderInner := ytinfra.NewOllamaClipMetadataBuilder(
		ai.OllamaClient,
		buildYouTubeRuntimeConfig(cfg).OllamaMetadataModel,
		0,
		log,
	)
	var ollamaBuilder ytmetadata.ClipMetadataBuilder = ollamaBuilderInner
	if dbs.Cache != nil && dbs.Cache.DB != nil && dbs.Main != nil && dbs.Main.DB != nil {
		if cache, cacheErr := NewArtifactCache(cfg, dbs.Cache.DB, dbs.Main.DB, log); cacheErr == nil {
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
		return nil, fmt.Errorf("compose domains: clip metadata service: %w", err)
	}
	return clipMetadataService, nil
}

// registerClipMetadataEnrichment registers the durable
// metadata.enrich.requested consumer on the PostgreSQL media outbox.
// godlike/07 fail-closed: an enabled async gate with no outbox worker refuses
// to boot rather than silently dropping every enrichment intent.
func registerClipMetadataEnrichment(outbox *OutboxBundle, clipMetadataService *ytmetadata.MetadataService, log *zap.Logger) error {
	if outbox != nil && outbox.MediaIndexWorker != nil {
		enrichHandler, enrichErr := newClipMetadataEnrichHandler(clipMetadataService, log)
		if enrichErr != nil {
			return fmt.Errorf("compose domains: metadata enrichment handler: %w", enrichErr)
		}
		if regErr := outbox.MediaIndexWorker.RegisterHandler(pgmedia.EventMetadataEnrichRequested, enrichHandler); regErr != nil {
			return fmt.Errorf("compose domains: register metadata enrichment handler: %w", regErr)
		}
		log.Info("youtube async metadata enrichment handler registered: metadata.enrich.requested -> AnalyzeClip + atomic metadata/index commit")
		return nil
	}
	if youtube.AsyncEnrichmentEnabled() {
		return fmt.Errorf("compose domains: VELOX_YOUTUBE_ASYNC_ENRICHMENT is enabled but the PostgreSQL media outbox worker is unavailable; refusing to boot with a silent enrichment drop")
	}
	return nil
}

// buildTextTrackResolver assembles the canonical text-track resolver:
// priority-chain lookup for localized text tracks, the multilingual
// certainty gate and the acquisition-order switch. Both operator knobs are
// fail-safe: disabling YouTube subtitles leaves the local Whisper
// transcriber, and ONLY the explicit whisper_first token reorders the chain
// (an unset or misspelled value never silently changes which source produces
// the transcript).
func buildTextTrackResolver(
	mlCfg config.MultilingualConfig,
	repos *RepoBundle,
	ai *AIBundle,
	subtitleFetcherAdapter *ytinfra.SubtitleFetcherAdapter,
	log *zap.Logger,
) *youtube.TextTrackResolver {
	// ACQUISITION SOURCE (Sept 2026): multilingual.youtube_subtitles_disabled
	// lets an operator source the transcript from the LOCAL Whisper
	// transcriber instead of YouTube captions. A nil Subtitles port makes
	// AcquireSegmentText skip priorities 3+4 and fall straight through to
	// priority 5. The DEFAULT (false) keeps the canonical priority chain, so
	// a deployment that does not set the flag is unaffected.
	var subtitlePort youtubeports.SubtitleFetcherPort = subtitleFetcherAdapter
	if mlCfg.YouTubeSubtitlesDisabled {
		subtitlePort = nil
		log.Info("multilingual.youtube_subtitles_disabled=true: acquisition sources the transcript from the local Whisper transcriber (YouTube subtitle levels 3+4 skipped)")
	}
	// ACQUISITION ORDER (Sept 2026): multilingual.source_priority=whisper_first
	// makes the local Whisper transcriber the PRIMARY transcript source and keeps
	// the YouTube subtitle levels as the fallback. Anything else (the default
	// "captions_first" included) keeps the canonical captions-then-Whisper
	// chain, so an unset or misspelled value can never silently reorder
	// acquisition (godlike/07: fail-safe, not fail-open).
	whisperFirst := resolveSourcePriority(mlCfg.SourcePriority)
	if whisperFirst {
		log.Info("multilingual.source_priority=whisper_first: local Whisper transcriber is the primary transcript source (YouTube captions kept as fallback)")
	}
	return &youtube.TextTrackResolver{
		Repo:         repos.TextTrackRepo,
		Subtitles:    subtitlePort, // satisfies youtubeports.SubtitleFetcherPort at wire-time (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a)
		Transcriber:  ai.WhisperTranscriber,
		WhisperFirst: whisperFirst,
		Log:          log,
		// The certainty gate stays config-driven; with the canonical order
		// Whisper is the fallback when subtitles are unavailable or unusable,
		// with whisper_first the subtitles are the fallback for Whisper.
		RequireLanguageCertainty: mlCfg.RequireLanguageCertainty,
	}
}

// buildProcessYouTubeSegment assembles the per-segment pipeline from its four
// capability-area sub-bundles (Core/Media/Metadata/Observability).
//
// PR-GRUPOC-2 (July 2026): youtube.ProcessSegmentDeps (17 fields) is
// RETIRED; the 17 fields are split into these four sub-bundles (each ≤7
// fields) to clear percheck_struct_deps ≤8 enforcement. No port is added,
// dropped, or renamed.
//
// Subtitles is intentionally left zero (that optional port is not exercised
// by the YouTube orchestrator at composition time).
// FFProbe is WIRED (Aug 2026 stub-recovery): Step 5a is the fail-closed gate
// that rejects a 262-byte empty MP4 stub (no video stream / zero duration)
// produced when a bot-checked yt-dlp section download exits zero but writes
// no media data. Before wiring, the Step 5 gate (size > 0) accepted the stub
// and the job "succeeded" with a dead artifact on Drive + Qdrant. The gate
// reuses the same shared media probe (rustexec) that cut_and_normalize uses,
// so the validation and the encode agree on the same execution plane.
// SegmentPolicy is config-driven: the canonical default is 4-60s
// (DefaultSegmentPolicy); production raises the max via
// cfg.Jobs.YoutubeMaxSegmentDurationSeconds (e.g. 120 for 2-minute clips)
// without mutating the shared default.
func buildProcessYouTubeSegment(
	cfg *config.Config,
	mlCfg config.MultilingualConfig,
	mediaConfig mediaexec.ExecutionConfig,
	clipProcessor *rustexec.VideoProcessor,
	videoPipelineAdapter *ytinfra.VideoPipelineAdapter,
	hashAdapter *ytinfra.HashAdapter,
	clipCache youtubeports.ClipCachePort,
	youtubePubAdapter *ytadapters.YouTubePublisherDriveAdapter,
	textTrackResolver *youtube.TextTrackResolver,
	youtubeSourceStager acquisition.SourceStager,
	clipWriter canonicalClipWriter,
	clipMetadataWriter youtubeports.ClipMetadataWriter,
	clipMetadataService *ytmetadata.MetadataService,
	log *zap.Logger,
) *youtube.ProcessYouTubeSegmentUseCase {
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
	return youtube.NewProcessYouTubeSegmentFromSubBundles(
		processSegCore,
		processSegMedia,
		processSegMetadata,
		processSegObservability,
	)
}

// resolveSourcePriority maps media.multilingual.source_priority to the resolver
// order token. ONLY the explicit "whisper_first" token reorders the chain;
// every other value (including the default "captions_first" and any typo)
// keeps the canonical captions-first order (godlike/07: an unrecognised value
// must not silently change which source produces the transcript).
func resolveSourcePriority(sourcePriority string) bool {
	return strings.EqualFold(strings.TrimSpace(sourcePriority), "whisper_first")
}

// buildBcp47CSV was removed: unused after the SubtitleFetcherAdapter wiring
// collapsed to the config-driven path.
