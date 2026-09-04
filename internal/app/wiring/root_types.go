package wiring

import (
	"context"
	"database/sql"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	apiMw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors.
type IOpaqueStartFunc func() error

// ComposeRoot is the assembled composition tree. Bundle ownership is split
// across focused type files; this root contains only the graph itself.
type ComposeRoot struct {
	CanonicalAssetWriter assetspersistence.CanonicalAssetWriter
	MediaExec            mediaexec.ExecutionConfig
	ClipRenderRuntime    *ClipRenderRuntime

	DB              *storage.SQLiteDB
	ObservabilityDB *storage.SQLiteDB
	CacheDB         *storage.SQLiteDB
	MediaPostgres   *sql.DB

	Drive      *DriveBundle
	Repos      *RepoBundle
	Search     *SearchBundle
	Process    *ProcessBundle
	TextTracks *TextTrackBundle

	AI      *AIBundle
	Domains *DomainBundle
	Jobs    *JobsBundle
	Outbox  *OutboxBundle
	Sync    *SyncBundle
	Maint   *MaintBundle
	Utility *UtilityBundle

	Staging   *StagingBundle
	Finalizer *FinalizerBundle

	DriveStart            IOpaqueStartFunc
	OutboxStart           IOpaqueStartFunc
	IdempotencyMiddleware *apiMw.Idempotency
	Ctx                   context.Context
}
