// Package app — outbox deps + handler-registration sub-blocks.
//
// Extracted from build_bundles_process.go::BuildOutboxBundle (July 2026
// sub-section split). Owns: buildOutboxDeps (outbox.Deps construction
// incl. httpClient/HMAC secrets/source querier/metadata-export handler),
// registerOutboxCoreHandlers (Qdrant on/off core registration),
// registerOutboxWorkers (optional + script.generate.queued + publisher +
// drive-uploader workers) and noopIndexClipper (qdrant-off IndexClip
// no-op). All error strings keep the canonical "BuildOutboxBundle:"
// prefix so the fail-closed contract observed by
// composition_failclosed_test.go is unchanged.
package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	publishdrive "github.com/Marcuss-ops/PipelineGen/internal/application/publish_drive"
	publishoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/publish_outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/metadataexport"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpclient"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

// buildOutboxDeps constructs the jobsoutbox.Deps consumed by the core +
// optional handler registrations and the pre-built
// metadataexport.MetadataExportHandler. Extracted verbatim from
// BuildOutboxBundle (July 2026); called by the reduced orchestrator.
func buildOutboxDeps(
	dbs *wiring.Databases,
	cfg *config.Config,
	repos *wiring.RepoBundle,
	jobs *wiring.JobsBundle,
	qd *wiring.QdrantDeps,
	voiceoverDriver jobsoutbox.VoiceoverCleanupDriver,
	log *zap.Logger,
) (*jobsoutbox.Deps, outboxevents.Handler) {
	// PR-REFACTOR-P0-IO-BINDER-HTTP (July 2026): route the outbox http.Client
	// construction through internal/infrastructure/httpclient.NewDefaultClient
	// (the canonical owner of *http.Client construction for the application
	// port surface). The result satisfies ports.Client, which is the
	// field type of InfraDeps.HTTPClient (consumed by the DeliveryHandler).
	httpClient := httpclient.NewDefaultClient(30 * time.Second)

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	// SourceVersionQuerier is the narrow port consumed by the
	// IndexingHandler source_version supersede gate (PR 11 follow-up,
	// June 2026). The production concrete is *assets.ClipsRepository
	// (already wired into the dispatcher's MultiClipsUpserter; same
	// instance also implements SourceVersionQuerier via a thin
	// delegating method). nil ClipsRepo → nil SourceVersionQuerier →
	// IndexingHandler skips the supersede gate (acceptable in test
	// dbs; production always wires non-nil).
	//
	// Wave 16 (June 2026): typed-port direct assignment per
	// AGENTS.md Pattern 0. The previous
	// `any(repos.ClipsRepo).(jobsoutbox.AssetSourceChecker)`
	// raw cast is replaced because *assets.ClipsRepository
	// statically implements the port (compile-time assertion at
	// internal/platform/sqlite/assets/clips_repository.go).
	// Dropping the `, ok` form is safe: the assertion fails the build
	// if port drift ever breaks the static implementation contract.
	// PR 11 follow-up extends the assertion to SourceVersionQuerier
	// (single-method port) — the previous AssetSourceChecker port
	// (GetClip → walk Asset) is removed entirely.
	var sourceQuerier jobsoutbox.SourceVersionQuerier
	if repos.ClipsRepo != nil {
		sourceQuerier = repos.ClipsRepo
	}

	// Step 2 (June 2026): pre-build the canonical MetadataExportHandler
	// via the new typed-port adapters. The composition root is the ONLY
	// place infra concrete types meet application ports — the
	// outbox.Deps struct no longer needs MetadataDir because the
	// handler gets its output dir as part of HandlerDeps at wire time.
	metadataExportResolver := sqmetadataexport.NewSQLiteAdapter(dbs.DualPool.Writer)
	metadataExportWriter := &filesmetadataexport.FileWriter{}
	metadataExportDeps := jobsoutbox.MetadataExportHandlerDeps{
		Resolver:  metadataExportResolver,
		Writer:    metadataExportWriter,
		OutputDir: cfg.Storage.FullPath("asset_metadata"),
		Log:       log,
	}
	metadataExportHandler := jobsoutbox.NewMetadataExportHandler(metadataExportDeps)

	outboxDeps := &jobsoutbox.Deps{
		Infra: jobsoutbox.InfraDeps{
			DB:          dbs.DualPool.Writer,
			HTTPClient:  httpClient,
			HMACSecrets: hmacSecrets,
			InsecureDev: cfg.Security.DeliveryInsecureDev,
		},
		Jobs: jobsoutbox.JobDeps{
			Jobs:                 jobs.Service,
			SourceVersionQuerier: sourceQuerier,
		},
	}
	// PR 4 (June 2026, refactor/single-qdrant-runtime): wire
	// qd.QdrantDeleter (outbox.VectorPointDeleter; == qd.Runtime.Writer
	// when Qdrant is enabled) directly into outbox.Deps.Jobs.VectorPointDeleter.
	// The previous `any` cast `qd.QdrantDeleter.(jobsoutbox.QdrantDeleter)`
	// is gone: the compile-time assertion at
	// internal/platform/qdrant/index_writer.go pins the
	// conformance (`_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`),
	// and qd.QdrantDeleter's field type is already
	// jobsoutbox.VectorPointDeleter so direct assignment is type-safe.
	if qd.QdrantDeleter != nil {
		outboxDeps.Jobs.VectorPointDeleter = qd.QdrantDeleter
	}
	// PR 3 fix/qdrant-outbox-fail-closed (#4): wire the canonical
	// AssetDeleter so IndexDeleteHandler has BOTH its dep slots
	// populated. *assets.ClipsRepository statically implements the
	// local outbox.AssetDeleter port (compile-time assertion at the
	// top of this file pins GetClip + SoftDelete + SetIndexState
	// conformance). Before this wiring, IndexDeleteHandler
	// registered in a partially-wired state whenever
	// Qdrant.Enabled=true but composer's ClipsRepo wiring failed —
	// every asset.index.delete_requested event then dead-lettered
	// with "no handler for event type X". Fail-closed wiring: only
	// when cfg.Qdrant.Enabled AND ClipsRepo is present.
	if cfg.Qdrant.Enabled && repos.ClipsRepo != nil {
		outboxDeps.Jobs.AssetDeleter = repos.ClipsRepo
	}
	// P0.7 Wave 21 Step 10/12 (June 2026): voiceover orphan cleanup
	// driver (production concrete = drive.Admin, which saturates the
	// narrow VoiceoverCleanupDriver port via its DeleteFile method
	// — structural conformance, no wrapper needed). nil is
	// tolerated — RegisterOptionalHandlers unconditionally registers
	// the handler, and the handler's driver==nil branch logs+skips
	// the Drive delete step (local file removal still runs via
	// stdlib os.Remove, no port ceremony). Production wiring always
	// supplies a non-nil adapter via composition.go (built from
	// driveBundle.Admin).
	if voiceoverDriver != nil {
		outboxDeps.Jobs.VoiceoverCleanupDriver = voiceoverDriver
	}
	return outboxDeps, metadataExportHandler
}

