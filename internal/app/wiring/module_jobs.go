package wiring

import (
	"fmt"

	"go.uber.org/zap"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	platformjobregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

// JobsBundle is the Job module's owned runtime surface.
type JobsBundle struct {
	DB         *storage.SQLiteDB
	Repo       *sqljobs.SQLiteStore
	Dispatcher *appjobs.Dispatcher
	Service    *appjobs.Service
	Facade     job.Service
	Broker     appjobs.CompletionPort
	JobLedger  capjobregistry.Registry
	History    appjobs.HistoryReader

	VoiceoverRepo  *assets.VoiceoversRepository
	ImagesRepo     *imagesrepo.ImagesRepository
	DriveUploader  *drive.Uploader
	DriveLifecycle drive.FileLifecycle
}

// BuildJobsBundle constructs the canonical jobs runtime. The raw SQLiteStore is
// retained on the bundle for infrastructure-specific composition needs, while
// the capability Service receives sqljobs.Broker so SQLite driver errors are
// translated at the platform boundary before reaching jobs/queue.
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
	registry := appjobs.Compose()
	repo.SetProducesArtifacts(registry.ProducesArtifactsMap())

	jobLedger, err := platformjobregistry.New(db.DB)
	if err != nil {
		return nil, fmt.Errorf("build jobs bundle: job registry: %w", err)
	}

	dispatcher := appjobs.NewDispatcher()
	broker := sqljobs.NewBroker(repo)
	svc, err := appjobs.NewService(broker, dispatcher, log, registry)
	if err != nil {
		return nil, fmt.Errorf("build jobs bundle: %w", err)
	}

	return &JobsBundle{
		DB:             db,
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
