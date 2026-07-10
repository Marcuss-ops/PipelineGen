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
	root := &ComposeRoot{
		DB:      dbs.main,
		Drive:   driveBundle,
		Repos:   repos,
		Search:  search,
		Process: process,

		AI:      ai,
		Domains: domains,
		Jobs:    jobs,
		Outbox:  outbox,
		Sync:    sync,
		Maint:   maint,
		Utility: utility,

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
