package wiring

import (
	"context"
	"database/sql"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	apiMw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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

	// ClipRenderParentAggregator is the ONE clip.render parent finalisation
	// authority, shared by the event-driven notifier (the worker's child
	// completion path) and the recovery sweeper (the lifecycle step). Cached
	// here so both composition sites use the same instance instead of each
	// building their own.
	ClipRenderParentAggregator *cliprender.ParentAggregator

	// DriveUploadGate is the ONE process-wide fair gate that bounds concurrent
	// Google Drive uploads. Two composition sites publish to Drive — the
	// voiceover per-item publisher (build_bundles_voiceover.go, wired inside
	// BuildDomainBundle) and the certified final-audio publisher
	// (final_audio_publisher.go, wired inside BuildScriptGenerationRuntime) —
	// and `voiceover.max_concurrent_drive_uploads` is a PROCESS-WIDE ceiling
	// ("limits parallel Google Drive upload calls"), not a per-publisher one.
	// Cached here so both sites acquire the SAME gate: if each built its own,
	// the configured ceiling would be multiplied by the number of publishers
	// and cross-job contention (measured 2026-09-28: one final_audio upload
	// waited 85.7 s for ~5 s of work) would stay invisible to the gate.
	//
	// Storage site follows the ClipRenderParentAggregator precedent: the
	// composition root is the only place that holds both publishers, so it is
	// where the shared instance belongs.
	DriveUploadGate *concurrent.FairSemaphore

	DB              *storage.SQLiteDB
	ObservabilityDB *storage.SQLiteDB
	CacheDB         *storage.SQLiteDB
	MediaPostgres   *sql.DB

	Drive      *DriveBundle
	Repos      *RepoBundle
	Media      *MediaRepoBundle
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
