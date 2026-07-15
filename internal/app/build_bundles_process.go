// Package app owns canonical outbox and media-processor composition.
package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	metadataexport "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox/metadataexport"
	publishdrive "github.com/Marcuss-ops/PipelineGen/internal/application/publish_drive"
	publishoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/publish_outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildOutboxBundle constructs the canonical ingestion dispatcher, event
// registry and worker pool. Core Qdrant handlers fail closed; optional delivery
// and cleanup handlers remain best-effort by contract.
func BuildOutboxBundle(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	repos *RepoBundle,
	qd *QdrantDeps,
	jobs *JobsBundle,
	voiceoverDriver jobsoutbox.VoiceoverCleanupDriver,
	stagingSvc staging.Store,
	repo artifact.Repository,
	drivePublisher delivery.Publisher,
) (*OutboxBundle, IOpaqueStartFunc, error) {
	if qd == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}
	if stagingSvc == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: stagingSvc is required (FASE 3 Push 3.1c; composition must call BuildStagingBundle before BuildOutboxBundle)")
	}
	if repo == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: repo is required (FASE 3 Push 3.1e; composition must inject StagingBundle.Repository)")
	}
	if drivePublisher == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: drivePublisher is required (FASE 3 Push 3.1e; composition must inject DriveBundle.Publisher)")
	}
	if err := validateQdrantIndexerCompatibility(cfg); err != nil {
		return nil, nil, err
	}

	outboxEventsRepo := outboxevents.NewRepository(dbs.dualPool.Writer)
	eventsRegistry := outboxevents.NewHandlerRegistry()
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	var sourceQuerier jobsoutbox.SourceVersionQuerier
	if repos.ClipsRepo != nil {
		sourceQuerier = repos.ClipsRepo
	}

	metadataExportHandler := metadataexport.NewMetadataExportHandler(metadataexport.HandlerDeps{
		Resolver:  sqmetadataexport.NewSQLiteAdapter(dbs.dualPool.Writer),
		Writer:    &filesmetadataexport.FileWriter{},
		OutputDir: cfg.Storage.FullPath("asset_metadata"),
		Log:       log,
	})

	outboxDeps := &jobsoutbox.Deps{
		DeliveryDeps: jobsoutbox.DeliveryDeps{
			DB:          dbs.dualPool.Writer,
			HTTPClient:  httpClient,
			HMACSecrets: hmacSecrets,
			InsecureDev: cfg.Security.DeliveryInsecureDev,
			Jobs:        jobs.Service,
		},
		IndexingDeps: jobsoutbox.IndexingDeps{
			SourceVersionQuerier: sourceQuerier,
		},
		VoiceoverCleanupDeps: jobsoutbox.VoiceoverCleanupDeps{
			VoiceoverCleanupDriver: voiceoverDriver,
		},
	}
	if qd.QdrantDeleter != nil {
		outboxDeps.IndexingDeps.VectorPointDeleter = qd.QdrantDeleter
	}
	if cfg.Qdrant.Enabled && repos.ClipsRepo != nil {
		outboxDeps.IndexingDeps.AssetDeleter = repos.ClipsRepo
	}

	if cfg.Qdrant.Enabled {
		if err := jobsoutbox.RegisterCoreHandlers(eventsRegistry, log, qd.ClipIndexerService, outboxDeps); err != nil {
			return nil, nil, fmt.Errorf("BuildOutboxBundle: register core outbox handlers (fail-closed): %w", err)
		}
	} else {
		var qdrantOffSourceQuerier jobsoutbox.SourceVersionQuerier
		if repos != nil && repos.ClipsRepo != nil {
			qdrantOffSourceQuerier = repos.ClipsRepo
		}
		if err := eventsRegistry.Register(jobsoutbox.NewIndexingHandler(noopIndexClipper{}, qdrantOffSourceQuerier, log)); err != nil {
			return nil, nil, fmt.Errorf("BuildOutboxBundle: register qdrant-off indexing handler: %w", err)
		}
		log.Info("outbox indexing handler registered in no-op mode because qdrant is disabled")
	}
	if err := jobsoutbox.RegisterOptionalHandlers(eventsRegistry, log, outboxDeps, metadataExportHandler); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register optional outbox handlers: %w", err)
	}

	publisherHandler, err := publishoutbox.NewHandler(stagingSvc, log)
	if err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_outbox.NewHandler (fail-fast at construction): %w", err)
	}
	if err := eventsRegistry.Register(publisherHandler); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_outbox handler (fail-closed): %w", err)
	}

	driveUploadHandler, err := publishdrive.NewHandler(repo, drivePublisher, log)
	if err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_drive.NewHandler (fail-fast at construction): %w", err)
	}
	if err := eventsRegistry.Register(driveUploadHandler); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_drive handler (fail-closed): %w", err)
	}

	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": repos.ClipsRepo,
			"stock":   repos.ClipsRepo,
			"artlist": repos.ClipsRepo,
		},
		repos.ClipsRepo,
		log,
	)
	stateWriter := outbox.ClipsStateWriter(repos.ClipsRepo)
	outboxTxMgr := outbox.NewManager(dbs.dualPool.Writer, log)
	dispatcher := outbox.NewDispatcher(multiClipsUp, stateWriter, outboxEventsRepo, outboxTxMgr, log)

	pollInterval := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		pollInterval = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	reclaimInterval := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		reclaimInterval = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	processTimeout := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		processTimeout = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	poolCfg := outboxevents.WorkerPollConfig{
		PollInterval:    pollInterval,
		ProcessTimeout:  processTimeout,
		ReclaimInterval: reclaimInterval,
	}
	eventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, eventsRegistry, log, poolCfg)
	startClosure := func() error { return startOutboxEventsPool(ctx, eventsPool, poolCfg, log) }

	return &OutboxBundle{
		Dispatcher:     dispatcher,
		EventsRepo:     outboxEventsRepo,
		EventsRegistry: eventsRegistry,
		EventsPool:     eventsPool,
		Publisher:      publisherHandler,
		DriveUploader:  driveUploadHandler,
	}, startClosure, nil
}

type noopIndexClipper struct{}

func (noopIndexClipper) IndexClip(context.Context, string) error { return nil }

func startOutboxEventsPool(ctx context.Context, eventsPool *outboxevents.Pool, cfg outboxevents.WorkerPollConfig, log *zap.Logger) error {
	if eventsPool == nil {
		return nil
	}
	concurrent.SafeGo("outbox-events-pool", func() { eventsPool.Start(ctx, 1) })
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := eventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started", zap.Duration("poll_interval", cfg.PollInterval))
	return nil
}

func wireMediaProcessor(
	outboxBundle *OutboxBundle,
	repos *RepoBundle,
	dbs *databases,
	cfg *config.Config,
	publisher delivery.Publisher,
	log *zap.Logger,
) (asset.Processor, error) {
	if outboxBundle == nil || outboxBundle.Dispatcher == nil {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed)")
		return nil, nil
	}
	mutationsDisp, err := newMutationsDispatcherAdapter(outboxBundle.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("wireMediaProcessor: mutations dispatcher adapter: %w", err)
	}
	processor := initMediaProcessor(
		cfg,
		dbs.main,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
		mutationsDisp,
		log,
		publisher,
	)
	log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (F2.8: publisher wired)")
	return processor, nil
}

func newVLMClient(cfg *config.Config) *vlm.Client {
	return vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})
}
