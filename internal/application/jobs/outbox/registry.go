// Package outboxhandlers wires concrete handlers into an
// outboxevents.HandlerRegistry. Each handler is responsible for one event type.
package outbox

import (
	"database/sql"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// DeliveryDeps owns optional outbound-delivery and provider-sync inputs.
type DeliveryDeps struct {
	DB          *sql.DB
	HTTPClient  *http.Client
	HMACSecrets [][]byte
	InsecureDev bool
	Jobs        JobsEnqueuer
}

// IndexingDeps owns Qdrant indexing and deletion inputs.
type IndexingDeps struct {
	VectorPointDeleter   VectorPointDeleter
	AssetDeleter         AssetDeleter
	SourceVersionQuerier SourceVersionQuerier
}

// VoiceoverCleanupDeps owns the optional orphan-cleanup driver.
type VoiceoverCleanupDeps struct {
	VoiceoverCleanupDriver VoiceoverCleanupDriver
}

// DriveDeletionDeps owns the deletion state-machine ports.
type DriveDeletionDeps struct {
	DrivePatchLifecycle  LifecycleStateReader
	DrivePatchLifecycleW ClipsLifecycleStateWriter
	DrivePatchStateAdv   StateAdvancer
	DriveDeleteHandler   DriveDeleter
}

// Deps bundles optional handler dependencies through four capability groups.
// Anonymous embedding preserves existing selector and literal behavior while
// keeping the visible dependency surface below the architecture cap.
type Deps struct {
	DeliveryDeps
	IndexingDeps
	VoiceoverCleanupDeps
	DriveDeletionDeps
}

// RegisterAll is the legacy best-effort wrapper around core and optional
// registration. Missing core inputs are logged and skipped rather than returned.
func RegisterAll(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps, metadataExportHandler outboxevents.Handler) error {
	if registry == nil {
		return fmtError("outbox RegisterAll: registry is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	coreHandlers := []outboxevents.Handler{}
	if indexer != nil && deps != nil && deps.SourceVersionQuerier != nil {
		coreHandlers = append(coreHandlers, buildIndexingHandler(indexer, deps.SourceVersionQuerier, log))
	} else {
		log.Info("outbox RegisterAll (legacy): IndexingHandler skipped (missing indexer or SourceVersionQuerier)")
	}
	if deps != nil && deps.VectorPointDeleter != nil && deps.AssetDeleter != nil {
		coreHandlers = append(coreHandlers, NewIndexDeleteHandler(log, deps.VectorPointDeleter, deps.AssetDeleter))
	} else {
		log.Info("outbox RegisterAll (legacy): IndexDeleteHandler skipped (missing VectorPointDeleter or AssetDeleter)")
	}
	for _, h := range coreHandlers {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	return RegisterOptionalHandlers(registry, log, deps, metadataExportHandler)
}

// RegisterCoreHandlers wires handlers that must exist when Qdrant is enabled.
func RegisterCoreHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps) error {
	if registry == nil {
		return fmtError("outbox RegisterCoreHandlers: registry is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	if indexer == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexingHandler mandatory dep missing (indexer=nil; buildQdrantDeps did not construct a ClipIndexer service)")
	}
	if deps == nil {
		return fmtError("outbox RegisterCoreHandlers: deps is nil (composition omitted outbox.Deps — wiring bug)")
	}
	if deps.SourceVersionQuerier == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexingHandler source-version gate cannot run (SourceVersionQuerier=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	if deps.VectorPointDeleter == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (VectorPointDeleter=nil; Qdrant enabled but no IndexWriter built from buildQdrantDeps)")
	}
	if deps.AssetDeleter == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (AssetDeleter=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	core := []outboxevents.Handler{
		buildIndexingHandler(indexer, deps.SourceVersionQuerier, log),
		NewIndexDeleteHandler(log, deps.VectorPointDeleter, deps.AssetDeleter),
	}
	for _, h := range core {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	log.Info("outbox core handlers registered (fail-closed contract when cfg.Qdrant.Enabled)",
		zap.Int("registered", len(core)),
	)
	return nil
}

// RegisterOptionalHandlers wires best-effort handlers. Missing optional
// dependencies skip only the corresponding handler.
func RegisterOptionalHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, deps *Deps, metadataExportHandler outboxevents.Handler) error {
	if registry == nil {
		return fmtError("outbox RegisterOptionalHandlers: registry is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	optional := []outboxevents.Handler{
		&WorkflowStepCompletedHandler{log: log},
		&WorkflowStepFailedHandler{log: log},
	}
	if metadataExportHandler != nil {
		optional = append(optional, metadataExportHandler)
	}
	if deps != nil {
		if (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev) {
			optional = append(optional, NewDeliveryHandler(log, deps.HTTPClient, deps.DB, deps.HMACSecrets, deps.InsecureDev))
		}
	}
	optional = append(optional, NewProviderSyncHandler(log, depsOrNil(deps).Jobs))
	optional = append(optional, NewVoiceoverCleanupHandler(depsOrNil(deps).VoiceoverCleanupDriver, log))

	if deps != nil &&
		deps.DrivePatchLifecycle != nil &&
		deps.DrivePatchLifecycleW != nil &&
		deps.DrivePatchStateAdv != nil &&
		deps.DriveDeleteHandler != nil {
		optional = append(optional, NewDriveDeleteHandler(
			log,
			deps.DriveDeleteHandler,
			deps.DrivePatchLifecycle,
			deps.DrivePatchLifecycleW,
			deps.DrivePatchStateAdv,
		))
	}
	for _, h := range optional {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	log.Info("outbox optional handlers registered",
		zap.Int("registered", len(optional)),
		zap.Bool("metadata_export_wired", metadataExportHandler != nil),
		zap.Bool("delivery_wired", deps != nil && (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev)),
		zap.Bool("provider_sync_jobs_wired", deps != nil && deps.Jobs != nil),
		zap.Bool("voiceover_cleanup_driver_wired", deps != nil && deps.VoiceoverCleanupDriver != nil),
	)
	return nil
}

func depsOrNil(d *Deps) *Deps {
	if d == nil {
		return &Deps{}
	}
	return d
}

func fmtError(msg string) error { return &registryError{msg: msg} }

type registryError struct{ msg string }

func (e *registryError) Error() string { return e.msg }

// buildIndexingHandler auto-wires the indexer state-updater when the concrete
// indexer supports it, preserving the sentinel-driven state-write path.
func buildIndexingHandler(indexer IndexClipper, sourceQuerier SourceVersionQuerier, log *zap.Logger) *IndexingHandler {
	h := NewIndexingHandler(indexer, sourceQuerier, log)
	if su, ok := indexer.(clipindexer.IndexerStateUpdater); ok {
		h.WithStateUpdater(su)
		return h
	}
	log.Info("outbox buildIndexingHandler: indexer does not implement IndexerStateUpdater; sentinel-driven state-write will be skipped (retry path still fires)",
		zap.String("indexer_type", fmt.Sprintf("%T", indexer)),
	)
	return h
}
