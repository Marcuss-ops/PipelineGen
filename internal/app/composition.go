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

// buildQdrantDeps constructs the tiny pre-phase QdrantDeps bundle used
// by BuildOutboxBundle (QDRANT-003 — IndexWriter as QdrantDeleter + the
// canonical ClipIndexerService).
//
// PR 4 (June 2026, refactor/single-qdrant-runtime): the body now
// delegates Client/Writer/Searcher/Manager/Health/Cleaner/Mapper/Store
// construction to qdrant.NewRuntime — there is exactly ONE qdrant.NewClient
// call per process (was previously called from BOTH buildQdrantDeps and
// BuildProcessBundle; the second invocation created a redundant Client
// that wire responses could drift apart on under future changes).
//
// The 3 fields returned here are exactly what BuildOutboxBundle reads.
// The remaining Qdrant-derived adapters (CollectionManager, VectorSvc,
// LocatorCleaner, QdrantHealthProbe, QdrantSearcher, QdrantClient
// itself) stay in BuildProcessBundle — none of them depend on
// outbox.Dispatcher, so they can be constructed inline after
// BuildOutboxBundle returns the canonical dispatcher.
//
// Wire-time invariant: buildQdrantDeps MUST NOT start goroutines
// (composition_test.go::TestComposition_NoGoroutinesSpawned_FrozenSiteCount
// pins the no-spawn shape of every builder name matching Build\w+Bundle in
// source). clipindexer.NewService + qdrant.NewRuntime all return struct
// values without spawning goroutines, so this body is in scope.
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): replaces the
// PR-7 deferred-hydration strategy (hydrateMediaProcessor +
// ProcessBundle.MediaProcessor=nil) with this strict-DAG pre-phase.
// Composition order is now: qdrantDeps(no deps) -> outbox(reads qd) ->
// process(reads outbox+qd) -> domains(reads process+outbox).
// NewComposition composes all bundles in dependency order and returns the
// fully-assembled ComposeRoot. Cleanup is owned by shutdown.go.
//
// PG-028 (June 2026): each Build*Bundle now lives in its own
// `build_<bundle>.go` file. composition.go retains the bundle types,
// ComposeRoot, IOpaqueStartFunc, configOnlyDestinations, and the
// NewComposition orchestrator.
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

	// PR-Queue-Split-EXPAND / ADR-0003 (June 2026): the JobsBundle's DB
	// is the EXPAND-on jobs.db.sqlite when the gate is enabled + the DB
	// opened successfully at initDatabases; otherwise the canonical
	// single-DB shape on dbs.main. The BuildJobsBundle signature is
	// unchanged; composition-root does the pick. Documented here so a
	// future reader does not chase the dbs.main reference looking for
	// where the EXPAND gate surfaces.
	jobsDB := dbs.main
	if dbs.jobs != nil {
		jobsDB = dbs.jobs
	}
	jobs, err := BuildJobsBundle(jobsDB, log)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed) —
	// ring-break reorder: the canonical Qdrant adapters that
	// BuildOutboxBundle needs (ClipIndexerService + QdrantDeleter) are
	// constructed in a tiny buildQdrantDeps pre-phase so BuildOutboxBundle
	// can run BEFORE BuildProcessBundle. BuildProcessBundle then consumes
	// both qdrantDeps + outbox.OutboxBundle and constructs MediaProcessor
	// inline using the canonical outbox.Dispatcher SSOT (QDRANT-002
	// atomicity invariant). The composition graph is now a strict DAG:
	// qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd)
	// -> domains. The previous PR-7 deferred-hydration strategy
	// (hydrateMediaProcessor + MediaProcessor=nil) is gone.
	qdrantDeps, err := buildQdrantDeps(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose qdrant deps: %w", err)
	}

	// PR-12d (June 2026): BuildOutboxBundle runs BEFORE BuildDomainBundle.
	// PR 8 (June 2026) takes this further and reorders BuildOutboxBundle
	// BEFORE BuildProcessBundle too: BuildProcessBundle now reads
	// qdrantDeps (the only Process-side deps BuildOutboxBundle needed,
	// exposed by the pre-phase) and outbox.OutboxBundle (for the
	// canonical mutations dispatcher used in MediaProcessor). The
	// dispatcher is wired into images.Service via constructor injection
	// in BuildDomainBundle.
	//
	// P0.7 Wave 21 Step 10/12 (June 2026): construct the voiceover
	// cleanup driver inline BEFORE BuildOutboxBundle because the
	// outbox bundle now expects a jobsoutbox.VoiceoverCleanupDriver
	// arg (the canonical narrow port for orphan Drive file delete).
	// The canonical drive.Admin interface structurally satisfies
	// jobsoutbox.VoiceoverCleanupDriver (both declare DeleteFile with
	// the same signature). nil Drive admin → nil cleanup driver →
	// handler registered but skips the Drive delete branch (local-
	// remove branch still runs via stdlib; logs operator-visible
	// warning).
	driveAdmin := driveBundle.Admin
	var voiceoverDriver jobsoutbox.VoiceoverCleanupDriver
	if driveAdmin != nil {
		voiceoverDriver = driveAdmin
	}
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	// F2.8 (June 2026): thread driveBundle.Publisher (the canonical
	// delivery.Publisher port) instead of the unexported driveUploader.
	// The pre-F2.8 raw-uploader bypass is closed. Publisher is
	// fail-populated in BuildDriveBundle (build_bundles_drive.go) — a
	// nil publisher surfaces in processor.NewProcessor as a typed
	// panic at composition time (loud in operator log) rather than
	// silent nil-deref on first upload.
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

	// Wave A (June 2026): BuildUtilityBundle now takes the canonical
	// drive.Admin port. The legacy `rawDriveSvc *gdrive.Service`
	// extraction from `driveBundle.driveUploader.Service` is gone —
	// pass `driveBundle.Admin` (the Pattern 0 port) directly so the
	// health probe can call driveAdmin.Ping() which wraps the raw
	// About.Get internally.
	utility := BuildUtilityBundle(cfg, dbs.main, driveBundle.Reader, driveBundle.Publisher, jobs.Service, ai.OllamaClient, outbox.EventsPool, log)

	// Late-bindings: jobs.RegisterHandler for domain services that opt in.
	// Per PG-028 (July 2026): extracted into per-capability wire_* helpers.
	// See build_bundles_youtube.go, build_bundles_voiceover.go,
	// build_bundles_images.go, build_bundles_clips.go.
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
	// Capability Standard migration (June 2026): BooksService and
	// LessonsService worker handlers are NOT registered here.
	// The Generation capability owns the books.process and
	// lessons.process job types and publishes the handlers via
	// its Descriptor (api.DescriptorJobs), wired in registry.go
	// after generation.Build returns. Single source of truth.

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
		// PR8 (June 2026): wire construction is performed in registry.go
		// and assigned back to root.IdempotencyMiddleware. The cleanup
		// goroutine lives only at the registry level (per reviewer fix A).
		// WireRegistry (after this fn returns) sets the field directly
		// via ComposeRoot.IdempotencyMiddleware assignment.
		Ctx: ctx,
	}

	// ── Critical-handler validator (Audit P0 #2 cont., PR-VALIDATOR-LITERAL-REGISTER, July 2026) ──
	// Per PG-028 (July 2026): validator construction extracted into
	// per-capability append_* helpers. See build_bundles_youtube.go,
	// build_bundles_voiceover.go, build_bundles_images.go,
	// build_bundles_clips.go.
	//
	// stockpipeline.media_stock is NOT in this slice — it's wired via
	// registerInternalModules::WireStockPipeline AFTER NewComposition
	// returns. The canonical stockpipeline validator pass lives in
	// lifecycle.go (post-WireStockPipeline + pre-ListenAndServe).
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
