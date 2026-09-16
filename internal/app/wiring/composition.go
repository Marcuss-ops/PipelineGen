// Package wiring — composition root.
package wiring

import (
	"context"
	"fmt"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	artifactsinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifacts"
	historyinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/history"

	"go.uber.org/zap"
)

// NewComposition assembles all bundles in dependency order and returns the
// fully-wired ComposeRoot. Cleanup is owned by shutdown.go.
func NewComposition(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger) (*ComposeRoot, error) {
	mediaConfig := mediasub.MediaexecConfig(cfg)

	if dbs == nil || dbs.DualPool == nil || dbs.DualPool.Writer == nil {
		return nil, fmt.Errorf("compose: canonical database writer is required")
	}

	// POSTGRES-MEDIA-CUTOVER: open the media SSOT handle FIRST so every
	// downstream media decision sees one engine selection. enabled=true is
	// fail-closed for invalid config or an unreachable database.
	mediaPG, err := mediasub.RequireMediaPostgres(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("compose media postgres: %w", err)
	}

	repos, err := BuildRepoBundle(ctx, cfg, dbs, log, mediaPG)
	if err != nil {
		return nil, fmt.Errorf("compose repos: %w", err)
	}
	// P1-7 (Sept 2026): MediaRepoBundle is the sole media-authoritative
	// bundle (PostgreSQL). RepoBundle retains only operational SQLite
	// surfaces; new media reads/writes MUST target Media, not Repos.
	mediaBundle := NewMediaRepoBundle(mediaPG, repos.TextTrackRepo)
	search, err := BuildSearchBundle(ctx, cfg, dbs, log, repos)
	if err != nil {
		return nil, fmt.Errorf("compose search: %w", err)
	}

	driveBundle, driveStart, err := BuildDriveBundle(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose drive: %w", err)
	}

	jobsDB := dbs.Main
	if dbs.Jobs != nil {
		jobsDB = dbs.Jobs
	}
	jobs, err := BuildJobsBundle(jobsDB, log, repos.VoiceoverRepo, repos.ImageRepo, driveBundle.DriveUploader, driveBundle.Lifecycle)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}
	if dbs.Logs != nil {
		historyReader, historyErr := historyinfra.NewReader(jobsDB.DB, dbs.Logs.DB)
		if historyErr != nil {
			return nil, fmt.Errorf("compose history: %w", historyErr)
		}
		jobs.History = historyReader
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	qdrantDeps, err := buildQdrantDeps(ctx, cfg, dbs, repos, log)
	if err != nil {
		return nil, fmt.Errorf("compose qdrant deps: %w", err)
	}

	driveAdmin := driveBundle.Admin
	var voiceoverDriver jobsoutbox.VoiceoverCleanupDriver
	if driveAdmin != nil {
		voiceoverDriver = driveAdmin
	}

	staging, err := BuildStagingBundle(dbs, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("compose staging: %w", err)
	}

	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver, staging.Store, staging.Repository, driveBundle.Publisher, driveBundle.Lifecycle, mediaPG)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.Publisher, outbox, qdrantDeps, mediaPG, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	// COMPLETE MEDIA CUTOVER: the legacy-named ClipIndexerService still has
	// call sites such as reindex jobs and provider workflows. In PostgreSQL
	// media mode those imperative calls MUST enqueue the canonical PG outbox
	// event rather than execute the retired SQLite -> Qdrant implementation.
	// Qdrant can remain independently wired inside ProcessBundle for an
	// explicitly owned non-media capability; it is not rebound as the media
	// vector authority here.
	if mediaPG != nil && process.ClipIndexerService != nil {
		process.ClipIndexerService.SetCanonicalIndexRequester(pgmedia.NewReindexRequester(mediaPG))
		// MEDIA-SSOT P2-9 Phase 2: the eligibility gate reads media_assets, so it
		// must read the media SSOT. ClipIndexerService's own db is the
		// operational SQLite store, which holds no committed media rows — leaving
		// the gate on that handle made the taxonomy decision engine-blind. Both
		// seams are set from the same mediaPG handle so the requester and the
		// eligibility reader cannot drift onto different engines.
		process.ClipIndexerService.SetMediaEligibilityReader(pgmedia.NewMediaEligibilityReader(mediaPG))
	}

	domains, err := BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, search, process, ai, outbox, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose domains: %w", err)
	}

	sync, err := BuildSyncBundle(ctx, cfg, dbs, log, repos, search, process, driveBundle, outbox, mediaPG)
	if err != nil {
		return nil, fmt.Errorf("compose sync: %w", err)
	}

	sourceCatalog, err := artifactsinfra.NewArtifactSourceCatalog(repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo, repos.VoiceoverRepo, repos.ImageRepo)
	if err != nil {
		return nil, fmt.Errorf("compose source catalog: %w", err)
	}
	maint, err := BuildMaintBundle(ctx, cfg, dbs, log, driveBundle, repos, search, jobs, outbox, sourceCatalog)
	if err != nil {
		return nil, fmt.Errorf("compose maintenance: %w", err)
	}

	storagePlanes := func(checkCtx context.Context) map[string]systemhealth.CheckResult {
		result := make(map[string]systemhealth.CheckResult)
		for name, plane := range dbs.Set.HealthByPlane(checkCtx) {
			check := systemhealth.CheckResult{"ok": plane.Available}
			if plane.Error != nil {
				check["error"] = plane.Error.Error()
			}
			if name == "cache" || name == "observability" {
				check["applicable"] = true
			}
			result[name] = check
		}
		return result
	}
	utility := BuildUtilityBundle(cfg, dbs.Main, dbs.Jobs, storagePlanes, driveBundle.Reader, driveBundle.Publisher, jobs.Service, ai.OllamaClient, outbox.EventsPool, log)

	// ACQUISITION SOURCE (Sept 2026): the same policy the per-segment
	// resolver applies — when YouTube subtitles are disabled the backfill
	// acquirer must not re-acquire them either, or a repair pass would
	// resurrect a YouTube transcript over the locally transcribed one.
	acquireSubtitles := domains.SubtitleFetcher
	if ActiveMultilingualConfig(cfg).YouTubeSubtitlesDisabled {
		acquireSubtitles = nil
	}
	acquirePorts := &AcquirePorts{
		Subtitles: acquireSubtitles,
		Whisper:   ai.WhisperTranscriber,
		Drive:     driveBundle.Reader,
		CueWriter: domains.CueWriter,
		// POSTGRES-MEDIA-CUTOVER: the backfill/materialize asset reader
		// reads the PostgreSQL media SSOT, never the legacy SQLite
		// catalog (which does not contain a just-committed PG clip).
		MediaAssets: newPostgresMediaAssetLister(mediaPG),
		// Single owner of the subtitle artifact Drive folder: the
		// configured subtitle root under the source video id, the same
		// folder the extraction uploaded the .txt sidecar into.
		SubtitleFolders: NewSubtitleRootLayoutResolver(cfg.Drive.YouTubeSubtitlesFolder()),
	}
	textTracks, err := BuildTextTrackBundle(cfg, repos, ai, outbox, acquirePorts, driveBundle.Publisher, log)
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: %w", err)
	}

	finalizer, err := BuildArtifactFinalizeBundle(staging, log)
	if err != nil {
		return nil, fmt.Errorf("compose artifact_finalize: %w", err)
	}

	wireScriptReadinessProbe(cfg, utility, ai, driveBundle, jobs)

	if err := wireLateBindings(cfg, sync, domains, jobs, process, textTracks, log); err != nil {
		return nil, err
	}

	if err := validateCriticalHandlers(jobs, sync, domains, process, log); err != nil {
		return nil, err
	}

	// MEDIA-SSOT P1-7: attach the canonical media writer to the media bundle so
	// MediaRepoBundle is the complete media boundary (reader + identity +
	// writer). The committer is built by BuildOutboxBundle, hence the late
	// binding; it stays nil only in the degraded (PG-disabled) mode.
	if mediaBundle != nil {
		mediaBundle.Writer = outbox.CanonicalWriter
	}

	root := &ComposeRoot{
		CanonicalAssetWriter: outbox.CanonicalWriter,
		MediaExec:            mediaConfig,
		DB:                   dbs.Main,
		ObservabilityDB:      dbs.Logs,
		CacheDB:              dbs.Cache,
		MediaPostgres:        mediaPG,
		Drive:                driveBundle,
		Repos:                repos,
		Media:                mediaBundle,
		Search:               search,
		Process:              process,
		TextTracks:           textTracks,

		AI:        ai,
		Domains:   domains,
		Jobs:      jobs,
		Outbox:    outbox,
		Sync:      sync,
		Maint:     maint,
		Utility:   utility,
		Staging:   staging,
		Finalizer: finalizer,

		DriveStart:  driveStart,
		OutboxStart: outboxStart,
		Ctx:         ctx,
	}

	return root, nil
}

