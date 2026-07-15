// Package outboxhandlers wires concrete handlers into an outboxevents.HandlerRegistry.
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

// Deps bundles the optional dependencies forwarded to outbox handlers. Four
// embedded capability groups replace the previous twelve-field flat bag while
// retaining promoted selectors for existing registration code and call sites.
type Deps struct {
	DeliveryDeps
	IndexingDeps
	VoiceoverCleanupDeps
	DriveDeletionDeps
}

// IndexClipper is declared in indexing.go (canonical owner).

// RegisterAll wires the canonical set of handlers into the registry.
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

// RegisterCoreHandlers wires handlers that are mandatory when Qdrant is enabled.
func RegisterCoreHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps) error {
	if registry == nil {
		return fmtError("outbox RegisterCoreHandlers: registry is nil")
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
	log.Info("outbox core handlers registered (fail-closed contract when cfg.Qdrant.Enabled)", zap.Int("registered", len(core)))
	return nil
}

// RegisterOptionalHandlers wires handlers that tolerate missing dependencies.
func RegisterOptionalHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, deps *Deps, metadataExportHandler outboxevents.Handler) error {
	if registry == nil {
		return fmtError("outbox RegisterOptionalHandlers: registry is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	if deps == nil {
		deps = &Deps{}
	}

	for _, h := range []outboxevents.Handler{
		NewWorkflowStepCompletedHandler(log),
		NewWorkflowStepFailedHandler(log),
	} {
		if err := registry.Register(h); err != nil {
			return err
		}
	}

	if metadataExportHandler != nil {
		if err := registry.Register(metadataExportHandler); err != nil {
			return err
		}
	}

	if deps.DB != nil || deps.HTTPClient != nil {
		if len(deps.HMACSecrets) > 0 || deps.InsecureDev {
			h := NewDeliveryHandler(deps.DB, deps.HTTPClient, deps.HMACSecrets, deps.InsecureDev, log)
			if err := registry.Register(h); err != nil {
				return err
			}
		} else {
			log.Info("outbox DeliveryHandler skipped (no HMAC secrets and insecure dev disabled)")
		}
	}

	if deps.Jobs != nil || deps.DB != nil {
		if err := registry.Register(NewProviderSyncHandler(deps.Jobs, deps.DB, log)); err != nil {
			return err
		}
	}

	if err := registry.Register(NewVoiceoverCleanupHandler(log, deps.VoiceoverCleanupDriver)); err != nil {
		return err
	}

	if deps.DrivePatchLifecycle != nil && deps.DrivePatchLifecycleW != nil && deps.DrivePatchStateAdv != nil && deps.DriveDeleteHandler != nil {
		if err := registry.Register(NewDriveDeleteHandler(
			deps.DrivePatchLifecycle,
			deps.DrivePatchLifecycleW,
			deps.DrivePatchStateAdv,
			deps.DriveDeleteHandler,
			log,
		)); err != nil {
			return err
		}
	}
	return nil
}

func fmtError(msg string) error { return fmt.Errorf("%s", msg) }

var _ = clipindexer.IndexingHandler(nil)