// registerOutboxCoreHandlers registers the fail-closed core outbox
// handlers. Extracted verbatim from BuildOutboxBundle (July 2026);
// the Qdrant-on path fails closed via RegisterCoreHandlers, the
// qdrant-off path registers a no-op IndexingHandler so
// image-generation jobs do not dead-letter their indexing event.
func registerOutboxCoreHandlers(
	eventsRegistry *outboxevents.HandlerRegistry,
	cfg *config.Config,
	repos *wiring.RepoBundle,
	qd *wiring.QdrantDeps,
	outboxDeps *jobsoutbox.Deps,
	log *zap.Logger,
) error {
	// PR 3 fix/qdrant-outbox-fail-closed (#4 + #5): core handlers are
	// fail-closed when Qdrant is enabled. The previous
	// `log.Warn("failed to register outbox events handlers", err)`
	// silently downgraded a wiring bug to a runtime dead-letter on
	// the first asset.index.requested event. Now: cfg.Qdrant.Enabled
	// AND any core dep missing → return err which BuildOutboxBundle
	// propagates up to NewComposition so an operator
	// misconfiguration aborts boot rather than running with a broken
	// outbox.
	if cfg.Qdrant.Enabled {
		if err := jobsoutbox.RegisterCoreHandlers(eventsRegistry, log, qd.ClipIndexerService, outboxDeps); err != nil {
			return fmt.Errorf("BuildOutboxBundle: register core outbox handlers (fail-closed): %w", err)
		}
	} else {
		// Dev / qdrant-off mode: still register a no-op asset.index.requested
		// consumer so image-generation jobs do not dead-letter their indexing
		// event. The handler preserves the envelope validation + supersede
		// checks but routes the final IndexClip call to a no-op concrete.
		sourceQuerier := jobsoutbox.SourceVersionQuerier(nil)
		if repos != nil && repos.ClipsRepo != nil {
			sourceQuerier = repos.ClipsRepo
		}
		if err := eventsRegistry.Register(jobsoutbox.NewIndexingHandler(noopIndexClipper{}, sourceQuerier, log)); err != nil {
			return fmt.Errorf("BuildOutboxBundle: register qdrant-off indexing handler: %w", err)
		}
		log.Info("outbox indexing handler registered in no-op mode because qdrant is disabled")
	}
	return nil
}