func wireScriptReadinessProbe(cfg *config.Config, utility *UtilityBundle, ai *AIBundle, driveBundle *DriveBundle, jobs *JobsBundle) {
	if utility.ReadyChecker == nil || utility.HealthService == nil || !anyScriptFeatureEnabled(cfg) {
		return
	}
	scriptChecker := systemhealth.NewScriptGenerateChecker(
		utility.HealthService,
		systemhealth.NewOllamaChecker(func(ctx context.Context) bool {
			return ai.OllamaClient != nil && ai.OllamaClient.CheckHealth(ctx)
		}),
		systemhealth.NewDriveFolderChecker(driveBundle.Publisher),
		cfg.Drive.ScriptsGenFolder(),
		systemhealth.NewPublisherChecker(driveBundle.Publisher),
		func() bool { return driveBundle.DocClient != nil },
		func(jobType string) bool { return jobs.Service != nil && jobs.Service.HasHandler(jobType) },
	)
	utility.ReadyChecker.WithScriptGenerateCheck(scriptChecker)
}

func wireLateBindings(cfg *config.Config, sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, process *ProcessBundle, textTracks *TextTrackBundle, log *zap.Logger) error {
	if err := wireYoutubeCatalogJobBindings(sync, domains, jobs); err != nil {
		return fmt.Errorf("compose catalogsync/youtube late-binding: %w", err)
	}
	if err := wireVoiceoverJobBindings(domains, jobs, log); err != nil {
		return fmt.Errorf("compose voiceover late-binding: %w", err)
	}
	if err := wireImagesJobBinding(domains, jobs); err != nil {
		return fmt.Errorf("compose images late-binding: %w", err)
	}
	if err := WireTextTrackJobBindings(textTracks, jobs); err != nil {
		return fmt.Errorf("compose texttracks late-binding: %w", err)
	}
	WireTextTracksFanOut(textTracks, jobs.Service, log)
	if textTracks.FanOut != nil {
		textTracks.FanOut.SetDefaultSourceLanguage(ActiveMultilingualConfig(cfg).SourceLanguage)
	}
	// POSTGRES-MEDIA-CUTOVER follow-up (September 2026): give the direct
	// YouTube extraction path the canonical post-commit fan-out. The Artlist /
	// Stock / generic paths reach it through their finalizers; YouTube commits
	// through LocalizedWriter directly, so without this late binding a freshly
	// extracted clip produced only the languages the acquisition chain
	// happened to find. nil-guarded: no broker → no fan-out → unchanged
	// behaviour.
	if textTracks.FanOut != nil && domains.YoutubeClipService != nil {
		domains.YoutubeClipService.WithMaterializeFanOut(textTracks.FanOut)
	}
	return nil
}

