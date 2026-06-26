package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildOutboxBundle constructs the canonical ingestion outbox + outbox_events.Pool.
//
// PR9-B (June 2026): BuildOutboxBundle returns an IOpaqueStartFunc closure
// that defers the outbox events pool goroutines (Start + shutdown) to the
// lifecycle. The bundle itself is fully populated on return.
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, process *ProcessBundle, jobs *JobsBundle) (*OutboxBundle, IOpaqueStartFunc, error) {
	outboxEventsRepo := outboxevents.NewRepository(dbs.main.DB)

	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": repos.ClipsRepo,
			"stock":   repos.ClipsRepo,
			"artlist": repos.ClipsRepo,
		},
		repos.ClipsRepo,
		log,
	)
	// QDRANT-002 PR7: wire *assets.ClipsRepository as the
	// ClipsStateWriter (same concrete that already implements
	// ClipsUpserter — methods are partitioned by go-type, not by
	// runtime class). Per-state dispatching is unnecessary post
	// PR2.6 media_assets consolidation because every per-source
	// shim funnels into the same SQLite table.
	stateWriter := outbox.ClipsStateWriter(repos.ClipsRepo)
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	dispatcher := outbox.NewDispatcher(multiClipsUp, stateWriter, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path AND canonical delete+outbox_events enqueue path (QDRANT-002 PR7)")

	eventsRegistry := outboxevents.NewHandlerRegistry()

	httpClient := &http.Client{Timeout: 30 * time.Second}

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	// AssetSourceChecker is the load-bearing GetClip port used by
	// the IndexingHandler source_version supersede gate (QDRANT-002
	// item F). The production concrete is the same ClipsRepository
	// already wired into the dispatcher's MultiClipsUpserter; both
	// expose GetClip, so a single instance satisfies the interface.
	// nil ClipsRepo → nil AssetSourceChecker → IndexingHandler skips
	// the supersede gate (acceptable in test dbs; production always
	// wires non-nil).
	var assetSourceChecker jobsoutbox.AssetSourceChecker
	if repos.ClipsRepo != nil {
		if sc, ok := interface{}(repos.ClipsRepo).(jobsoutbox.AssetSourceChecker); ok {
			assetSourceChecker = sc
		}
	}

	outboxDeps := &jobsoutbox.Deps{
		DB:                 dbs.main.DB,
		HTTPClient:         httpClient,
		MetadataDir:        cfg.Storage.FullPath("asset_metadata"),
		HMACSecrets:        hmacSecrets,
		InsecureDev:        cfg.Security.DeliveryInsecureDev,
		Jobs:               jobs.Service,
		AssetSourceChecker: assetSourceChecker,
	}
	// QDRANT-003: wire IndexWriter as QdrantDeleter for index.delete_requested events.
	if process.QdrantDeleter != nil {
		if qd, ok := process.QdrantDeleter.(jobsoutbox.QdrantDeleter); ok {
			outboxDeps.QdrantDeleter = qd
		}
	}
	if err := jobsoutbox.RegisterAll(eventsRegistry, log, process.ClipIndexerService, outboxDeps); err != nil {
		log.Warn("failed to register outbox events handlers", zap.Error(err))
	}

	cfgPoll := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		cfgPoll = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	cfgReclaim := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		cfgReclaim = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	cfgProcess := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		cfgProcess = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	outboxEventsCfg := outboxevents.WorkerPollConfig{
		PollInterval:    cfgPoll,
		ProcessTimeout:  cfgProcess,
		ReclaimInterval: cfgReclaim,
	}
	eventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, eventsRegistry, log, outboxEventsCfg)

	startClosure := func() error {
		return startOutboxEventsPool(ctx, eventsPool, outboxEventsCfg, log)
	}

	return &OutboxBundle{
		Dispatcher:     dispatcher,
		EventsRepo:     outboxEventsRepo,
		EventsRegistry: eventsRegistry,
		EventsPool:     eventsPool,
	}, startClosure, nil
}

// startOutboxEventsPool performs the side-effecting outbox events pool
// initialisation.
//
// Lifecycle-runtime-ownership (June 2026): Pool.Start is void-returning
// so the goroutine is launched via SafeGo (panic-recovery). The shutdown
// goroutine drains the pool on ctx.Done(). The caller treats this as a
// required step — if the goroutine panics, SafeGo recovers and logs the
// panic without crashing the server.
func startOutboxEventsPool(
	ctx context.Context,
	eventsPool *outboxevents.Pool,
	cfg outboxevents.WorkerPollConfig,
	log *zap.Logger,
) error {
	if eventsPool == nil {
		return nil
	}
	concurrent.SafeGo("outbox-events-pool", func() {
		eventsPool.Start(ctx, 1)
	})
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := eventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started", zap.Duration("poll_interval", cfg.PollInterval))
	return nil
}