// registerOutboxWorkers registers the optional + worker handlers
// (metadata export, script.generate.queued, publish_outbox Publisher,
// publish_drive DriveUploader) and returns the two canonical workers
// for the wiring.OutboxBundle fields. Extracted verbatim from
// BuildOutboxBundle (July 2026).
func registerOutboxWorkers(
	eventsRegistry *outboxevents.HandlerRegistry,
	log *zap.Logger,
	outboxDeps *jobsoutbox.Deps,
	metadataExportHandler outboxevents.Handler,
	jobs *wiring.JobsBundle,
	stagingSvc staging.Store,
	repo artifact.ArtifactStageRepository,
	imageRepo *imagesrepo.ImagesRepository,
	drivePublisher delivery.Publisher,
) (*publishoutbox.Handler, *publishdrive.Handler, error) {
	// Optional handlers: best-effort. Missing deps here are logged
	// and skipped; missing deps do NOT abort boot (delivery,
	// metadata_export, provider_sync are non-essential at boot).
	// Step 2 (June 2026): the pre-built metadataexport.MetadataExportHandler
	// (composition-root owned) is passed to RegisterOptionalHandlers via
	// a new metadataExportHandler arg.
	if err := jobsoutbox.RegisterOptionalHandlers(eventsRegistry, log, outboxDeps, metadataExportHandler); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register optional outbox handlers: %w", err)
	}
	queuedHandler, queuedErr := jobsoutbox.NewScriptGenerateQueuedHandler(jobs.Repo)
	if queuedErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: script.generate.queued handler: %w", queuedErr)
	}
	if regErr := eventsRegistry.Register(queuedHandler); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register script.generate.queued handler: %w", regErr)
	}

	// FASE 3 Push 3.1c (July 2026): register the canonical
	// Promote→Publisher worker. Drains
	// `artifact.publish_requested.v1` events from outbox_events
	// and forwards them to staging.Store.Stage (which then
	// co-emits `artifact.staged.v1` via
	// Repository.InsertWithOutbox — the canonical atomic
	// primitive). Fail-closed: a nil/errored handler
	// registration aborts boot — a half-wired publisher would
	// dead-letter every publish_requested event on the first
	// emission, which is a worse failure mode than a clean
	// compose-time abort.
	publisherHandler, pubErr := publishoutbox.NewHandler(stagingSvc, log)
	if pubErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_outbox.NewHandler (fail-fast at construction): %w", pubErr)
	}
	if regErr := eventsRegistry.Register(publisherHandler); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_outbox handler (fail-closed): %w", regErr)
	}
	log.Info("outbox publish handler registered: artifact.publish_requested.v1 → staging.Store.Stage (FASE 3 Push 3.1c)")

	// FASE 3 Push 3.1e (July 2026): register the canonical
	// Stage→Publish worker. Drains `artifact.staged.v1` events
	// (atomically co-emitted by Repository.InsertWithOutbox in
	// Push 3.1c) and forwards each event to
	// delivery.Publisher.Publish (the canonical Drive upload
	// canal) + Repository.MarkPublished with a canonical JSON
	// PublishedLocation payload. Fail-closed: a nil/errored
	// handler registration aborts boot — a half-wired
	// DriveUploader would dead-letter every staged.v1 event on
	// the first emission, which is a worse failure mode than a
	// clean compose-time abort.
	//
	// The handler consumes the SAME artifact.Repository port
	// that staging.StoreService.Stage uses (canonical single-
	// writer; the Repository is the typed cursor to the same
	// underlying *artifactstages.Repository concrete — godlike/06
	// SSOT per FASE 3 Spina Dorsale). Threading the Repository
	// explicitly into BuildOutboxBundle (rather than re-fetching
	// from a downstream service) keeps the wiring fail-closed:
	// a NULL repo at compose-time is a typed-error abort, not a
	// silent runtime nil-deref.
	driveUploadHandler, driveErr := publishdrive.NewHandler(repo, drivePublisher, log)
	if driveErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_drive.NewHandler (fail-fast at construction): %w", driveErr)
	}
	if regErr := eventsRegistry.Register(driveUploadHandler); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_drive handler (fail-closed): %w", regErr)
	}
	log.Info("outbox publish_drive handler registered: artifact.staged.v1 → delivery.Publisher.Publish + Repository.MarkPublished (FASE 3 Push 3.1e)")

	imageHandler, imageErr := imagesapp.NewImageDriveDeliveryHandler(imageRepo, drivePublisher, log)
	if imageErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: image Drive delivery handler: %w", imageErr)
	}
	if regErr := eventsRegistry.Register(imageDriveDeliveryOutboxAdapter{handler: imageHandler}); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register image Drive delivery handler: %w", regErr)
	}
	log.Info("outbox image Drive delivery handler registered: image.drive_delivery.requested → delivery.Publisher.Publish")

	return publisherHandler, driveUploadHandler, nil
}

