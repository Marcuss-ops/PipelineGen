// Package app — composition root decomposed into capability bundles.
//
// Bundle types live in composition_types.go.
// Bundle constructors live in per-bundle files under
// `internal/app/build_<bundle>.go` (PG-028, June 2026).
// composition.go retains NewComposition.
// Lifecycle (lifecycle.go) and Shutdown (shutdown.go) operate on the
// assembled ComposeRoot.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Orchestrator: NewComposition ─────────────────────────────────────────

// buildQdrantDeps constructs the pre-phase QdrantDeps bundle for
// BuildOutboxBundle. Returns ClipIndexerService + QdrantRuntime +
// QdrantDeleter. Remaining Qdrant adapters (CollectionManager etc.)
// stay in BuildProcessBundle (they don't depend on outbox.Dispatcher).
//
// Wire-time invariant: MUST NOT start goroutines (pinned by
// composition_test.go NoGoroutinesSpawned test). All constructors
// here return struct values without spawning.
//
// PG-028 (June 2026): each Build*Bundle now lives in its own
// `build_<bundle>.go` file. composition.go retains the bundle types,
// ComposeRoot, IOpaqueStartFunc, configOnlyDestinations, and the
// NewComposition orchestrator.
// NewComposition assembles all bundles in dependency order and returns
// the fully-wired ComposeRoot. Cleanup is owned by shutdown.go.
func NewComposition(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*ComposeRoot, error) {
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

	jobsDB := dbs.main
	if dbs.jobs != nil {
		jobsDB = dbs.jobs
	}
	// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): 4 cross-domain deps
	// threaded into JobsBundle pollution so buildClipOpsPorts(clipRepo, jobs)
	// stays strict 2-arg at the wire_assets_clips.go:187 call site.
	jobs, err := BuildJobsBundle(jobsDB, log, repos.VoiceoverRepo, repos.ImageRepo, driveBundle.driveUploader, driveBundle.Lifecycle)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	qdrantDeps, err := buildQdrantDeps(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose qdrant deps: %w", err)
	}

	driveAdmin := driveBundle.Admin
	var voiceoverDriver jobsoutbox.VoiceoverCleanupDriver
	if driveAdmin != nil {
		voiceoverDriver = driveAdmin
	}
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver)
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

	utility := BuildUtilityBundle(cfg, dbs.main, driveBundle.Reader, driveBundle.Publisher, jobs.Service, ai.OllamaClient, outbox.EventsPool, log)

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026): the
	// texttracks materializer + job handler. Constructed
	// after OutboxBundle (needs outbox.EventsRepo) and
	// AIBundle (needs OllamaTranslator).
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
	// AcquireService (local VTT/SRT → YouTube subs → Whisper
	// chain) is wired via the SubtitleFetcherPort exposed
	// on the DomainBundle + the WhisperTranscriber adapter
	// constructed by BuildAIBundle. The narrow
	// texttracks.SubtitlesPort / texttracks.WhisperPort
	// interfaces are STRUCTURAL subsets of the full
	// youtubeports ports; the type assertion in
	// BuildTextTrackBundle is a no-op at runtime.
	acquirePorts := &AcquirePorts{
		Subtitles: domains.SubtitleFetcher,
		Whisper:   ai.WhisperTranscriber,
	}
	textTracks, err := BuildTextTrackBundle(cfg, repos, ai, outbox, acquirePorts, log)
	if err != nil {
		return nil, fmt.Errorf("compose texttracks: %w", err)
	}

	// FASE 3 Spina Dorsale (Push 3.1b, July 2026): wire the
	// staging.StoreService + artifact_stages Repository. Placed
	// LAST in NewComposition (after BuildTextTrackBundle) because
	// the bundle has minimal deps (only dbs.main.DB + cfg +
	// log) — no risk of breaking the existing 12-bundle
	// aggregation. The forward-pointer publisher worker pool
	// (Push 3.1c) will consume root.Staging.Store to drain
	// the outbox into the canonical artifact_stages table.
	staging, err := BuildStagingBundle(dbs, cfg, log)
	if err != nil {
		return nil, fmt.Errorf("compose staging: %w", err)
	}

	// Wire the script.generate readiness probe when any script feature is
	// enabled. It needs the health Service (for db/jobs sub-checks),
	// Ollama, Drive, document service, and (later, at route-build time)
	// the /api/script route.
	if utility.ReadyChecker != nil && utility.HealthService != nil && anyScriptFeatureEnabled(cfg) {
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

	if err := wireYoutubeCatalogJobBindings(sync, domains, jobs); err != nil {
		return nil, fmt.Errorf("compose catalogsync/youtube late-binding: %w", err)
	}
	if err := wireVoiceoverJobBindings(domains, jobs, log); err != nil {
		return nil, fmt.Errorf("compose voiceover late-binding: %w", err)
	}
	if err := wireImagesJobBinding(domains, jobs); err != nil {
		return nil, fmt.Errorf("compose images late-binding: %w", err)
	}
	if err := wireClipIndexerJobBinding(process, jobs); err != nil {
		return nil, fmt.Errorf("compose clipindexer late-binding: %w", err)
	}
	if err := wireTextTrackJobBindings(textTracks, jobs); err != nil {
		return nil, fmt.Errorf("compose texttracks late-binding: %w", err)
	}
	root := &ComposeRoot{
		DB:         dbs.main,
		Drive:      driveBundle,
		Repos:      repos,
		Search:     search,
		Process:    process,
		TextTracks: textTracks,

		AI:      ai,
		Domains: domains,
		Jobs:    jobs,
		Outbox:  outbox,
		Sync:    sync,
		Maint:   maint,
		Utility: utility,
		Staging: staging,

		DriveStart:  driveStart,
		OutboxStart: outboxStart,
		Ctx:         ctx,
	}

	var criticalHandlerValidators []CriticalHandler
	appendYoutubeCatalogCriticalValidators(sync, domains, jobs, &criticalHandlerValidators)
	appendImagesCriticalValidator(domains, jobs, &criticalHandlerValidators)
	appendClipIndexerCriticalValidator(process, jobs, &criticalHandlerValidators)
	appendVoiceoverCriticalValidators(domains, jobs, &criticalHandlerValidators)
	if err := ValidateCriticalHandlers(jobs.Service, log, criticalHandlerValidators); err != nil {
		return nil, fmt.Errorf("compose critical-handler validation (audit-P0.2 cont., PR-VALIDATOR-LITERAL-REGISTER): %w", err)
	}

	return root, nil
}
