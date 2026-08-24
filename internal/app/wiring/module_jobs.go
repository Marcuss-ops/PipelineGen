package wiring

import (
	"fmt"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	platformjobregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
)

// JobsBundle is the Job module's *owned* runtime surface.
//
// Phase-B ownership inversion (June 2026): these objects are constructed
// once by BuildJobsBundle, returned as a typed bundle, and consumed by
// composeIntegration for cross-module handler registration.
//
// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): 4 cross-domain deps polluted
// (VoiceoverRepo + ImagesRepo + DriveUploader + DriveLifecycle) so
// buildClipOpsPorts(clipRepo, jobs) stays strict 2-arg per the user
// spec. The Dispatcher field stays the canonical *appjobs.Dispatcher
// for job-broker mechanics — the ClipOpsService consumes a different
// typed port (ClipIndexDispatcherPort) inline at the wire_assets_clips.go:187
// call site, NOT through JobsBundle (type mismatch).
type JobsBundle struct {
	Repo       *sqljobs.SQLiteStore
	Dispatcher *appjobs.Dispatcher
	Service    *appjobs.Service
	Facade     job.Service // canonical domain interface satisfied by *appjobs.Service
	Broker     appjobs.CompletionPort
	JobLedger  capjobregistry.Registry
	History    appjobs.HistoryReader

	// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): cross-domain deps
	// threaded into buildClipOpsPorts via the strict 2-arg jobs parameter.
	VoiceoverRepo  *assets.VoiceoversRepository
	ImagesRepo     *imagesrepo.ImagesRepository
	DriveUploader  *drive.Uploader
	DriveLifecycle drive.FileLifecycle
}

// BuildJobsBundle constructs the Job runtime pieces in the canonical order:
//
//  1. SQLite-backed job Store.
//  2. In-process Dispatcher (handler registry; kept nil-free until Freeze).
//  3. The application Service that orchestrates enqueue / list / cancel /
//     status propagation.
//  4. The domain job.Service interface satisfied by *appjobs.Service.
//
// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): the 4 trailing args (voiceoverRepo,
// imagesRepo, driveUploader, driveLifecycle) are the cross-domain deps
// polluted into JobsBundle so buildClipOpsPorts(clipRepo, jobs) stays
// strict 2-arg at the clips composition site. All 4 are nil-tolerant —
// the underlying adapter constructors (newVoiceoverRepoAdapter,
// newImageRepoAdapter, newClipsDriveAdapter) all return nil on nil
// input, so test fixtures may pass nil for any subset.
//
// Returns a JobsBundle. The bundle is fully constructed but its Dispatcher
// is NOT frozen yet — freezing is performed by WireServices in bootstrap.go
// *after* WireRegistry, so that no new module can register a handler while
// workers are claiming jobs.
//
// Returning `(nil, error)` is reserved for unrecoverable construction errors
// (nil db / nil logger). All four fields are required to be non-nil on success.
func BuildJobsBundle(
	db *storage.SQLiteDB,
	log *zap.Logger,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *imagesrepo.ImagesRepository,
	driveUploader *drive.Uploader,
	driveLifecycle drive.FileLifecycle,
) (*JobsBundle, error) {
	if db == nil {
		return nil, fmt.Errorf("build jobs bundle: db is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("build jobs bundle: log is nil")
	}

	repo := sqljobs.NewSQLiteStore(db.DB, log)
	// Wire the ProducesArtifacts gate from the registry so that
	// artifact-producing job types are rejected by the legacy
	// SQLiteStore.Complete path (they must use CompleteWithArtifacts).
	registry := appjobs.Compose()
	repo.SetProducesArtifacts(registry.ProducesArtifactsMap())
	jobLedger, err := platformjobregistry.New(db.DB)
	if err != nil {
		return nil, fmt.Errorf("build jobs bundle: job registry: %w", err)
	}
	dispatcher := appjobs.NewDispatcher()
	// PR-jobs-retry-contract (July 2026): fail-closed 4-arg constructor;
	// ErrRegistryRequired propagates here so composition wiring surfaces
	// at startup rather than at first Enqueue.
	svc, err := appjobs.NewService(repo, dispatcher, log, registry)
	if err != nil {
		return nil, fmt.Errorf("build jobs bundle: %w", err)
	}

	return &JobsBundle{
		Repo:           repo,
		Dispatcher:     dispatcher,
		Service:        svc,
		Facade:         svc,
		JobLedger:      jobLedger,
		VoiceoverRepo:  voiceoverRepo,
		ImagesRepo:     imagesRepo,
		DriveUploader:  driveUploader,
		DriveLifecycle: driveLifecycle,
	}, nil
}