// MediaAssetLister resolves the canonical BATCH media-clip reader for a
// composition root — the `List(ctx, asset.Filter)` shape the backfill pipeline
// and the `asset.text.materialize` handler consume. PostgreSQL is the media
// SSOT; the legacy SQLite *assets.ClipsRepository satisfies the same interface
// only for the media-PostgreSQL-disabled degrade mode.
//
// godlike/06 SSOT: mirror of ComposeRoot.MediaClipReader (single-id reads) so
// the batch path cannot silently keep reading a second catalog.
//
// Lives next to the ComposeRoot type it hangs off (Pattern 5 split, keeping
// build_bundles_texttracks.go inside the 600-line gate).
func (r *ComposeRoot) MediaAssetLister() texttracks.MediaAssetLister {
	if r == nil {
		return nil
	}
	if lister := newPostgresMediaAssetLister(r.MediaPostgres); lister != nil {
		return lister
	}
	if r.Repos != nil && r.Repos.ClipsRepo != nil {
		return r.Repos.ClipsRepo
	}
	return nil
}

func validateCriticalHandlers(jobs *JobsBundle, sync *SyncBundle, domains *DomainBundle, process *ProcessBundle, log *zap.Logger) error {
	var criticalHandlerValidators []CriticalHandler
	appendYoutubeCatalogCriticalValidators(sync, domains, jobs, &criticalHandlerValidators)
	appendImagesCriticalValidator(domains, jobs, &criticalHandlerValidators)
	appendVoiceoverCriticalValidators(domains, jobs, &criticalHandlerValidators)
	if err := ValidateCriticalHandlers(jobs.Service, log, criticalHandlerValidators); err != nil {
		return fmt.Errorf("compose critical-handler validation: %w", err)
	}
	return nil
}