// imageDriveDeliveryOutboxAdapter keeps the SQLite outbox envelope at the
// composition boundary. The image capability owns only its payload handler;
// this adapter owns the concrete outboxevents.Handler contract.
type imageDriveDeliveryOutboxAdapter struct {
	handler *imagesapp.ImageDriveDeliveryHandler
}

func (a imageDriveDeliveryOutboxAdapter) EventType() string {
	return imagesapp.EventTypeImageDriveDeliveryRequested
}
func (a imageDriveDeliveryOutboxAdapter) IdempotencyKey() string {
	return imagesapp.EventTypeImageDriveDeliveryRequested + ".v1"
}
func (a imageDriveDeliveryOutboxAdapter) Handle(ctx context.Context, evt outboxevents.Event) error {
	return a.handler.HandlePayload(ctx, evt.PayloadJSON)
}

// registerPerformanceProjectionHandler registers the job.completed
// performance-projection handler. It is best-effort (derived projection): a
// missing DB handle or a construction error logs a Warn and skips — the
// performance-backfill admin command remains the recovery path. It never
// aborts boot.
func registerPerformanceProjectionHandler(eventsRegistry *outboxevents.HandlerRegistry, dbs *wiring.Databases, log *zap.Logger) {
	if eventsRegistry == nil {
		return
	}
	if dbs == nil || dbs.Set == nil || dbs.Set.Primary == nil || dbs.Set.Primary.DB == nil ||
		dbs.Set.Observability == nil || dbs.Set.Observability.DB == nil {
		log.Warn("outbox job.completed performance handler NOT wired (primary/observability DB missing)")
		return
	}
	proj, err := perfstore.NewProjection(dbs.Set.Primary.DB, dbs.Set.Observability.DB)
	if err != nil {
		log.Warn("outbox job.completed performance handler NOT wired", zap.Error(err))
		return
	}
	if err := eventsRegistry.Register(jobCompletedPerformanceAdapter{projection: proj, log: log}); err != nil {
		log.Warn("outbox job.completed performance handler registration failed", zap.Error(err))
		return
	}
	log.Info("outbox job.completed performance handler registered: job.completed → performance_runs/steps projection")
}

// jobCompletedPerformanceAdapter keeps the SQLite outbox envelope at the
// composition boundary (mirrors imageDriveDeliveryOutboxAdapter). The
// performance capability owns only the ProjectionService port; this adapter
// owns the concrete outboxevents.Handler contract and extracts the job id
// from the event envelope before delegating to the projection.
type jobCompletedPerformanceAdapter struct {
	projection capperformance.ProjectionService
	log        *zap.Logger
}

func (a jobCompletedPerformanceAdapter) EventType() string {
	return outboxevents.EventJobCompleted
}

func (a jobCompletedPerformanceAdapter) IdempotencyKey() string {
	return outboxevents.EventJobCompleted + ".project.v1"
}

func (a jobCompletedPerformanceAdapter) Handle(ctx context.Context, evt outboxevents.Event) error {
	jobID := evt.AggregateID
	if jobID == "" {
		var payload struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err == nil && payload.JobID != "" {
			jobID = payload.JobID
		}
	}
	if jobID == "" {
		return fmt.Errorf("job.completed performance handler: missing job id (aggregate_id=%q)", evt.AggregateID)
	}
	if err := a.projection.ProjectCompletedJob(ctx, jobID); err != nil {
		// Retryable: the run report may not be finalized yet, or the
		// projection hit a transient DB failure. A permanently missing
		// run surfaces via dead-letter after max attempts (fail closed).
		return fmt.Errorf("job.completed performance projection for %q: %w", jobID, err)
	}
	a.log.Debug("job.completed performance projected", zap.String("job_id", jobID))
	return nil
}

// noopIndexClipper is the qdrant-off IndexClip no-op concrete used by
// the dev-mode IndexingHandler registration (registerOutboxCoreHandlers).
type noopIndexClipper struct{}

func (noopIndexClipper) IndexClip(context.Context, string) error { return nil }
