// Package app — composition root.
//
// Bundle types live in composition_types.go.
// Bundle constructors live in per-bundle files under
// `internal/app/build_<bundle>.go`.
// composition.go retains NewComposition.
// Lifecycle (lifecycle.go) and Shutdown (shutdown.go) operate on the
// assembled wiring.ComposeRoot.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"
	"fmt"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// NewComposition assembles all bundles in dependency order and returns
// the fully-wired wiring.ComposeRoot. Cleanup is owned by shutdown.go.
func NewComposition(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, log *zap.Logger) (*wiring.ComposeRoot, error) {
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
	// Cross-domain dependencies are threaded into wiring.JobsBundle here so that
	// downstream constructors can keep strict, narrow signatures.
	jobs, err := wiring.BuildJobsBundle(jobsDB, log, repos.VoiceoverRepo, repos.ImageRepo, driveBundle.DriveUploader, driveBundle.Lifecycle)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
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

	// wiring.StagingBundle must be built before wiring.OutboxBundle so the Publisher
	// handler can register against staging.Store at wire-time.
	staging, err := BuildStagingBundle(dbs, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("compose staging: %w", err)
	}

	// wiring.OutboxBundle consumes wiring.StagingBundle.Store and Repository, plus the
	// canonical delivery.Publisher, to drain artifact lifecycle events.
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver, staging.Store, staging.Repository, driveBundle.Publisher)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.Publisher, outbox, qdrantDeps)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	domains, err := BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, search, process, ai, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose domains: %w", err)
	}

	sync, err := BuildSyncBundle(ctx, cfg, dbs, log, repos, search, process, driveBundle, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose sync: %w", err)
	}

	maint, err := BuildMaintBundle(ctx, cfg, dbs, log, driveBundle, repos, search, jobs, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose maintenance: %w", err)
	}

	utility := BuildUtilityBundle(cfg, dbs.Main, driveBundle.Reader, driveBundle.Publisher, jobs.Service, ai.OllamaClient, outbox.EventsPool, log)

	acquirePorts := &wiring.AcquirePorts{
		Subtitles: domains.SubtitleFetcher,
		Whisper:   ai.WhisperTranscriber,
		Drive:     driveBundle.Reader,
		CueWriter: domains.CueWriter,
	}
	textTracks, err := wiring.BuildTextTrackBundle(cfg, repos, ai, outbox, acquirePorts, driveBundle.Publisher, log)
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

	root := &wiring.ComposeRoot{
		DB:         dbs.Main,
		Drive:      driveBundle,
		Repos:      repos,
		Search:     search,
		Process:    process,
		TextTracks: textTracks,

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
func wireScriptReadinessProbe(cfg *config.Config, utility *wiring.UtilityBundle, ai *wiring.AIBundle, driveBundle *wiring.DriveBundle, jobs *wiring.JobsBundle) {
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
func wireLateBindings(cfg *config.Config, sync *wiring.SyncBundle, domains *wiring.DomainBundle, jobs *wiring.JobsBundle, process *wiring.ProcessBundle, textTracks *wiring.TextTrackBundle, log *zap.Logger) error {
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
	if err := wiring.WireTextTrackJobBindings(textTracks, jobs); err != nil {
		return fmt.Errorf("compose texttracks late-binding: %w", err)
	}
	wiring.WireTextTracksFanOut(textTracks, jobs.Service, log)
	if textTracks.FanOut != nil {
		textTracks.FanOut.SetDefaultSourceLanguage(wiring.ActiveMultilingualConfig(cfg).SourceLanguage)
	}
	return nil
}

// validateCriticalHandlers assembles and runs the critical handler
// validation suite after all bundles and late bindings are wired.
func validateCriticalHandlers(jobs *wiring.JobsBundle, sync *wiring.SyncBundle, domains *wiring.DomainBundle, process *wiring.ProcessBundle, log *zap.Logger) error {
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
