package wiring

// finalize_drive_outbox.go owns the composition-root consumer for the
// deferred post_writer_finalize Drive publication (L10). The finalize spine
// commits the artifact asset and this event atomically; this consumer performs
// the slow external upload later — off the user's critical path — and then
// reconciles the canonical Drive location, exactly like the clip.render and
// overlay Drive consumers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// finalizeDriveDeliveryHandler drains finalize.drive_delivery.requested.v1.
// The publication itself goes through the SAME ArtifactPreparation the
// synchronous path uses, so folder resolution, destination routing and the
// per-artifact IdempotencyKey are byte-identical — only the timing changes.
type finalizeDriveDeliveryHandler struct {
	preparation finalization.ArtifactPreparationService
	mutator     persistence.AssetMutator
	objects     *cas.Store
	// observer binds a delivery run so the real Drive upload lands in
	// run_operation_observations (the overlay/clip outbox precedent). Nil
	// degrades to the unmeasured pass-through: instrumentation must never
	// block a publication.
	observer *kernobs.RunObserver
	log      *zap.Logger
}

func newFinalizeDriveDeliveryHandler(
	preparation finalization.ArtifactPreparationService,
	mutator persistence.AssetMutator,
	objects *cas.Store,
	observer *kernobs.RunObserver,
	log *zap.Logger,
) (*finalizeDriveDeliveryHandler, error) {
	if preparation == nil {
		return nil, fmt.Errorf("finalize Drive delivery: artifact preparation is required")
	}
	if mutator == nil {
		return nil, fmt.Errorf("finalize Drive delivery: asset mutator is required")
	}
	if objects == nil {
		return nil, fmt.Errorf("finalize Drive delivery: object store is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &finalizeDriveDeliveryHandler{preparation: preparation, mutator: mutator, objects: objects, observer: observer, log: log}, nil
}

func (h *finalizeDriveDeliveryHandler) EventType() string {
	return finalization.EventFinalizeDriveDeliveryRequested
}

func (h *finalizeDriveDeliveryHandler) IdempotencyKey() string {
	return finalization.EventFinalizeDriveDeliveryRequested
}

func (h *finalizeDriveDeliveryHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	if h == nil || h.preparation == nil || h.mutator == nil || h.objects == nil {
		return fmt.Errorf("finalize Drive delivery: handler is not configured")
	}
	var req finalization.FinalizeDriveDeliveryRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("finalize Drive delivery: decode payload: %w", err)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	// The certified object-store copy is the artifact: the completion already
	// hashed it against the manifest digest, and ArtifactPreparation re-verifies
	// size + SHA-256 before a single byte leaves for Drive.
	objectPath, err := h.objects.LocalPath(req.StorageKey)
	if err != nil {
		return fmt.Errorf("finalize Drive delivery %s: resolve storage key %s: %w", req.ArtifactID, req.StorageKey, err)
	}
	verified := req.ToVerifiedArtifact()
	verified.LocalPath = objectPath

	// Measurement parity with the overlay outbox publisher: when the dispatch
	// context carries no run (the normal outbox case) bind a delivery run so
	// the real upload cost is observable per job.
	publishCtx := ctx
	if kernobs.FromContext(ctx) == nil && h.observer != nil {
		delivery := h.observer.StartRun(ctx, kernobs.RunInfo{
			JobID:     req.JobID,
			JobType:   finalization.EventFinalizeDriveDeliveryRequested,
			AttemptID: kernobs.NewAttemptID(),
		})
		publishCtx = kernobs.WithRun(ctx, delivery)
		defer delivery.Finish()
	}
	metadataJSON, err := json.Marshal(map[string]any{
		"path":          "outbox",
		"job_id":        req.JobID,
		"artifact_id":   req.ArtifactID,
		"storage_key":   req.StorageKey,
		"event_id":      evt.ID,
		"artifact_sha":  req.SHA256,
		"artifact_size": req.SizeBytes,
	})
	if err != nil {
		return fmt.Errorf("finalize Drive delivery %s: encode measurement metadata: %w", req.ArtifactID, err)
	}
	var published finalization.PublishedArtifact
	if err := kernobs.MeasureOperation(publishCtx, kernobs.OperationInfo{
		Stage:        kernobs.StagePostWriterFinalize,
		Component:    kernobs.ComponentDrive,
		Operation:    kernobs.OperationDrivePublish,
		Provider:     "drive",
		Items:        1,
		Bytes:        req.SizeBytes,
		MetadataJSON: string(metadataJSON),
	}, func(measureCtx context.Context) error {
		var publishErr error
		published, publishErr = h.preparation.Prepare(measureCtx, verified)
		return publishErr
	}); err != nil {
		return fmt.Errorf("finalize Drive delivery %s: %w", req.ArtifactID, err)
	}
	if published.Location.FileID == "" {
		return fmt.Errorf("finalize Drive delivery %s: Drive returned no file id", req.ArtifactID)
	}
	// Reconcile with the byte identity this delivery proved (the clip.render
	// lesson: reconciling only the Drive id forced the writer to default
	// mime/size columns).
	deliveredMime := strings.TrimSpace(published.MIMEType)
	if deliveredMime == "" {
		deliveredMime = mime.TypeByExtension(filepath.Ext(req.Filename))
	}
	if deliveredMime == "" {
		deliveredMime = "application/octet-stream"
	}
	if err := h.mutator.ReconcileDriveLocations(ctx, []persistence.DriveLocationPatch{{
		AssetID:       req.AssetID,
		DriveFileID:   published.Location.FileID,
		DriveLink:     published.Location.WebViewLink,
		DownloadURL:   published.Location.DownloadLink,
		FileSizeBytes: req.SizeBytes,
		MimeType:      deliveredMime,
	}}); err != nil {
		return fmt.Errorf("finalize Drive delivery %s: reconcile Drive location: %w", req.ArtifactID, err)
	}
	patch, err := json.Marshal(map[string]any{
		"delivery_status": "completed",
		"drive_file_id":   published.Location.FileID,
		"drive_link":      published.Location.WebViewLink,
	})
	if err != nil {
		return fmt.Errorf("finalize Drive delivery %s: encode metadata patch: %w", req.ArtifactID, err)
	}
	patchJSON := string(patch)
	if err := h.mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: req.AssetID, MetadataPatchJSON: &patchJSON}); err != nil {
		return fmt.Errorf("finalize Drive delivery %s: mark delivery complete: %w", req.ArtifactID, err)
	}
	// No staged-copy cleanup: the artifact bytes live in the content-addressed
	// object store, where reuse dedupes and lifecycle is the store's own.
	h.log.Info("finalize artifact Drive delivery completed",
		zap.String("job_id", req.JobID),
		zap.String("artifact_id", req.ArtifactID),
		zap.String("drive_file_id", published.Location.FileID),
		zap.Int64("size_bytes", req.SizeBytes))
	return nil
}

