package wiring

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/maintenance"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// OutboxBundle aggregates the canonical outbox dispatcher and event workers.
type OutboxBundle struct {
	CanonicalWriter  assetspersistence.CanonicalAssetWriter
	Dispatcher       *outbox.Dispatcher
	EventsRepo       *outboxevents.Repository
	EventsRegistry   *outboxevents.HandlerRegistry
	EventsPool       *outboxevents.Pool
	JobsEventsPool   *outboxevents.Pool
	MediaIndexWorker *pgmedia.PostgresIndexWorker
	Publisher        outboxevents.Handler
	DriveUploader    outboxevents.Handler
}

// SyncBundle owns only catalog-to-Drive synchronization.
type SyncBundle struct {
	CatalogSync *catalogsync.Service
}

// MaintBundle owns periodic maintenance and deletion services.
type MaintBundle struct {
	MaintenanceSvc             *maintenance.Service
	DeletionSvc                *deletion.DeletionService
	EntityImageRecertification *entitycatalog.RecertificationService
}

// UtilityBundle owns lightweight system utility services.
type UtilityBundle struct {
	HealthService *systemhealth.Service
	ReadyChecker  *systemhealth.ReadyChecker
}

// AssetsWiring holds the externally exposed assets module wiring.
type AssetsWiring struct {
	Module               module.Module
	DeletionSvc          *deletion.DeletionService
	InternalMediaHandler *assetstorage.Handler
	SearchAggregator     *assetsearch.Aggregator
	SearchFanOut         assetsearch.SearchFanOut
}
