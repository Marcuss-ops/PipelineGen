// Package app — composition root.
//
// Bundle types live in composition_types.go.
// Bundle constructors live in per-bundle files under
// `internal/app/build_<bundle>.go`.
// composition.go retains NewComposition.
// Lifecycle (go) and Shutdown (shutdown.go) operate on the
// assembled ComposeRoot.
package wiring

import (
	"context"
	"fmt"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	artifactsinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifacts"
	historyinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/history"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// NewComposition assembles all bundles in dependency order and returns
// the fully-wired ComposeRoot. Cleanup is owned by shutdown.go.
func NewComposition(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger) (*ComposeRoot, error) {
	mediaConfig := mediasub.MediaexecConfig(cfg)

	if dbs == nil || dbs.DualPool == nil || dbs.DualPool.Writer == nil {
		return nil, fmt.Errorf("compose: canonical database writer is required")
	}

	// POSTGRES-MEDIA-CUTOVER: open the media SSOT handle FIRST so every
	// downstream wiring decision (canonical committer, vector search
	// plane, index worker) sees a consistent engine selection. Fail-closed:
	// an enabled-but-unreachable media PostgreSQL aborts composition.
	mediaPG, err := mediasub.RequireMediaPostgres(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("compose media postgres: %w", err)
	}

	repos, err := BuildRepoBundle(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose repos: %w", err)
	}

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
	// Cross-domain dependencies are threaded into JobsBundle here so that
	// downstream constructors can keep strict, narrow signatures.
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

	// StagingBundle must be built before OutboxBundle so the Publisher
	// handler can register against staging.Store at wire-time.
	staging, err := BuildStagingBundle(dbs, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("compose staging: %w", err)
	}

	// OutboxBundle consumes StagingBundle.Store and Repository, plus the
	// canonical delivery.Publisher, to drain artifact lifecycle events.
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver, staging.Store, staging.Repository, driveBundle.Publisher, driveBundle.Lifecycle, mediaPG)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.Publisher, outbox, qdrantDeps, mediaPG, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	domains, err := BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, search, process, ai, outbox, mediaConfig)
	if err != nil {
		return nil, fmt.Errorf("compose domains: %w", err)
	}

	sync, err := BuildSyncBundle(ctx, cfg, dbs, log, repos, search, process, driveBundle, outbox)
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

	acquirePorts := &AcquirePorts{
		Subtitles: domains.SubtitleFetcher,
		Whisper:   ai.WhisperTranscriber,
		Drive:     driveBundle.Reader,
		CueWriter: domains.CueWriter,
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

	root := &ComposeRoot{
		CanonicalAssetWriter: outbox.CanonicalWriter,
		MediaExec:            mediaConfig,
		DB:                   dbs.Main,
		ObservabilityDB:      dbs.Logs,
		CacheDB:              dbs.Cache,
		MediaPostgres:        mediaPG,
		Drive:                driveBundle,
		Repos:                repos,
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

// wireScriptReadinessProbe registers the script.generate readiness probe
// when any script feature is enabled.
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

// wireLateBindings connects circular/lazy job handler registrations after
// all bundles have been constructed.
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
	if err := wireClipIndexerJobBinding(process, jobs); err != nil {
		return fmt.Errorf("compose clipindexer late-binding: %w", err)
	}
	if err := WireTextTrackJobBindings(textTracks, jobs); err != nil {
		return fmt.Errorf("compose texttracks late-binding: %w", err)
	}
	WireTextTracksFanOut(textTracks, jobs.Service, log)
	if textTracks.FanOut != nil {
		textTracks.FanOut.SetDefaultSourceLanguage(ActiveMultilingualConfig(cfg).SourceLanguage)
	}
	return nil
}

// validateCriticalHandlers assembles and runs the critical handler
// validation suite after all bundles and late bindings are wired.
func validateCriticalHandlers(jobs *JobsBundle, sync *SyncBundle, domains *DomainBundle, process *ProcessBundle, log *zap.Logger) error {
	var criticalHandlerValidators []CriticalHandler
	appendYoutubeCatalogCriticalValidators(sync, domains, jobs, &criticalHandlerValidators)
	appendImagesCriticalValidator(domains, jobs, &criticalHandlerValidators)
	appendClipIndexerCriticalValidator(process, jobs, &criticalHandlerValidators)
	appendVoiceoverCriticalValidators(domains, jobs, &criticalHandlerValidators)
	if err := ValidateCriticalHandlers(jobs.Service, log, criticalHandlerValidators); err != nil {
		return fmt.Errorf("compose critical-handler validation: %w", err)
	}
	return nil
}