// registerFinalizeDriveDeliveryHandler wires the consumer onto the SQLite
// outbox worker pool and returns nothing on success. Registration is
// fail-closed at composition time: a half-wired consumer would dead-letter
// every delivery intent on first emission.
func registerFinalizeDriveDeliveryHandler(
	registry *outboxevents.HandlerRegistry,
	preparation finalization.ArtifactPreparationService,
	mutator persistence.AssetMutator,
	objects *cas.Store,
	observer *kernobs.RunObserver,
	log *zap.Logger,
) error {
	if registry == nil {
		return fmt.Errorf("register finalize Drive delivery handler: outbox registry is required")
	}
	handler, err := newFinalizeDriveDeliveryHandler(preparation, mutator, objects, observer, log)
	if err != nil {
		return err
	}
	if err := registry.Register(handler); err != nil {
		return fmt.Errorf("register finalize Drive delivery handler: %w", err)
	}
	return nil
}

// finalizeDriveOutboxRegistry selects the registry that DRAINS the outbox
// plane the job finalizer enqueues its delivery intents into. The split jobs
// plane (jobs.db) owns its own handler registry and pool, separate from the
// media outbox registry: registering the consumer on the media registry while
// the intent lands in the jobs outbox dead-letters every intent with
// "no handler registered" (observed on the first L10 verification run,
// 2026-10-26). splitJobsPlane mirrors the finalizer's own enqueue-plane
// selection (root.Jobs present → jobs.db outbox).
func finalizeDriveOutboxRegistry(eventsRegistry, jobsRegistry *outboxevents.HandlerRegistry, splitJobsPlane bool) *outboxevents.HandlerRegistry {
	if splitJobsPlane && jobsRegistry != nil {
		return jobsRegistry
	}
	return eventsRegistry
}

// buildFinalizeDriveObjectStore opens (or creates) the local content-addressed
// object store (H1) the deferred delivery stages artifacts through. It shares
// the canonical <data>/cas root with the other CAS clients: objects are
// content-addressed, so concurrent producers dedup instead of colliding.
func buildFinalizeDriveObjectStore(cfg *config.Config) (*cas.Store, error) {
	if cfg == nil {
		return nil, fmt.Errorf("finalize Drive delivery: config is required")
	}
	casRoot := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(casRoot, ".staging")})
	if err != nil {
		return nil, fmt.Errorf("finalize Drive delivery: local store: %w", err)
	}
	store, err := cas.NewStore(cas.Config{Root: casRoot, Stager: stager})
	if err != nil {
		return nil, fmt.Errorf("finalize Drive delivery: cas store: %w", err)
	}
	return store, nil
}

// finalizeDriveDeliveryObserver builds the delivery-run observer that makes
// the real Drive upload measurable (mirrors wireDurableOverlayPublisher). Nil
// when the observability database is unavailable — publication never depends
// on instrumentation.
func finalizeDriveDeliveryObserver(observabilityDB *sql.DB) *kernobs.RunObserver {
	if observabilityDB == nil {
		return nil
	}
	return kernobs.NewRunObserverWithCollector(
		obsmetrics.NewSQLiteRecorder(observabilityDB),
		obsmetrics.NewRunReportsCollector(),
	)
}
