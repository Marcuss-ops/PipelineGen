// Package app — outbox deps + handler-registration sub-blocks.
//
// Extracted from build_bundles_process.go::BuildOutboxBundle (July 2026
// sub-section split). Owns: buildOutboxDeps (outbox.Deps construction
// incl. httpClient/HMAC secrets/source querier/metadata-export handler),
// assertSingleMediaIndexOwner (fail-closed single-owner assertion for the
// media index plane) and registerOutboxWorkers (optional +
// script.generate.queued + publisher + drive-uploader workers). All
// error strings keep the canonical "BuildOutboxBundle:" prefix so the
// fail-closed contract observed by composition_failclosed_test.go is
// unchanged.
package wiring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	assetmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	publishdrive "github.com/Marcuss-ops/PipelineGen/internal/capabilities/publish_drive"
	publishoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/publish_outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/metadataexport"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpclient"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

// buildOutboxDeps constructs the jobsoutbox.Deps consumed by the core +
// optional handler registrations and the pre-built
// metadataexport.MetadataExportHandler. Extracted verbatim from
// BuildOutboxBundle (July 2026); called by the reduced orchestrator.
func buildOutboxDeps(
	dbs *Databases,
	cfg *config.Config,
	repos *RepoBundle,
	jobs *JobsBundle,
	qd *QdrantDeps,
	voiceoverDriver jobsoutbox.VoiceoverCleanupDriver,
	mediaPostgres *sql.DB,
	log *zap.Logger,
) (*jobsoutbox.Deps, outboxevents.Handler) {
	// PR-REFACTOR-P0-IO-BINDER-HTTP (July 2026): route the outbox http.Client
	// construction through internal/platform/httpclient.NewDefaultClient
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

	// MEDIA-CUTOVER (2026-09-12): the SourceVersionQuerier wiring is
	// REMOVED with the retired IndexingHandler family. The supersede gate
	// it fed consumed SQLite-outbox asset.index.requested events — a
	// pipeline with zero production consumers since the PostgreSQL media
	// cutover (media index plane = PostgresIndexWorker).

	// Step 2 (June 2026): pre-build the canonical MetadataExportHandler
	// via the new typed-port adapters. The composition root is the ONLY
	// place infra concrete types meet application ports — the
	// outbox.Deps struct no longer needs MetadataDir because the
	// handler gets its output dir as part of HandlerDeps at wire time.
	var metadataExportResolver assetmetadata.AssetResolver
	if mediaPostgres != nil {
		metadataExportResolver = pgmedia.NewMetadataExportResolver(mediaPostgres, dbs.DualPool.Writer)
	}
	if metadataExportResolver == nil {
		metadataExportResolver = sqmetadataexport.NewSQLiteAdapter(dbs.DualPool.Writer)
	}
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
			Jobs: jobs.Service,
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

// assertSingleMediaIndexOwner is the fail-closed composition assertion that
// replaced the retired registerOutboxCoreHandlers no-op (POSTGRES-MEDIA-CUTOVER,
// September 2026). The previous function registered nothing and returned nil in
// every mode, so its six parameters were dead architecture kept alive only by
// `_ = param` pins; a no-op cannot fail closed, so it could not actually enforce
// the invariant its log line described.
//
// The invariant it now *enforces* at boot is that the media index plane has
// exactly one owner — the pgvector PostgresIndexWorker over the PostgreSQL SSOT:
//
//   - PostgreSQL media enabled but no worker built => the plane has NO owner.
//     Boot aborts: continuing would dead-letter every asset.index.requested.
//   - the SQLite control-plane outbox registering a consumer for
//     asset.index.requested => a SECOND owner. Boot aborts: a stray media event
//     must dead-letter loudly rather than project into a third plane.
//
// The retired Qdrant media projection branch is gone, so there is no ordering
// dependency left and this assertion takes no Repository/Qdrant handles.
func assertSingleMediaIndexOwner(
	cfg *config.Config,
	eventsRegistry *outboxevents.HandlerRegistry,
	pgIndexWorker *pgmedia.PostgresIndexWorker,
	log *zap.Logger,
) error {
	if cfg == nil {
		return fmt.Errorf("BuildOutboxBundle: media index owner assertion requires a config")
	}
	if cfg.MediaPostgreSQL.Enabled && pgIndexWorker == nil {
		return fmt.Errorf("BuildOutboxBundle: media PostgreSQL is enabled but no PostgresIndexWorker was built; the media index plane would have no owner (POSTGRES-MEDIA-CUTOVER)")
	}
	if eventsRegistry != nil {
		if _, registered := eventsRegistry.Get(outboxevents.EventAssetIndexRequested); registered {
			return fmt.Errorf("BuildOutboxBundle: SQLite outbox registered a handler for %q; the media index plane is owned by the PostgreSQL PostgresIndexWorker (POSTGRES-MEDIA-CUTOVER)", outboxevents.EventAssetIndexRequested)
		}
	}
	log.Info("POSTGRES-MEDIA-CUTOVER: single media index owner asserted; media index plane = pgvector PostgresIndexWorker",
		zap.Bool("postgres_media_enabled", cfg.MediaPostgreSQL.Enabled))
	return nil
}

// registerOutboxWorkers registers the optional + worker handlers
// (metadata export, script.generate.queued, publish_outbox Publisher,
// publish_drive DriveUploader) and returns the two canonical workers
// for the OutboxBundle fields. Extracted verbatim from
// BuildOutboxBundle (July 2026).
func registerOutboxWorkers(
	eventsRegistry *outboxevents.HandlerRegistry,
	log *zap.Logger,
	outboxDeps *jobsoutbox.Deps,
	metadataExportHandler outboxevents.Handler,
	jobs *JobsBundle,
	stagingSvc staging.Store,
	repo detail.ArtifactStageRepository,
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

	// NB: image.drive_delivery.requested is deliberately NOT registered on the
	// SQLite outbox. The canonical media committer emits it into the
	// PostgreSQL outbox (see registerPostgresMediaOutboxHandlers), so a SQLite
	// registration could never receive an event: it would only be a second
	// consumer for the same fact. The image Drive handler is wired once, on
	// the PG worker.

	return publisherHandler, driveUploadHandler, nil
}

// registerPostgresMediaOutboxHandlers wires external delivery consumers into
// the same PostgreSQL outbox that the canonical media committer writes. This
// is deliberately separate from registerOutboxWorkers: the latter drains the
// control-plane SQLite outbox, while media assets are owned by PostgreSQL.
func registerPostgresMediaOutboxHandlers(
	worker *pgmedia.PostgresIndexWorker,
	drivePublisher delivery.Publisher,
	mutator assetspersistence.AssetMutator,
	imageRepo *imagesrepo.ImagesRepository,
	log *zap.Logger,
) error {
	if worker == nil {
		return nil
	}
	clipHandler, clipErr := newClipRenderDriveDeliveryHandler(drivePublisher, mutator, log)
	if clipErr != nil {
		return fmt.Errorf("clip.render PostgreSQL Drive delivery handler: %w", clipErr)
	}
	if err := worker.RegisterHandler(cliprender.EventClipRenderDriveDeliveryRequested, clipHandler); err != nil {
		return fmt.Errorf("register clip.render PostgreSQL Drive delivery handler: %w", err)
	}
	worker.WithOutboxStatusMetrics(
		observability.NewMediaOutboxStatusCountAdapter(),
		cliprender.EventClipRenderDriveDeliveryRequested,
	)
	if imageRepo != nil {
		imageHandler, imageErr := imagesapp.NewImageDriveDeliveryHandler(imageRepo, drivePublisher, log)
		if imageErr != nil {
			return fmt.Errorf("image PostgreSQL Drive delivery handler: %w", imageErr)
		}
		if err := worker.RegisterHandler(imagesapp.EventTypeImageDriveDeliveryRequested, imageDriveDeliveryPGHandler{handler: imageHandler}); err != nil {
			return fmt.Errorf("register image PostgreSQL Drive delivery handler: %w", err)
		}
	}
	log.Info("PostgreSQL media outbox Drive delivery handlers registered")
	return nil
}

// registerPerformanceProjectionHandler registers the job.completed
// performance-projection handler. It is best-effort (derived projection): a
// missing DB handle or a construction error logs a Warn and skips — the
// performance-backfill admin command remains the recovery path. It never
// aborts boot.
func registerPerformanceProjectionHandler(eventsRegistry *outboxevents.HandlerRegistry, dbs *Databases, executionDB *storage.SQLiteDB, registryDB *storage.SQLiteDB, log *zap.Logger) {
	if eventsRegistry == nil {
		return
	}
	if dbs == nil || dbs.Set == nil || executionDB == nil || executionDB.DB == nil || registryDB == nil || registryDB.DB == nil ||
		dbs.Set.Observability == nil || dbs.Set.Observability.DB == nil {
		log.Warn("outbox job.completed performance handler NOT wired (execution/registry/observability DB missing)")
		return
	}
	var performanceTable string
	if err := registryDB.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='performance_runs'`).Scan(&performanceTable); err != nil || performanceTable == "" {
		log.Warn("outbox job.completed performance handler skipped (performance registry table is unavailable)")
		return
	}
	proj, err := perfstore.NewSplitProjection(executionDB.DB, registryDB.DB, dbs.Set.Observability.DB)
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
// composition boundary. The
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
